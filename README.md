gin layout template
---
[![CI](https://github.com/zhangweidev/gintemp/actions/workflows/ci.yml/badge.svg)](https://github.com/zhangweidev/gintemp/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/zhangweidev/gintemp.svg)](https://pkg.go.dev/github.com/zhangweidev/gintemp)

多模板加载
为模板扩展 layout ，用于指定当前页面的模板

虽然现在前端已经有了 Vue 、 React 等框架，但是使用模版有些时候可能是更好的解决方法。 
如果用到模版来解决一些问题，可以尝试使用 `gintemp` 来管理模版。  

## 安装

```bash
go get github.com/zhangweidev/gintemp
```

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
{{layout "layout" .}}
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
	renderer, err := gintemp.LoadTemplates(
		gintemp.WithFuncMap(funcMap()),   // 使用扩展的函数 
		gintemp.WithTempPath("./assets/templates"),
	)
	if err != nil {
		log.Fatal(err)
	}
	r.HTMLRender = renderer

```


更加详细参考 `examples` 目录

## 开发期热重载
模板修改后自动重新加载，无需重启进程：
```
renderer, err := gintemp.LoadTemplates(
    gintemp.WithTempPath("./assets/templates"),
    gintemp.WithReload(true),   // 开发期开启热重载
)
if err != nil {
    log.Fatal(err)
}
r.HTMLRender = renderer
```
渲染时检测 `views` / `layouts` / `widgets` 目录的文件变化（mtime），变化后自动重新加载；重新加载失败（如模板写错）时继续使用旧模板并输出日志，修好后自动恢复。生产环境建议关闭。

## 配置选项

| Option | 说明 | 默认 |
|--------|------|------|
| `WithTempPath` | 模板根路径 | `./templates` |
| `WithViewDir` / `WithLayoutDir` / `WithWidgetDir` | 三个子目录名 | `views` / `layouts` / `widgets` |
| `WithExt` | 模板扩展名 | `.html` |
| `WithDefaultLayout` | 未写 layout 指令时的默认 layout 名 | `layout` |
| `WithSkipPrefix` | 跳过该前缀命名的文件/目录（如片段文件 `_partial.html`） | 不跳过 |
| `WithFuncMap` | 注入自定义模板函数 | — |
| `WithReload` | 开发期热重载 | 关闭 |
| `WithLogger` | 注入日志输出 | 静默 |

## 常见问题
- 渲染时报 `no such template "xxxxx"`：模版中引用了某个定义未实现 ,检查模版中 define 是否实现

layout 指令（如 `{{layout "layout" .}}`）在模板加载时静态提取，不再预执行模板，加载期不会出现 `no such template` 类报错。

## License

[MIT](LICENSE)
 
