package gintemp

import (
	"html/template"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/gin-contrib/multitemplate"
)

const testdataPath = "testdata/templates"

// renderWith 渲染指定 view 并返回输出内容
func renderWith(t *testing.T, r multitemplate.Renderer, name string) string {
	t.Helper()
	w := httptest.NewRecorder()
	if err := r.Instance(name, map[string]interface{}{}).Render(w); err != nil {
		t.Fatalf("render %s: %v", name, err)
	}
	return w.Body.String()
}

func TestExtractLayout(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
		ok   bool
	}{
		{"basic", `{{layout "layout" .}}`, "layout", true},
		{"trim marker", "{{- layout \"auth\" .}}", "auth", true},
		{"spaces", `{{ layout "layout" . }}`, "layout", true},
		{"not first line", "{{define \"content\"}}x{{end}}\n{{layout \"other\" .}}", "other", true},
		{"no directive", `{{define "content"}}x{{end}}`, "", false},
		{"first match wins", `{{layout "a" .}}{{layout "b" .}}`, "a", true},
		{"non literal arg", `{{layout (printf "x") .}}`, "", false},
		{"only keyword", `{{layout}}`, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := extractLayout([]byte(tt.src))
			if ok != tt.ok || got != tt.want {
				t.Fatalf("extractLayout(%q) = (%q, %v), want (%q, %v)", tt.src, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestCheckLayoutName(t *testing.T) {
	bad := []string{"../../etc/passwd", "a/b", `a\b`, "..", "....//x", "/abs"}
	for _, name := range bad {
		if err := checkLayoutName(name); err == nil {
			t.Errorf("checkLayoutName(%q) = nil, want error", name)
		}
	}
	good := []string{"layout", "auth", "admin-main", "x.y"}
	for _, name := range good {
		if err := checkLayoutName(name); err != nil {
			t.Errorf("checkLayoutName(%q) = %v, want nil", name, err)
		}
	}
}

func TestLoadFileRecursionAndExtFilter(t *testing.T) {
	g := NewGinTemp(WithTempPath("testdata"))

	got, err := g.loadFile(filepath.Join(testdataPath, "views"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{
		filepath.Join(testdataPath, "views", "auth", "login.html"),
		filepath.Join(testdataPath, "views", "home", "index.html"),
		filepath.Join(testdataPath, "views", "plain.html"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loadFile(views) = %v, want %v", got, want)
	}

	// 扩展名过滤：note.txt 不应被加载
	got, err = g.loadFile(filepath.Join(testdataPath, "widgets"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want = []string{
		filepath.Join(testdataPath, "widgets", "footer.html"),
		filepath.Join(testdataPath, "widgets", "header.html"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loadFile(widgets) = %v, want %v", got, want)
	}
}

func TestLoadFileMissingDir(t *testing.T) {
	g := NewGinTemp()
	got, err := g.loadFile(filepath.Join("testdata", "not-exist"))
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("loadFile(missing) = %v, want empty", got)
	}
}

func TestLoadFileBadGlobPattern(t *testing.T) {
	g := NewGinTemp()
	if _, err := g.loadFile("testdata/["); err == nil {
		t.Fatal("want glob pattern error, got nil")
	}
}

func TestLoadRendersLayoutWidgetsAndFuncs(t *testing.T) {
	r, err := LoadTemplates(
		WithTempPath(testdataPath),
		WithFuncMap(template.FuncMap{"add": func(x, y int) int { return x + y }}),
	)
	if err != nil {
		t.Fatal(err)
	}

	out := renderWith(t, r, "home/index.html")
	for _, want := range []string{"<html>", "HEADER", "HOME-CONTENT", "FOOTER", "</html>"} {
		if !strings.Contains(out, want) {
			t.Fatalf("home/index.html missing %q:\n%s", want, out)
		}
	}

	// 自定义 layout（静态提取 {{layout "auth" .}}）
	out = renderWith(t, r, "auth/login.html")
	for _, want := range []string{`class="auth"`, "HEADER", "LOGIN-CONTENT"} {
		if !strings.Contains(out, want) {
			t.Fatalf("auth/login.html missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "FOOTER") {
		t.Fatalf("auth layout should not include footer:\n%s", out)
	}

	// 无 layout 指令时回退默认 layout
	out = renderWith(t, r, "plain.html")
	if !strings.Contains(out, "<html>") || !strings.Contains(out, "PLAIN-CONTENT") {
		t.Fatalf("plain.html default layout wrong:\n%s", out)
	}
}

func TestLoadFuncMap(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "layouts/layout.html", `[{{template "content" .}}]`)
	writeTemplate(t, dir, "views/calc.html", `{{layout "layout" .}}{{define "content"}}{{add 1 2}}{{end}}`)

	r, err := LoadTemplates(
		WithTempPath(dir),
		WithFuncMap(template.FuncMap{"add": func(x, y int) int { return x + y }}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if out := renderWith(t, r, "calc.html"); !strings.Contains(out, "[3]") {
		t.Fatalf("funcMap not applied:\n%s", out)
	}
}

func TestLoadMissingLayoutReturnsError(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "views/v.html", `{{layout "nope" .}}{{define "content"}}x{{end}}`)

	defer func() {
		if p := recover(); p != nil {
			t.Fatalf("Load should not panic: %v", p)
		}
	}()
	if _, err := LoadTemplates(WithTempPath(dir)); err == nil {
		t.Fatal("want error for missing layout, got nil")
	}
}

func TestLoadBadTemplateReturnsError(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "layouts/layout.html", `{{template "content" .}}`)
	writeTemplate(t, dir, "views/bad.html", `{{layout "layout" .}}{{define "content"}}{{end`)

	defer func() {
		if p := recover(); p != nil {
			t.Fatalf("Load should not panic: %v", p)
		}
	}()
	if _, err := LoadTemplates(WithTempPath(dir)); err == nil {
		t.Fatal("want parse error, got nil")
	}
}

func TestLoadRejectsLayoutTraversal(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "layouts/layout.html", `{{template "content" .}}`)
	writeTemplate(t, dir, "views/evil.html", `{{layout "../../etc/passwd" .}}{{define "content"}}x{{end}}`)

	_, err := LoadTemplates(WithTempPath(dir))
	if err == nil || !strings.Contains(err.Error(), "illegal layout name") {
		t.Fatalf("want illegal layout name error, got %v", err)
	}
}

func TestConcurrentRender(t *testing.T) {
	r, err := LoadTemplates(WithTempPath(testdataPath))
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"home/index.html", "auth/login.html", "plain.html"}
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := httptest.NewRecorder()
			if err := r.Instance(names[i%len(names)], map[string]interface{}{}).Render(w); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
}

func writeTemplate(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
