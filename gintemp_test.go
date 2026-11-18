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
	"testing/fstest"
	"time"

	"github.com/gin-contrib/multitemplate"
)

const testdataPath = "testdata/templates"

// renderWith 渲染指定 view 并返回输出内容
func renderWith(t *testing.T, r multitemplate.Renderer, name string) string {
	t.Helper()
	return renderWithData(t, r, name, map[string]interface{}{})
}

// renderWithData 以指定数据渲染 view 并返回输出内容
func renderWithData(t *testing.T, r multitemplate.Renderer, name string, data interface{}) string {
	t.Helper()
	w := httptest.NewRecorder()
	if err := r.Instance(name, data).Render(w); err != nil {
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
		{"inside define ignored", `{{define "x"}}{{layout "inner" .}}{{end}}`, "", false},
		{"inside comment ignored", `{{/* {{layout "x" .}} */}}`, "", false},
		{"inside branch ignored", `{{if .X}}{{layout "x" .}}{{end}}`, "", false},
		{"syntax error", `{{define "x"}}{{end`, "", false},
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
	g := NewGinTemp(WithTempPath(testdataPath))
	fsys := rootFSOf(t, g)

	got, err := g.loadFile(fsys, "views")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{
		"views/auth/login.html",
		"views/home/index.html",
		"views/plain.html",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loadFile(views) = %v, want %v", got, want)
	}

	// 扩展名过滤：note.txt 不应被加载
	got, err = g.loadFile(fsys, "widgets")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want = []string{
		"widgets/footer.html",
		"widgets/header.html",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loadFile(widgets) = %v, want %v", got, want)
	}
}

func TestLoadFileMissingDir(t *testing.T) {
	g := NewGinTemp(WithTempPath(testdataPath))
	got, err := g.loadFile(rootFSOf(t, g), "not-exist")
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
	got, err := g.loadFile(rootFSOf(t, g), filepath.Join("views", "[draft]"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"views/[draft]/v.html"}
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

func TestPartialFunc(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "layouts/layout.html", `[{{partial "header" .}}|{{template "content" .}}|{{partial "footer" .}}|{{partial "hero" .}}]`)
	writeTemplate(t, dir, "widgets/header.html", `{{define "widgets/header"}}H:{{.Title}}{{end}}`)
	writeTemplate(t, dir, "widgets/footer.html", `{{define "widgets/footer"}}F{{end}}`)
	writeTemplate(t, dir, "widgets/hero.html", `HERO-{{.Title}}`) // 顶级内容 widget
	writeTemplate(t, dir, "views/v.html", `{{layout "layout" .}}{{define "content"}}{{partial "widgets/header" .}}|{{partial "footer" .}}{{end}}`)

	r, err := LoadTemplates(WithTempPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	out := renderWithData(t, r, "v.html", map[string]interface{}{"Title": "T"})
	// layout 中 partial 按名渲染 define 型与顶级内容型 widget，
	// view 中 partial("widgets/header") 走完整 define 名
	want := `[H:T|H:T|F|F|HERO-T]`
	if !strings.Contains(out, want) {
		t.Fatalf("partial render wrong, want %q:\n%s", want, out)
	}
}

func TestPartialNotFound(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "layouts/layout.html", `[{{template "content" .}}]`)
	writeTemplate(t, dir, "views/v.html", `{{layout "layout" .}}{{define "content"}}{{partial "missing" .}}{{end}}`)

	r, err := LoadTemplates(WithTempPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	err = r.Instance("v.html", map[string]interface{}{}).Render(w)
	if err == nil || !strings.Contains(err.Error(), `partial "missing" not found`) {
		t.Fatalf("want partial not found error, got %v", err)
	}
}

func TestNestedLayout(t *testing.T) {
	dir := t.TempDir()
	// 最外层 layout 的顶层内容为渲染根，{{block}} 提供默认值
	writeTemplate(t, dir, "layouts/base.html", `<html><aside>{{block "sidebar" .}}S-DEF{{end}}</aside><main>{{block "content" .}}C-DEF{{end}}</main></html>`)
	// 内层 layout 用 layout 指令指定父 layout，define 覆盖外层同名块；
	// 顶层内容（除指令外）不参与渲染
	writeTemplate(t, dir, "layouts/admin.html", `{{layout "base" .}}{{define "sidebar"}}S-ADMIN{{end}}`)
	writeTemplate(t, dir, "views/v.html", `{{layout "admin" .}}{{define "content"}}VIEW{{end}}`)
	// 直接使用 base 的页面回退到默认块
	writeTemplate(t, dir, "views/w.html", `{{layout "base" .}}{{define "content"}}W-CONTENT{{end}}`)

	r, err := LoadTemplates(WithTempPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	out := renderWith(t, r, "v.html")
	if want := `<html><aside>S-ADMIN</aside><main>VIEW</main></html>`; out != want {
		t.Fatalf("nested layout render = %q, want %q", out, want)
	}
	out = renderWith(t, r, "w.html")
	if want := `<html><aside>S-DEF</aside><main>W-CONTENT</main></html>`; out != want {
		t.Fatalf("base layout render = %q, want %q", out, want)
	}
}

func TestNestedLayoutCycleAndMissing(t *testing.T) {
	// 循环继承
	dir := t.TempDir()
	writeTemplate(t, dir, "layouts/a.html", `{{layout "b" .}}`)
	writeTemplate(t, dir, "layouts/b.html", `{{layout "a" .}}`)
	writeTemplate(t, dir, "views/v.html", `{{layout "a" .}}{{define "content"}}x{{end}}`)
	if _, err := LoadTemplates(WithTempPath(dir)); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("want inheritance cycle error, got %v", err)
	}

	// 父 layout 缺失
	dir2 := t.TempDir()
	writeTemplate(t, dir2, "layouts/admin.html", `{{layout "base" .}}`)
	writeTemplate(t, dir2, "views/v.html", `{{layout "admin" .}}{{define "content"}}x{{end}}`)
	if _, err := LoadTemplates(WithTempPath(dir2)); err == nil {
		t.Fatal("want error for missing parent layout, got nil")
	}

	// 超过继承层数上限
	dir3 := t.TempDir()
	for i := 0; i <= maxLayoutDepth; i++ {
		writeTemplate(t, dir3, fmt.Sprintf("layouts/l%d.html", i), fmt.Sprintf(`{{layout "l%d" .}}`, i+1))
	}
	writeTemplate(t, dir3, "views/v.html", `{{layout "l0" .}}{{define "content"}}x{{end}}`)
	if _, err := LoadTemplates(WithTempPath(dir3)); err == nil || !strings.Contains(err.Error(), "deeper than") {
		t.Fatalf("want depth limit error, got %v", err)
	}
}

func TestWidgetsBasenameCollision(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "layouts/layout.html", `[{{template "content" .}}]`)
	writeTemplate(t, dir, "views/v.html", `{{define "content"}}x{{end}}`)
	writeTemplate(t, dir, "widgets/a/header.html", `{{define "widgets/header"}}A{{end}}`)
	writeTemplate(t, dir, "widgets/b/header.html", `{{define "widgets/header2"}}B{{end}}`)

	_, err := LoadTemplates(WithTempPath(dir))
	if err == nil || !strings.Contains(err.Error(), "same file name") {
		t.Fatalf("want widget name collision error, got %v", err)
	}
}

func TestReloadIntervalThrottle(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "layouts/layout.html", `A[{{template "content" .}}]`)
	writeTemplate(t, dir, "views/v.html", `{{define "content"}}ONE{{end}}`)

	r, err := LoadTemplates(WithTempPath(dir), WithReload(true), WithReloadInterval(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if out := renderWith(t, r, "v.html"); !strings.Contains(out, "A[ONE]") {
		t.Fatalf("initial render wrong:\n%s", out)
	}

	// 间隔内的变更不触发重新加载
	writeTemplate(t, dir, "layouts/layout.html", `B[{{template "content" .}}]`)
	bumpModTime(t, dir)
	if out := renderWith(t, r, "v.html"); !strings.Contains(out, "A[ONE]") {
		t.Fatalf("throttled reload should keep old templates:\n%s", out)
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

// rootFSOf 返回 GinTemp 解析出的模板根文件系统
func rootFSOf(t *testing.T, g *GinTemp) fs.FS {
	t.Helper()
	fsys, err := g.rootFS()
	if err != nil {
		t.Fatal(err)
	}
	return fsys
}

func BenchmarkLoad(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := LoadTemplates(WithTempPath(testdataPath)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRender(b *testing.B) {
	r, err := LoadTemplates(WithTempPath(testdataPath))
	if err != nil {
		b.Fatal(err)
	}
	data := map[string]interface{}{}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		if err := r.Instance("home/index.html", data).Render(w); err != nil {
			b.Fatal(err)
		}
	}
}

func TestWithFS(t *testing.T) {
	// go:embed 场景：FS 内路径带 "templates/" 前缀，默认 tempPath 即可命中
	fsys := fstest.MapFS{
		"templates/layouts/layout.html": {Data: []byte(`L[{{template "content" .}}]`)},
		"templates/views/v.html":        {Data: []byte(`{{layout "layout" .}}{{define "content"}}FS-OK{{end}}`)},
	}
	r, err := LoadTemplates(WithFS(fsys))
	if err != nil {
		t.Fatal(err)
	}
	if out := renderWith(t, r, "v.html"); !strings.Contains(out, "L[FS-OK]") {
		t.Fatalf("WithFS render wrong:\n%s", out)
	}
}

func TestWithFSSubPathAndFuncs(t *testing.T) {
	// tempPath 指定 FS 内子目录；widgets、funcMap、渲染名与宿主文件系统模式一致
	fsys := fstest.MapFS{
		"tpl/layouts/layout.html": {Data: []byte(`[{{template "widgets/header" .}}{{template "content" .}}]`)},
		"tpl/views/sub/v.html":    {Data: []byte(`{{layout "layout" .}}{{define "content"}}{{add 1 2}}{{end}}`)},
		"tpl/widgets/header.html": {Data: []byte(`{{define "widgets/header"}}H{{end}}`)},
	}
	r, err := LoadTemplates(
		WithFS(fsys),
		WithTempPath("tpl"),
		WithFuncMap(template.FuncMap{"add": func(x, y int) int { return x + y }}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if out := renderWith(t, r, "sub/v.html"); !strings.Contains(out, "[H3]") {
		t.Fatalf("WithFS sub path render wrong:\n%s", out)
	}
}

func TestWithFSRootPath(t *testing.T) {
	// tempPath 为 "." 时以 FS 根为模板根
	fsys := fstest.MapFS{
		"layouts/layout.html": {Data: []byte(`A[{{template "content" .}}]`)},
		"views/v.html":        {Data: []byte(`{{define "content"}}ROOT{{end}}`)},
	}
	r, err := LoadTemplates(WithFS(fsys), WithTempPath("."))
	if err != nil {
		t.Fatal(err)
	}
	if out := renderWith(t, r, "v.html"); !strings.Contains(out, "A[ROOT]") {
		t.Fatalf("WithFS root path render wrong:\n%s", out)
	}
}

func TestWithFSMissingLayoutReturnsError(t *testing.T) {
	fsys := fstest.MapFS{
		"views/v.html": {Data: []byte(`{{layout "nope" .}}{{define "content"}}x{{end}}`)},
	}
	if _, err := LoadTemplates(WithFS(fsys), WithTempPath(".")); err == nil {
		t.Fatal("want error for missing layout in fs, got nil")
	}
}
