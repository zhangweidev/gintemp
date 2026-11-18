package gintemp

import (
	"fmt"
	"html/template"
	"io/fs"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func TestLoadFileSpecialCharDir(t *testing.T) {
	// 回归测试：目录名含 [ ] 等 glob 特殊字符时（旧实现 filepath.Glob 会失效），
	// 模板仍能被正确发现
	dir := t.TempDir()
	writeTemplate(t, dir, "views/[draft]/v.html", `{{define "content"}}X{{end}}`)

	g := NewGinTemp(WithTempPath(dir))
	root := filepath.Join(dir, "views", "[draft]")
	got, err := g.loadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(root, "v.html")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loadFile(views/[draft]) = %v, want %v", got, want)
	}
}

func TestViewLayoutNameCollision(t *testing.T) {
	// view 与 layout 同名文件时，后解析的 view 会覆盖 layout 根模板，
	// 页面将静默丢掉 layout —— 加载期必须报错
	dir := t.TempDir()
	writeTemplate(t, dir, "layouts/home.html", `[{{template "content" .}}]`)
	writeTemplate(t, dir, "views/home.html", `{{layout "home" .}}{{define "content"}}X{{end}}`)

	_, err := LoadTemplates(WithTempPath(dir))
	if err == nil || !strings.Contains(err.Error(), "same file name") {
		t.Fatalf("want name collision error, got %v", err)
	}

	// view 与默认 layout 同名同样拒绝
	dir2 := t.TempDir()
	writeTemplate(t, dir2, "layouts/layout.html", `[{{template "content" .}}]`)
	writeTemplate(t, dir2, "views/layout.html", `{{define "content"}}X{{end}}`)
	if _, err := LoadTemplates(WithTempPath(dir2)); err == nil || !strings.Contains(err.Error(), "same file name") {
		t.Fatalf("want name collision error for default layout, got %v", err)
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

func TestNoReloadByDefault(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "layouts/layout.html", `A[{{template "content" .}}]`)
	writeTemplate(t, dir, "views/v.html", `{{define "content"}}ONE{{end}}`)

	r, err := LoadTemplates(WithTempPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if out := renderWith(t, r, "v.html"); !strings.Contains(out, "A[ONE]") {
		t.Fatalf("initial render wrong:\n%s", out)
	}

	writeTemplate(t, dir, "layouts/layout.html", `B[{{template "content" .}}]`)
	bumpModTime(t, dir)

	if out := renderWith(t, r, "v.html"); !strings.Contains(out, "A[ONE]") {
		t.Fatalf("templates should not reload without WithReload:\n%s", out)
	}
}

func TestReloadPicksUpChanges(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "layouts/layout.html", `A[{{template "content" .}}]`)
	writeTemplate(t, dir, "views/v.html", `{{define "content"}}ONE{{end}}`)

	r, err := LoadTemplates(WithTempPath(dir), WithReload(true))
	if err != nil {
		t.Fatal(err)
	}
	if out := renderWith(t, r, "v.html"); !strings.Contains(out, "A[ONE]") {
		t.Fatalf("initial render wrong:\n%s", out)
	}

	writeTemplate(t, dir, "layouts/layout.html", `B[{{template "content" .}}]`)
	writeTemplate(t, dir, "views/v.html", `{{define "content"}}TWO{{end}}`)
	bumpModTime(t, dir)

	if out := renderWith(t, r, "v.html"); !strings.Contains(out, "B[TWO]") {
		t.Fatalf("reload did not pick up changes:\n%s", out)
	}
}

func TestReloadKeepsOldOnBrokenTemplate(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "layouts/layout.html", `A[{{template "content" .}}]`)
	writeTemplate(t, dir, "views/v.html", `{{define "content"}}ONE{{end}}`)

	r, err := LoadTemplates(WithTempPath(dir), WithReload(true))
	if err != nil {
		t.Fatal(err)
	}
	if out := renderWith(t, r, "v.html"); !strings.Contains(out, "A[ONE]") {
		t.Fatalf("initial render wrong:\n%s", out)
	}

	// 写坏模板：重新加载失败时应继续使用旧模板
	writeTemplate(t, dir, "views/v.html", `{{define "content"}}{{end`)
	bumpModTime(t, dir)
	if out := renderWith(t, r, "v.html"); !strings.Contains(out, "A[ONE]") {
		t.Fatalf("broken template should keep old output:\n%s", out)
	}

	// 修好后自动恢复
	writeTemplate(t, dir, "views/v.html", `{{define "content"}}FIXED{{end}}`)
	bumpModTime(t, dir)
	if out := renderWith(t, r, "v.html"); !strings.Contains(out, "A[FIXED]") {
		t.Fatalf("reload did not recover after fix:\n%s", out)
	}
}

func TestConcurrentRenderWithReload(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "layouts/layout.html", `A[{{template "content" .}}]`)
	writeTemplate(t, dir, "views/v.html", `{{define "content"}}ONE{{end}}`)

	r, err := LoadTemplates(WithTempPath(dir), WithReload(true))
	if err != nil {
		t.Fatal(err)
	}

	// goroutine 内不能用 t.Fatal，改用 t.Error 上报；
	// 写入用 唯一临时文件 + rename 原子替换，避免 build 读到截断中的文件
	var seq int64
	write := func(content string) {
		p := filepath.Join(dir, "views", "v.html")
		tmp := fmt.Sprintf("%s.%d.tmp", p, atomic.AddInt64(&seq, 1))
		if err := os.WriteFile(tmp, []byte(`{{define "content"}}`+content+`{{end}}`), 0o644); err != nil {
			t.Error(err)
			return
		}
		if err := os.Rename(tmp, p); err != nil {
			t.Error(err)
			return
		}
		future := time.Now().Add(2 * time.Second)
		if err := os.Chtimes(p, future, future); err != nil {
			t.Error(err)
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				w := httptest.NewRecorder()
				if err := r.Instance("v.html", map[string]interface{}{}).Render(w); err != nil {
					t.Error(err)
					return
				}
				out := w.Body.String()
				if !strings.Contains(out, "A[ONE]") && !strings.Contains(out, "A[TWO]") {
					t.Errorf("unexpected output:\n%s", out)
					return
				}
			}
		}(i)
		for k := 0; k < 2; k++ {
			wg.Add(1)
			go func(i, k int) {
				defer wg.Done()
				content := "ONE"
				if (i+k)%2 == 0 {
					content = "TWO"
				}
				write(content)
			}(i, k)
		}
	}
	wg.Wait()
}

// bumpModTime 将目录下所有文件的 mtime 显式推后，保证 mtime 检测能感知变化
func bumpModTime(t *testing.T, root string) {
	t.Helper()
	future := time.Now().Add(2 * time.Second)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(path, future, future)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOptionsCustomDirsAndExt(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "tpl/my-layout.tpl", `<my>{{template "content" .}}</my>`)
	writeTemplate(t, dir, "pages/v.tpl", `{{layout "my-layout" .}}{{define "content"}}X{{end}}`)

	r, err := LoadTemplates(
		WithTempPath(dir),
		WithViewDir("pages"),
		WithLayoutDir("tpl"),
		WithWidgetDir("parts"),
		WithExt("tpl"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if out := renderWith(t, r, "v.tpl"); !strings.Contains(out, "<my>X</my>") {
		t.Fatalf("custom dirs/ext not applied:\n%s", out)
	}
}

func TestWithDefaultLayout(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "layouts/base.html", `BASE[{{template "content" .}}]`)
	writeTemplate(t, dir, "views/v.html", `{{define "content"}}X{{end}}`)

	r, err := LoadTemplates(WithTempPath(dir), WithDefaultLayout("base"))
	if err != nil {
		t.Fatal(err)
	}
	if out := renderWith(t, r, "v.html"); !strings.Contains(out, "BASE[X]") {
		t.Fatalf("custom default layout not applied:\n%s", out)
	}

	// 非法默认 layout 名同样被拒绝
	if _, err := LoadTemplates(WithTempPath(dir), WithDefaultLayout("../evil")); err == nil {
		t.Fatal("want illegal layout name error, got nil")
	}
}

func TestWithSkipPrefix(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "layouts/layout.html", `[{{template "content" .}}]`)
	writeTemplate(t, dir, "views/v.html", `{{define "content"}}OK{{end}}`)
	writeTemplate(t, dir, "views/_partial.html", `{{define "content"}}BROKEN{{end`)
	writeTemplate(t, dir, "views/_drafts/hidden.html", `{{define "content"}}HIDDEN{{end}}`)

	// 不跳过时 _partial.html 语法错误会导致加载失败
	if _, err := LoadTemplates(WithTempPath(dir)); err == nil {
		t.Fatal("want parse error for _partial.html, got nil")
	}

	r, err := LoadTemplates(WithTempPath(dir), WithSkipPrefix("_"))
	if err != nil {
		t.Fatal(err)
	}
	out := renderWith(t, r, "v.html")
	if !strings.Contains(out, "[OK]") || strings.Contains(out, "BROKEN") || strings.Contains(out, "HIDDEN") {
		t.Fatalf("skip prefix not applied:\n%s", out)
	}
}

type testLogger struct {
	mu   sync.Mutex
	msgs []string
}

func (l *testLogger) Printf(format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.msgs = append(l.msgs, fmt.Sprintf(format, args...))
}

func (l *testLogger) joined() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.msgs, "\n")
}

func TestWithLogger(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "layouts/layout.html", `[{{template "content" .}}]`)
	writeTemplate(t, dir, "views/v.html", `{{define "content"}}OK{{end}}`)

	// 默认静默
	r, err := LoadTemplates(WithTempPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	_ = renderWith(t, r, "v.html")

	// 注入 logger 后收到加载日志
	lg := &testLogger{}
	r, err = LoadTemplates(WithTempPath(dir), WithLogger(lg))
	if err != nil {
		t.Fatal(err)
	}
	_ = renderWith(t, r, "v.html")
	if !strings.Contains(lg.joined(), "template Load:v.html") {
		t.Fatalf("logger did not receive load log:\n%s", lg.joined())
	}
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
