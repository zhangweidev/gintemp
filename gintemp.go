// Package gintemp 为 Gin 提供多模板 layout 加载器。
//
// 页面由 当前页面、layout、widgets 三部分组成。
// 在页面头部用 layout 指令指定主模板，引号内为 layout 名（不含扩展名）：
//
//	{{layout "layout" .}}
//
// 该指令在模板加载时静态提取，渲染时为空操作。
package gintemp

import (
	"fmt"
	"html/template"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gin-contrib/multitemplate"
	"github.com/gin-gonic/gin/render"
)

// 默认 layout 名（不含扩展名）
const defaultLayoutName = "layout"

// 匹配模板源码中的 layout 指令，如 {{layout "layout" .}}
var layoutRe = regexp.MustCompile(`\{\{\s*-?\s*layout\s+"([^"]+)"`)

// Logger 是库的日志输出接口，兼容标准库 *log.Logger。
// 默认不输出任何日志，可通过 WithLogger 注入。
type Logger interface {
	Printf(format string, args ...interface{})
}

// GinTemp 管理模板目录约定（views/layouts/widgets）、layout 探测与渲染器加载，
// 通过 NewGinTemp 创建、Option 配置。
type GinTemp struct {
	tempPath      string           // 模板根路径
	viewDir       string           // 视图文件夹
	layoutDir     string           // 布局文件夹
	widgetDir     string           // 组件文件夹
	ext           string           // 模板扩展名（含点），如 ".html"
	defaultLayout string           // 未指定 layout 时使用的默认 layout 名
	skipPrefix    string           // 跳过该前缀命名的文件/目录（如 "_"）
	reload        bool             // 开发期热重载
	logger        Logger           // 日志输出，nil 表示静默
	funcMap       template.FuncMap // 注入的模板函数
}

// Option 用于配置 GinTemp。
type Option func(*GinTemp)

// WithTempPath 设置模板根路径，默认 "./templates"。
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
// 生产环境建议关闭。
func WithReload(enabled bool) Option {
	return func(g *GinTemp) {
		g.reload = enabled
	}
}

// WithLogger 注入库日志输出，用于观察加载与热重载过程；默认静默。
func WithLogger(l Logger) Option {
	return func(g *GinTemp) {
		g.logger = l
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

// build 执行一次完整的模板加载
func (g *GinTemp) build() (multitemplate.Renderer, error) {
	// 使用静态 Render 而非 NewRenderer()：
	// NewRenderer() 在 gin debug 模式下返回 DynamicRender，
	// 其 Instance() 会对共享模板调用 Delims()，并发渲染存在数据竞争。
	r := multitemplate.New()

	widgets, err := g.loadFile(filepath.Join(g.tempPath, g.widgetDir))
	if err != nil {
		return nil, err
	}
	views, err := g.loadFile(filepath.Join(g.tempPath, g.viewDir))
	if err != nil {
		return nil, err
	}

	// widgets 只解析一次，各 view 通过 Clone 共享解析树
	base := template.New("widgets").Funcs(g.funcMap)
	if len(widgets) > 0 {
		if _, err := base.ParseFiles(widgets...); err != nil {
			return nil, fmt.Errorf("parse widgets: %w", err)
		}
	}

	viewRoot := filepath.Join(g.tempPath, g.viewDir)
	for _, view := range views {

		name, err := filepath.Rel(viewRoot, view)
		if err != nil {
			return nil, fmt.Errorf("resolve view name %s: %w", view, err)
		}
		// 模板名统一使用斜杠，保证 Windows 下与调用方 c.HTML("auth/login.html", ...) 一致
		name = filepath.ToSlash(name)

		// 静态提取 layout 指令（替代旧的"预执行模板 + 副作用写 map"探测）
		src, err := os.ReadFile(view)
		if err != nil {
			return nil, fmt.Errorf("read view %s: %w", view, err)
		}
		layoutName := g.defaultLayout
		if v, ok := extractLayout(src); ok {
			layoutName = v
		}
		if err := checkLayoutName(layoutName); err != nil {
			return nil, fmt.Errorf("view %s: %w", name, err)
		}
		layoutPath := filepath.Join(g.tempPath, g.layoutDir, layoutName+g.ext)
		// 同名文件会互相覆盖解析树（后解析的顶掉 layout 根模板），加载期直接报错
		if filepath.Base(layoutPath) == filepath.Base(view) {
			return nil, fmt.Errorf("view %s: layout %s has the same file name %q, rename one of them to avoid template name collision", name, layoutPath, filepath.Base(view))
		}

		// 在共享 widgets 解析树的副本上解析 layout 与 view，
		// 根模板为 layout，渲染时输出 layout 内容
		tmpl, err := base.Clone()
		if err != nil {
			return nil, fmt.Errorf("clone widgets for %s: %w", view, err)
		}
		tmpl, err = tmpl.New(filepath.Base(layoutPath)).ParseFiles(layoutPath, view)
		if err != nil {
			return nil, fmt.Errorf("parse %s with layout %s: %w", view, layoutPath, err)
		}
		r.Add(name, tmpl)
		g.logf("template Load:%s,%s\n", name, layoutPath)
	}
	return r, nil
}

// extractLayout 从模板源码中静态提取 layout 指令指定的模版名称
func extractLayout(src []byte) (string, bool) {
	m := layoutRe.FindSubmatch(src)
	if m == nil {
		return "", false
	}
	return string(m[1]), true
}

// checkLayoutName 校验 layout 名称，防止路径穿越读取模板目录之外的文件
func checkLayoutName(name string) error {
	if strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("illegal layout name %q", name)
	}
	return nil
}

// loadFile 递归加载目录下扩展名匹配的模板文件，
// 跳过以 skipPrefix 命名的文件与目录；目录不存在时返回空列表。
// 使用 os.ReadDir 而非 Glob，目录名含 [ ] 等特殊字符时也能正确遍历。
func (g *GinTemp) loadFile(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}
	var files []string
	for _, e := range entries {
		if g.skipPrefix != "" && strings.HasPrefix(e.Name(), g.skipPrefix) {
			continue
		}
		f := filepath.Join(dir, e.Name())
		if e.IsDir() {
			childFiles, err := g.loadFile(f)
			if err != nil {
				return nil, err
			}
			files = append(files, childFiles...)
			continue
		}
		if filepath.Ext(e.Name()) == g.ext {
			files = append(files, f)
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
	g       *GinTemp
	mu      sync.RWMutex
	modTime time.Time // 上次加载时模板目录的最新 mtime
}

// Instance 返回渲染器；模板文件变化后先自动重新加载
func (rr *reloadRenderer) Instance(name string, data interface{}) render.Render {
	rr.reloadIfChanged()
	rr.mu.RLock()
	defer rr.mu.RUnlock()
	return rr.Renderer.Instance(name, data)
}

func (rr *reloadRenderer) reloadIfChanged() {
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
	var latest time.Time
	for _, dir := range []string{
		filepath.Join(g.tempPath, g.viewDir),
		filepath.Join(g.tempPath, g.layoutDir),
		filepath.Join(g.tempPath, g.widgetDir),
	} {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
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
		if err != nil && !os.IsNotExist(err) {
			return time.Time{}, err
		}
	}
	return latest, nil
}
