package gintemp

import (
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gin-contrib/multitemplate"
	"github.com/gin-gonic/gin/render"
)

/** 多模板加载
模板中通过 layout 指令指定当前页面的主模板，加载时静态提取该指令
页面由 当前页面、模版、和 widgets 组成

在页面头上加上

{{layout "layout" .}}

实现模版的使用， 引号内的为模版名称。

*/

// 默认 layout 名（不含扩展名）
const defaultLayoutName = "layout"

// 匹配模板源码中的 layout 指令，如 {{layout "layout" .}}
var layoutRe = regexp.MustCompile(`\{\{\s*-?\s*layout\s+"([^"]+)"`)

type GinTemp struct {
	TempPath  string           // 模版路径
	viewDir   string           // 视图文件夹
	layoutDir string           // 布局路径
	widgetDir string           // 组件路径
	ext       string           // 扩展名
	reload    bool             // 开发期热重载
	funcMap   template.FuncMap // 注入的函数
}

type Option func(*GinTemp)

func WithTempPath(path string) Option {
	return func(g *GinTemp) {
		g.TempPath = path
	}
}

func WithFuncMap(o template.FuncMap) Option {
	return func(g *GinTemp) {
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

// layoutFunc 是 layout 指令的占位实现。
// layout 在加载阶段已由 extractLayout 静态提取，
// 渲染时该指令只需保留为空操作以便模板正常解析。
func layoutFunc(name string, data interface{}) string {
	return ""
}

func NewGinTemp(options ...Option) *GinTemp {
	gintemp := &GinTemp{}
	gintemp.TempPath = "./templates"
	gintemp.viewDir = "views"
	gintemp.layoutDir = "layouts"
	gintemp.widgetDir = "widgets"
	gintemp.ext = ".html"
	gintemp.funcMap = template.FuncMap{
		"layout": layoutFunc,
	}

	for _, option := range options {
		option(gintemp)
	}

	return gintemp
}

//

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

// build 执行一次完整的模板加载
func (g *GinTemp) build() (multitemplate.Renderer, error) {
	// 使用静态 Render 而非 NewRenderer()：
	// NewRenderer() 在 gin debug 模式下返回 DynamicRender，
	// 其 Instance() 会对共享模板调用 Delims()，并发渲染存在数据竞争。
	r := multitemplate.New()

	widgets, err := g.loadFile(filepath.Join(g.TempPath, g.widgetDir))
	if err != nil {
		return nil, err
	}
	views, err := g.loadFile(filepath.Join(g.TempPath, g.viewDir))
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

	viewRoot := filepath.Join(g.TempPath, g.viewDir)
	for _, view := range views {

		name, err := filepath.Rel(viewRoot, view)
		if err != nil {
			return nil, fmt.Errorf("resolve view name %s: %w", view, err)
		}

		// 静态提取 layout 指令（替代旧的"预执行模板 + 副作用写 map"探测）
		src, err := os.ReadFile(view)
		if err != nil {
			return nil, fmt.Errorf("read view %s: %w", view, err)
		}
		layoutName := defaultLayoutName
		if v, ok := extractLayout(src); ok {
			layoutName = v
		}
		if err := checkLayoutName(layoutName); err != nil {
			return nil, fmt.Errorf("view %s: %w", name, err)
		}
		layoutPath := filepath.Join(g.TempPath, g.layoutDir, layoutName+g.ext)

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
		log.Printf("template Load:%s,%s\n", name, layoutPath)
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

// 加载文件
func (g *GinTemp) loadFile(dir string) ([]string, error) {

	files, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		return nil, fmt.Errorf("glob %s: %w", dir, err)
	}
	cfiles := []string{}
	for _, f := range files {
		finfo, err := os.Stat(f)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", f, err)
		}
		if finfo.IsDir() {
			childFiles, err := g.loadFile(f)
			if err != nil {
				return nil, err
			}
			cfiles = append(cfiles, childFiles...)
		} else {
			if filepath.Ext(f) == g.ext {
				cfiles = append(cfiles, f)
			}
		}

	}
	return cfiles, nil
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
		log.Printf("gintemp: check template changes: %v", err)
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
		log.Printf("gintemp: reload failed, keeping old templates: %v", err)
		return
	}
	rr.Renderer = r
	rr.modTime = modTime
	log.Printf("gintemp: templates reloaded\n")
}

// latestModTime 返回模板目录（views/layouts/widgets）下所有文件与目录的最新修改时间，
// 文件新增/删除会更新父目录 mtime，因此也能被检测到
func (g *GinTemp) latestModTime() (time.Time, error) {
	var latest time.Time
	for _, dir := range []string{
		filepath.Join(g.TempPath, g.viewDir),
		filepath.Join(g.TempPath, g.layoutDir),
		filepath.Join(g.TempPath, g.widgetDir),
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

func LoadTemplates(options ...Option) (multitemplate.Renderer, error) {
	gintemp := NewGinTemp(options...)
	return gintemp.Load()
}
