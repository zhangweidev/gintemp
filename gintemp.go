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

/**
多模板加载
为模板扩展了一个 layout 函数 ，用于指定当前页面的主模板，
页面由 当前页，主模板，widgets 组成 主模板不指定默认为 layouts/layout.html
*/

var (
	templatesPath string = "./templates" // templates default dir
	viewDir       string = "views"
	layoutDir     string = "layouts"
	widgetDir     string = "widgets"
	tempExt       string = ".html"
	layout_map    map[string]string
)

// 设置模版位置
func TemplateDir(path string) {
	templatesPath = path
}

type layout_object struct {
	Name string
}

func init() {
	layout_map = make(map[string]string)
}

func layout(name string, layout_name interface{}) string {
	switch layout_name.(type) {
	case layout_object:
		lay_obj := layout_name.(layout_object)
		layout_map[lay_obj.Name] = name
	}
	return ""
}

// 加载目录
func loadDir(r multitemplate.Renderer, path string, widgets []string) {
	// 为模板注入一个 layout 函数 ，定义了一个 layout_object ，用来提前获取 layout 配置的模板名称
	// 真实加载时，传入的内容不为  layout_object ，从而实现由模板传入名字。
	funcMap := template.FuncMap{
		"layout": layout,
	}

	views, _ := filepath.Glob(path + "/*")
	for _, view := range views {
		log.Println(view)
		if fileinfo, _ := os.Stat(view); fileinfo.IsDir() {
			loadDir(r, view, widgets)
		} else {
			// 直接加载
			if filepath.Ext(view) == tempExt {

				filename := filepath.Base(view) //获取文件名

				lay_obj := layout_object{
					Name: filename,
				}

				t := template.Must(template.New(filename).Funcs(funcMap).ParseFiles(view))
				var buf bytes.Buffer
				t.Execute(&buf, lay_obj)

				file_Rel, _ := filepath.Rel(fmt.Sprintf("%s/%s", templatesPath, viewDir), view)

				page_layout := fmt.Sprintf("%s/%s/layout.html", templatesPath, layoutDir)

				if v, ok := layout_map[filename]; ok {
					page_layout = fmt.Sprintf("%s/%s/%s.%s", templatesPath, layoutDir, v, tempExt)
				}

				var s []string
				s = append(s, page_layout)
				s = append(s, widgets...)
				s = append(s, view)
				r.AddFromFilesFuncs(file_Rel, funcMap, s...)
				log.Printf("template Load:%s \n", file_Rel)
			}

		}
	}
}

func LoadTemplates() multitemplate.Renderer {
	r := multitemplate.NewRenderer()
	widgets, err := filepath.Glob(fmt.Sprintf("%s/%s/*.%s", templatesPath, widgetDir, tempExt))
	if err != nil {
		panic(err.Error())
	}
	loadDir(r, fmt.Sprintf("%s/%s", templatesPath, viewDir), widgets)
	return r

}
