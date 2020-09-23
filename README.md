gin layout template
---
多模板加载
为模板扩展了 layout ，用于指定当前页面的模板


## 页面组成
页面由 
- 当前页面
- 模版
- widgets


在页面头上加上
```
{{layout "layout" .}}
```
实现模版的使用， 引号内的为模版名称。

## example 

```
{{layout "layout" .}}}
{{define "title"}}首页{{end}}

{{define "content"}}Hi, this is article template{{end}}

{{define "info"}}
this right 
{{end}}
```

文件头指定了使用名为 `layout` 的模版


更加详细参考 `_example` 目录
 
