gin layout template
---
多模板加载
为模板扩展 layout ，用于指定当前页面的模板

虽然现在前端已经有了 Vue 、 React 等框架，但是使用模版有些时候可能是更好的解决方法。 
如果用到模版来解决一些问题，可以尝试使用 `gintemp` 来管理模版。  

## 页面组成
- layout
    页面整体的结构
- widgets
    公共组件部分
- page 
    页面内容

在页面头上加上 layout 用于指定整体结构
```
{{layout "layout" .}}
```
实现模版的使用， 引号内的为模版名称。

## 例子 

```
{{layout "layout" .}}}
{{define "title"}}首页{{end}}

{{define "content"}}Hi, this is article template{{end}}

{{define "info"}}
this right 
{{end}}
```

文件头指定了使用名为 `layout` 的模版


### funcMap 
模版需要使用自定义函数来格式化输出时，需要用到 funcMap 来扩展
例如：
```
// 扩展一个除法函数
func funcMap() template.FuncMap {

	return template.FuncMap{
		"div": func(x int, y int) int {
			return x / y
		}, 
	} 
}

 // ----------

    r := gin.Default()
	r.HTMLRender = gintemp.LoadTemplates(
		gintemp.WithFuncMap(funcMap()),   // 使用扩展的函数 
		gintemp.WithTempPath("./assets/templates"),
	) 

```


更加详细参考 `_example` 目录

## isuse
- template Must execute : xxxxx: no such template "title"

模版中出现了某个定义未实现 ,检查模版
 
