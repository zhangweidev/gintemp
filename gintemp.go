// Package gintemp 为 Gin 提供多模板 layout 加载器。
//
// 页面由 当前页面、layout、widgets 三部分组成。
// 在页面头部用 layout 指令指定主模板，引号内为 layout 名（不含扩展名）：
//
//	{{layout "layout" .}}
//
// 该指令在模板加载时静态提取，渲染时为空操作。
// 模板可从宿主文件系统加载（WithTempPath），也可从任意 fs.FS
// （如 go:embed）加载（WithFS）。
package gintemp

import (
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"text/template/parse"
	"time"

	"github.com/gin-contrib/multitemplate"
	"github.com/gin-gonic/gin/render"
)

// 默认 layout 名（不含扩展名）
const defaultLayoutName = "layout"

// maxLayoutDepth 限制 layout 继承层数，防止配置错误导致的死循环
const maxLayoutDepth = 10

// Logger 是库的日志输出接口，兼容标准库 *log.Logger。
// 默认不输出任何日志，可通过 WithLogger 注入。
type Logger interface {
	Printf(format string, args ...interface{})
}

// GinTemp 管理模板目录约定（views/layouts/widgets）、layout 探测与渲染器加载，
// 通过 NewGinTemp 创建、Option 配置。
type GinTemp struct {
	tempPath       string           // 模板根路径：宿主目录，或 WithFS 时 FS 内的子目录
	viewDir        string           // 视图文件夹
	layoutDir      string           // 布局文件夹
	widgetDir      string           // 组件文件夹
	ext            string           // 模板扩展名（含点），如 ".html"
	defaultLayout  string           // 未指定 layout 时使用的默认 layout 名
	skipPrefix     string           // 跳过该前缀命名的文件/目录（如 "_"）
	reload         bool             // 开发期热重载
	reloadInterval time.Duration    // 热重载变更检查的最小间隔，0 表示每次渲染检查
	logger         Logger           // 日志输出，nil 表示静默
	funcMap        template.FuncMap // 注入的模板函数
	fsys           fs.FS            // 非 nil 时从该 FS 加载模板（如 go:embed）
}

// Option 用于配置 GinTemp。
type Option func(*GinTemp)

// WithTempPath 设置模板根路径，默认 "./templates"；
// 与 WithFS 同时使用时，表示 FS 内的子目录（如 "templates"），"." 表示 FS 根。
func WithTempPath(path string) Option {
	return func(g *GinTemp) {
		g.tempPath = path
	}
}

// WithViewDir 设置视图目录名（相对模板根路径），默认 "views"。
func WithViewDir(dir string) Option {
	return func(g *GinTemp) {
		g.viewDir = dir
	}
}

// WithLayoutDir 设置布局目录名（相对模板根路径），默认 "layouts"。
func WithLayoutDir(dir string) Option {
	return func(g *GinTemp) {
		g.layoutDir = dir
	}
}

// WithWidgetDir 设置组件目录名（相对模板根路径），默认 "widgets"。
func WithWidgetDir(dir string) Option {
	return func(g *GinTemp) {
		g.widgetDir = dir
	}
}

// WithExt 设置模板扩展名，默认 ".html"；省略前导点时自动补全。
func WithExt(ext string) Option {
	return func(g *GinTemp) {
		if ext != "" && !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		g.ext = ext
	}
}

// WithDefaultLayout 设置页面未用 layout 指令指定主模板时的默认 layout 名（不含扩展名），
// 默认 "layout"。
func WithDefaultLayout(name string) Option {
	return func(g *GinTemp) {
		g.defaultLayout = name
	}
}

// WithSkipPrefix 跳过以指定前缀命名的文件与目录（含扩展名前的部分），
// 例如 WithSkipPrefix("_") 可让 _partial.html 之类的片段不被当作页面加载。
func WithSkipPrefix(prefix string) Option {
	return func(g *GinTemp) {
		g.skipPrefix = prefix
	}
}

// WithFuncMap 注入自定义模板函数，可多次调用，后者覆盖同名函数。
func WithFuncMap(o template.FuncMap) Option {
	return func(g *GinTemp) {
		if g.funcMap == nil {
			g.funcMap = template.FuncMap{}
		}
		for k, v := range o {
			g.funcMap[k] = v
		}
	}
}

// WithReload 开启开发期热重载：
// 模板文件（views/layouts/widgets）发生变化后，渲染时自动重新加载，
// 无需重启进程；重新加载失败时继续使用旧模板并输出日志。
// 生产环境建议关闭。注意：fs.FS（如 go:embed）内容不可变，无需开启。
func WithReload(enabled bool) Option {
	return func(g *GinTemp) {
		g.reload = enabled
	}
}

// WithReloadInterval 设置热重载变更检查的最小间隔，默认 0（每次渲染都检查）。
// 模板目录较大或开发期 QPS 较高时可设置如 200*time.Millisecond 降低检查开销，
// 代价是模板变更最多延迟一个间隔才生效。仅在 WithReload 开启时有意义。
func WithReloadInterval(d time.Duration) Option {
	return func(g *GinTemp) {
		g.reloadInterval = d
	}
}

// WithLogger 注入库日志输出，用于观察加载与热重载过程；默认静默。
func WithLogger(l Logger) Option {
	return func(g *GinTemp) {
		g.logger = l
	}
}

// WithFS 从任意 fs.FS 加载模板（如 go:embed 的 embed.FS），
// 实现模板随二进制单文件部署。tempPath 解释为 FS 内的子目录（默认 "./templates"），
// 目录结构约定（views/layouts/widgets）与宿主文件系统模式一致。
func WithFS(fsys fs.FS) Option {
	return func(g *GinTemp) {
		g.fsys = fsys
	}
}

// layoutFunc 是 layout 指令的占位实现。
// layout 在加载阶段已由 extractLayout 静态提取，
// 渲染时该指令只需保留为空操作以便模板正常解析。
func layoutFunc(name string, data interface{}) string {
	return ""
}

// NewGinTemp 按给定 Option 创建 GinTemp。
func NewGinTemp(options ...Option) *GinTemp {
	g := &GinTemp{}
	g.tempPath = "./templates"
	g.viewDir = "views"
	g.layoutDir = "layouts"
	g.widgetDir = "widgets"
	g.ext = ".html"
	g.defaultLayout = defaultLayoutName
	g.funcMap = template.FuncMap{
		"layout": layoutFunc,
	}

	for _, option := range options {
		option(g)
	}

	return g
}

// Load 加载模板并返回多模板渲染器。
// 开启 WithReload 时返回的渲染器会在模板文件变化后自动重新加载。
func (g *GinTemp) Load() (multitemplate.Renderer, error) {
	r, err := g.build()
	if err != nil {
		return nil, err
	}
	if !g.reload {
		return r, nil
	}
	modTime, err := g.latestModTime()
	if err != nil {
		return nil, err
	}
	return &reloadRenderer{g: g, Renderer: r, modTime: modTime}, nil
}

// LoadTemplates 按给定 Option 加载模板并返回多模板渲染器，
// 供 gin 的 r.HTMLRender 直接使用。
func LoadTemplates(options ...Option) (multitemplate.Renderer, error) {
	g := NewGinTemp(options...)
	return g.Load()
}

// rootFS 返回模板根目录对应的文件系统：
// 未设置 WithFS 时为宿主文件系统的 tempPath 目录；
// 设置 WithFS 时为 fsys 内 tempPath 子目录（tempPath 用斜杠分隔，"." 表示 fsys 根）。
func (g *GinTemp) rootFS() (fs.FS, error) {
	if g.fsys == nil {
		return os.DirFS(g.tempPath), nil
	}
	sub := path.Clean(filepath.ToSlash(g.tempPath))
	if sub == "." {
		return g.fsys, nil
	}
	return fs.Sub(g.fsys, sub)
}

// build 执行一次完整的模板加载
func (g *GinTemp) build() (multitemplate.Renderer, error) {
	// 使用静态 Render 而非 NewRenderer()：
	// NewRenderer() 在 gin debug 模式下返回 DynamicRender，
	// 其 Instance() 会对共享模板调用 Delims()，并发渲染存在数据竞争。
	r := multitemplate.New()

	root, err := g.rootFS()
	if err != nil {
		return nil, fmt.Errorf("template root: %w", err)
	}

	widgets, err := g.loadFile(root, g.widgetDir)
	if err != nil {
		return nil, err
	}
	views, err := g.loadFile(root, g.viewDir)
	if err != nil {
		return nil, err
	}

	// widgets 共享同一解析树：同名文件会互相覆盖，加载期直接报错
	if err := checkBasenameCollisions(widgets); err != nil {
		return nil, err
	}

	// widgets 只解析一次，各 view 通过 Clone 共享解析树
	funcs := make(template.FuncMap, len(g.funcMap)+1)
	for k, v := range g.funcMap {
		funcs[k] = v
	}
	var base *template.Template
	if _, ok := funcs["partial"]; !ok {
		// {{partial "name" .}} 按名渲染 widget；base 在闭包调用时已解析完成
		funcs["partial"] = func(name string, data ...interface{}) (template.HTML, error) {
			var d interface{}
			if len(data) > 0 {
				d = data[0]
			}
			return g.execPartial(base, name, d)
		}
	}
	base = template.New("widgets").Funcs(funcs)
	if len(widgets) > 0 {
		if _, err := parseTemplateFiles(base, root, widgets); err != nil {
			return nil, fmt.Errorf("parse widgets: %w", err)
		}
	}

	for _, view := range views {
		name := relName(g.viewDir, view)

		// 静态提取 layout 指令（替代旧的"预执行模板 + 副作用写 map"探测）
		src, err := fs.ReadFile(root, view)
		if err != nil {
			return nil, fmt.Errorf("read view %s: %w", view, err)
		}
		layoutName := g.defaultLayout
		if v, ok := extractLayout(src); ok {
			layoutName = v
		}
		chain, err := g.layoutChain(root, layoutName)
		if err != nil {
			return nil, fmt.Errorf("view %s: %w", name, err)
		}

		// 同名文件会互相覆盖解析树（后解析的顶掉 layout 根模板），加载期直接报错
		files := append(append([]string{}, chain...), view)
		if err := checkBasenameCollisions(files); err != nil {
			return nil, fmt.Errorf("view %s: %w", name, err)
		}

		// 在共享 widgets 解析树的副本上按 继承链从外到内 + view 的顺序解析，
		// 后解析的 define 覆盖先解析的（layout 继承/块覆盖），
		// 根模板为最外层 layout，渲染时输出其内容
		tmpl, err := base.Clone()
		if err != nil {
			return nil, fmt.Errorf("clone widgets for %s: %w", view, err)
		}
		rootTmpl := tmpl.New(path.Base(chain[0]))
		if _, err := parseTemplateFiles(rootTmpl, root, files); err != nil {
			return nil, fmt.Errorf("parse %s with layout %s: %w", view, strings.Join(chain, " -> "), err)
		}
		r.Add(name, rootTmpl)
		g.logf("template Load:%s,%s\n", name, strings.Join(chain, " -> "))
	}
	return r, nil
}

// layoutChain 解析 layout 继承链：layout 文件顶部的 layout 指令指定其父 layout，
// 返回从最外层到最内层的 layout 文件列表（如 [base, admin]），即解析顺序。
// 布局继承采用块覆盖语义：内层 layout 通过 {{define}}/{{block}} 覆盖外层的同名块，
// 最外层 layout 的顶层内容为渲染根。
func (g *GinTemp) layoutChain(fsys fs.FS, name string) ([]string, error) {
	var chain []string
	seen := map[string]bool{}
	for {
		if seen[name] {
			return nil, fmt.Errorf("layout %q: inheritance cycle", name)
		}
		seen[name] = true
		if err := checkLayoutName(name); err != nil {
			return nil, err
		}
		if len(chain) >= maxLayoutDepth {
			return nil, fmt.Errorf("layout %q: inheritance deeper than %d levels", name, maxLayoutDepth)
		}
		p := path.Join(filepath.ToSlash(g.layoutDir), name+g.ext)
		chain = append([]string{p}, chain...)
		src, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil, fmt.Errorf("read layout %s: %w", p, err)
		}
		parent, ok := extractLayout(src)
		if !ok {
			return chain, nil
		}
		name = parent
	}
}

// execPartial 从 widget 模板集合中按名查找模板并渲染，供 {{partial "name" .}} 使用。
// 查找顺序：name、widgetDir/name、name+ext、widgetDir/name+ext，
// 使得 {{partial "header" .}} 既能命中 {{define "widgets/header"}}，
// 也能命中顶级内容的 widgets/header.html。
func (g *GinTemp) execPartial(t *template.Template, name string, data interface{}) (template.HTML, error) {
	widgetDir := filepath.ToSlash(g.widgetDir)
	candidates := []string{
		name,
		path.Join(widgetDir, name),
		name + g.ext,
		path.Join(widgetDir, name+g.ext),
	}
	seen := map[string]bool{}
	for _, cand := range candidates {
		if cand == "" || seen[cand] {
			continue
		}
		seen[cand] = true
		tmpl := t.Lookup(cand)
		if tmpl == nil {
			continue
		}
		var buf strings.Builder
		if err := tmpl.Execute(&buf, data); err != nil {
			return "", fmt.Errorf("partial %q: %w", name, err)
		}
		return template.HTML(buf.String()), nil // #nosec G203 输出来自受信任的模板文件，其中的数据仍按模板规则转义
	}
	return "", fmt.Errorf("gintemp: partial %q not found", name)
}

// checkBasenameCollisions 校验一组模板文件的 base name 两两不同：
// 同名文件解析进同一模板集合时会互相覆盖（后解析的顶掉先解析的）。
func checkBasenameCollisions(files []string) error {
	seen := map[string]string{}
	for _, f := range files {
		b := path.Base(f)
		if prev, ok := seen[b]; ok {
			return fmt.Errorf("templates %s and %s have the same file name %q, rename one of them to avoid template name collision", prev, f, b)
		}
		seen[b] = f
	}
	return nil
}

// parseTemplateFiles 将 files 依次解析进 t 的模板集合，语义与
// template.Template.ParseFiles 一致：模板以文件 base name 命名；
// 与 t 同名的文件内容进入 t 本身，其余文件各自成为关联模板。
// 相比 ParseFiles 直接读取 fs.FS，且不把文件名当 glob 模式处理。
func parseTemplateFiles(t *template.Template, fsys fs.FS, files []string) (*template.Template, error) {
	for _, f := range files {
		b, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, err
		}
		name := path.Base(f)
		tmpl := t
		if name != t.Name() {
			tmpl = t.New(name)
		}
		if _, err := tmpl.Parse(string(b)); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// relName 返回模板文件 p 相对目录 dir 的模板名（统一使用斜杠分隔），
// 作为渲染时 c.HTML(name, ...) 的注册键。
func relName(dir, p string) string {
	dir = strings.TrimSuffix(path.Clean(filepath.ToSlash(dir)), "/")
	if dir == "." {
		return p
	}
	return strings.TrimPrefix(p, dir+"/")
}

// extractLayout 从模板源码顶层静态提取 layout 指令指定的模版名称。
// 基于模板 AST（text/template/parse）识别，只认顶层的
// {{layout "name" ...}} 调用且名字必须是字符串字面量：
// 注释、{{define}} 块与条件分支内部的同名文本不会被误识别。
// 源码语法错误时返回 false，由后续解析阶段报出具体错误。
func extractLayout(src []byte) (string, bool) {
	tree := parse.New("gintemp-scan", map[string]any{})
	tree.Mode |= parse.SkipFuncCheck
	if _, err := tree.Parse(string(src), "{{", "}}", map[string]*parse.Tree{}); err != nil {
		return "", false
	}
	for _, n := range tree.Root.Nodes {
		action, ok := n.(*parse.ActionNode)
		if !ok {
			continue
		}
		for _, cmd := range action.Pipe.Cmds {
			args := cmd.Args
			if len(args) < 2 {
				continue
			}
			id, ok := args[0].(*parse.IdentifierNode)
			if !ok || id.Ident != "layout" {
				continue
			}
			s, ok := args[1].(*parse.StringNode)
			if !ok {
				continue
			}
			return s.Text, true
		}
	}
	return "", false
}

// checkLayoutName 校验 layout 名称，防止路径穿越读取模板目录之外的文件
func checkLayoutName(name string) error {
	if strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("illegal layout name %q", name)
	}
	return nil
}

// loadFile 递归列出目录下扩展名匹配的模板文件，
// 返回相对模板根的斜杠路径；跳过以 skipPrefix 命名的文件与目录；
// 目录不存在时返回空列表。
func (g *GinTemp) loadFile(fsys fs.FS, dir string) ([]string, error) {
	dir = filepath.ToSlash(dir)
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}
	var files []string
	for _, e := range entries {
		if g.skipPrefix != "" && strings.HasPrefix(e.Name(), g.skipPrefix) {
			continue
		}
		p := path.Join(dir, e.Name())
		if e.IsDir() {
			childFiles, err := g.loadFile(fsys, p)
			if err != nil {
				return nil, err
			}
			files = append(files, childFiles...)
			continue
		}
		if path.Ext(e.Name()) == g.ext {
			files = append(files, p)
		}
	}
	return files, nil
}

// logf 输出日志；未注入 Logger 时静默
func (g *GinTemp) logf(format string, args ...interface{}) {
	if g.logger != nil {
		g.logger.Printf(format, args...)
	}
}

// reloadRenderer 开发期热重载渲染器：
// 渲染前检测模板目录 mtime，文件变化后自动重新加载；
// 重新加载失败时继续使用旧模板，避免坏模板打断开发服务。
type reloadRenderer struct {
	multitemplate.Renderer
	g         *GinTemp
	mu        sync.RWMutex
	modTime   time.Time    // 上次加载时模板目录的最新 mtime
	lastCheck atomic.Int64 // 上次变更检查时间（unix 纳秒），配合 reloadInterval 节流
}

// Instance 返回渲染器；模板文件变化后先自动重新加载
func (rr *reloadRenderer) Instance(name string, data interface{}) render.Render {
	rr.reloadIfChanged()
	rr.mu.RLock()
	defer rr.mu.RUnlock()
	return rr.Renderer.Instance(name, data)
}

func (rr *reloadRenderer) reloadIfChanged() {
	if d := rr.g.reloadInterval; d > 0 {
		now := time.Now().UnixNano()
		last := rr.lastCheck.Load()
		// 未到检查时间，或已有并发检查在进行时直接跳过
		if now-last < int64(d) || !rr.lastCheck.CompareAndSwap(last, now) {
			return
		}
	}
	modTime, err := rr.g.latestModTime()
	if err != nil {
		rr.g.logf("gintemp: check template changes: %v", err)
		return
	}
	rr.mu.RLock()
	stale := modTime.After(rr.modTime)
	rr.mu.RUnlock()
	if !stale {
		return
	}
	rr.mu.Lock()
	defer rr.mu.Unlock()
	if !modTime.After(rr.modTime) {
		return
	}
	r, err := rr.g.build()
	if err != nil {
		rr.g.logf("gintemp: reload failed, keeping old templates: %v", err)
		return
	}
	rr.Renderer = r
	rr.modTime = modTime
	rr.g.logf("gintemp: templates reloaded")
}

// latestModTime 返回模板目录（views/layouts/widgets）下所有文件与目录的最新修改时间，
// 文件新增/删除会更新父目录 mtime，因此也能被检测到
func (g *GinTemp) latestModTime() (time.Time, error) {
	root, err := g.rootFS()
	if err != nil {
		return time.Time{}, err
	}
	var latest time.Time
	for _, dir := range []string{g.viewDir, g.layoutDir, g.widgetDir} {
		err := fs.WalkDir(root, filepath.ToSlash(dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if info.ModTime().After(latest) {
				latest = info.ModTime()
			}
			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return time.Time{}, err
		}
	}
	return latest, nil
}
