package gintemp

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"os"
	"path/filepath"

	"github.com/gin-contrib/multitemplate"
)

/** 多模板加载
为模板扩展了一个 layout 函数 ，用于指定当前页面的主模板
页面由 当前页面、模版、和 widgets 组成

在页面头上加上

{{layout "layout" .}}

实现模版的使用， 引号内的为模版名称。

*/

type LayoutObject struct {
	Name string
}

type GinTemp struct {
	TempPath  string            // 模版路径
	viewDir   string            // 视图文件夹
	layoutDir string            // 布局路径
	widgetDir string            // 组件路径
	ext       string            // 扩展名
	layoutMap map[string]string // 页面与模版的对应
	funcMap   template.FuncMap  // 注入的函数
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

func NewGinTemp(options ...Option) *GinTemp {
	gintemp := &GinTemp{}
	gintemp.TempPath = "./templates"
	gintemp.viewDir = "views"
	gintemp.layoutDir = "layouts"
	gintemp.widgetDir = "widgets"
	gintemp.ext = ".html"
	gintemp.layoutMap = make(map[string]string)
	gintemp.funcMap = template.FuncMap{
		"layout": gintemp.LayoutFunc,
	}

	for _, option := range options {
		option(gintemp)
	}

	return gintemp
}

func (g *GinTemp) LayoutFunc(name string, layout interface{}) string {

	obj, ok := layout.(LayoutObject)
	if ok {
		g.layoutMap[obj.Name] = name
	}
	return ""
}

//

func (g *GinTemp) Load() multitemplate.Renderer {
	r := multitemplate.NewRenderer()

	widgets := g.loadFile(filepath.Join(g.TempPath, g.widgetDir))
	fmt.Println("widgets", widgets)
	views := g.loadFile(filepath.Join(g.TempPath, g.viewDir))
	fmt.Println("views", views)

	for _, view := range views {

		name, _ := filepath.Rel(fmt.Sprintf("%s/%s", g.TempPath, g.viewDir), view)
		layoutObject := LayoutObject{
			Name: name,
		}

		v := []string{view}
		v = append(v, widgets...)
		t := template.Must(template.New(filepath.Base(view)).Funcs(g.funcMap).ParseFiles(v...))
		
		var buf bytes.Buffer
		err := t.Execute(&buf, layoutObject)

		if err != nil {
			fmt.Println("template Must execute :", view, err)
		}

		layoutPath := fmt.Sprintf("%s/%s/layout.html", g.TempPath, g.layoutDir)
		if v, ok := g.layoutMap[name]; ok {
			layoutPath = fmt.Sprintf("%s/%s/%s%s", g.TempPath, g.layoutDir, v, g.ext)
		}

		var s []string
		s = append(s, layoutPath)
		s = append(s, widgets...)
		s = append(s, view)
		r.AddFromFilesFuncs(name, g.funcMap, s...)
		log.Printf("template Load:%s,%s\n", name, layoutPath)
	}
	return r
}

// 加载文件
func (g *GinTemp) loadFile(dir string) []string {

	files, _ := filepath.Glob(fmt.Sprintf("%s/*", dir))
	cfiles := []string{}
	for _, f := range files {
		if finfo, _ := os.Stat(f); finfo.IsDir() {
			files_child := g.loadFile(f)
			cfiles = append(cfiles, files_child...)
		} else {
			if filepath.Ext(f) == g.ext {
				cfiles = append(cfiles, f)
			}
		}

	}
	return cfiles
}

func LoadTemplates(options ...Option) multitemplate.Renderer {
	gintemp := NewGinTemp(options...)
	return gintemp.Load()
}
