gin layout template
---
[![CI](https://github.com/zhangweidev/gintemp/actions/workflows/ci.yml/badge.svg)](https://github.com/zhangweidev/gintemp/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/zhangweidev/gintemp.svg)](https://pkg.go.dev/github.com/zhangweidev/gintemp)

为 Gin 提供多模板 layout 加载：页面用一行 `{{layout "layout" .}}` 指定布局，
widgets 公共组件跨页面共享，支持 layout 继承、`partial` 组件函数、开发期热重载，
并可将模板通过 `go:embed` 编译进二进制单文件部署。

虽然现在前端已经有了 Vue 、 React 等框架，但是使用模版有些时候可能是更好的解决方法。
如果用到模版来解决一些问题，可以尝试使用 `gintemp` 来管理模版。

## 特性

- **layout 指令**：页面头部一行指定布局，加载期静态提取，渲染零开销
- **三段式目录约定**：`views` / `layouts` / `widgets`，直观、免配置
- **layout 继承（嵌套 layout）**：布局再套布局，`define` / `block` 覆盖
- **`partial` 函数**：按名引用 widget，不必记住 define 命名
- **funcMap**：注入自定义模板函数
- **开发期热重载**：改模板免重启，坏模板自动保底旧版
- **`go:embed` 支持**：模板随二进制单文件部署（`WithFS`）
- **安全与健壮**：layout 名校验防路径穿越、同名模板冲突加载期报错、错误全链路返回（不 panic）

## 安装

```bash
go get github.com/zhangweidev/gintemp
```

## 快速开始

目录结构：

```
templates/
├── layouts/        # 页面整体结构
│   └── layout.html
├── widgets/        # 公共组件
│   ├── header.html
│   └── footer.html
└── views/          # 页面内容
    ├── home/
    │   └── index.html
    └── auth/
        └── login.html
```

页面文件头部用 layout 指令指定布局（引号内为 layout 名，不含扩展名）：

```html
{{layout "layout" .}}
{{define "title"}}首页{{end}}

{{define "content"}}Hi, this is article template{{end}}

{{define "info"}}this right{{end}}
```

```go
r := gin.Default()
renderer, err := gintemp.LoadTemplates(
    gintemp.WithTempPath("./templates"),
)
if err != nil {
    log.Fatal(err)
}
r.HTMLRender = renderer

r.GET("/", func(c *gin.Context) {
    // 模板名为相对 views 目录的斜杠路径
    c.HTML(http.StatusOK, "home/index.html", gin.H{})
})
```

## 渲染流程

```
 views/home/index.html        layouts/layout.html          widgets/*.html
 ┌───────────────────┐   ┌─────────────────────────┐   ┌────────────────┐
 │ {{layout "layout"}}│   │ <html>                  │   │ {{define       │
 │ {{define           │   │  {{template "widgets/   │   │  "widgets/..."}}│
 │  "content"}}...    │   │   header" .}}           │   └───────┬────────┘
 └─────────┬─────────┘   │  {{template "content"}} │           │
           │             └───────────┬─────────────┘           │
           │  ① 加载期静态提取 layout │                         │
           └──────────────┬──────────┘                         │
                          │  ② widgets 解析一次，各页面 Clone 共享
                          │◄───────────────────────────────────┘
                          ▼
            ③ 解析 layout + view（后解析的 define 覆盖先解析的）
                          ▼
        c.HTML("home/index.html") → 输出 layout 骨架 + 页面内容
```

## 布局继承（嵌套 layout）

layout 文件顶部也可以用 layout 指令指定**父 layout**，通过 `define` / `block`
覆盖外层同名块（内层定义优先，页面定义最优先）：

```html
<!-- layouts/base.html：最外层骨架，block 提供默认值 -->
<html>
<aside>{{block "sidebar" .}}默认侧栏{{end}}</aside>
<main>{{block "content" .}}{{end}}</main>
</html>

<!-- layouts/admin.html：继承 base，覆盖 sidebar；顶层内容不参与渲染 -->
{{layout "base" .}}
{{define "sidebar"}}管理后台侧栏{{end}}

<!-- views/admin/user.html：选择 admin，覆盖 content -->
{{layout "admin" .}}
{{define "content"}}用户列表{{end}}
```

渲染 `admin/user.html` 输出：

```html
<html><aside>管理后台侧栏</aside><main>用户列表</main></html>
```

继承链有环检测与层数上限（10 层）防护。

## widgets 与 partial

widget 既可通过 `{{template}}` 按 define 名引用，也可用 `partial` 函数按文件名引用：

```html
<!-- widgets/header.html，两种写法都支持 -->
{{define "widgets/header"}}<header>{{.Title}}</header>{{end}}
<!-- 或直接写顶级内容：widgets/hero.html → <section>...</section> -->
```

```html
{{partial "header" .}}        <!-- 依次查找 header、widgets/header（含扩展名） -->
{{partial "widgets/header" .}} <!-- 完整 define 名 -->
{{partial "hero" .}}           <!-- 顶级内容的 widgets/hero.html -->
```

## funcMap

模版需要使用自定义函数来格式化输出时，需要用到 funcMap 来扩展：

```go
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
    gintemp.WithFuncMap(funcMap()), // 使用扩展的函数
    gintemp.WithTempPath("./templates"),
)
if err != nil {
    log.Fatal(err)
}
r.HTMLRender = renderer
```

## go:embed 单文件部署

模板可编译进二进制，运行时不依赖外部模板目录：

```go
//go:embed templates
var templatesFS embed.FS

renderer, err := gintemp.LoadTemplates(gintemp.WithFS(templatesFS))
```

目录结构约定与文件系统模式一致；`WithTempPath` 此时表示 FS 内的子目录（默认 `templates`，`.` 为 FS 根）。
完整示例见 `examples/embed`。

## 开发期热重载

模板修改后自动重新加载，无需重启进程：

```go
renderer, err := gintemp.LoadTemplates(
    gintemp.WithTempPath("./templates"),
    gintemp.WithReload(true), // 开发期开启热重载
    // gintemp.WithReloadInterval(200 * time.Millisecond), // 可选：变更检查节流
)
```

渲染时检测 `views` / `layouts` / `widgets` 目录的文件变化（mtime），变化后自动重新加载；
重新加载失败（如模板写错）时继续使用旧模板并输出日志，修好后自动恢复。
生产环境建议关闭。`go:embed` 内容不可变，无需开启。

## 配置选项

| Option | 说明 | 默认 |
|--------|------|------|
| `WithTempPath` | 模板根路径（`WithFS` 时为 FS 内子目录） | `./templates` |
| `WithViewDir` / `WithLayoutDir` / `WithWidgetDir` | 三个子目录名 | `views` / `layouts` / `widgets` |
| `WithExt` | 模板扩展名 | `.html` |
| `WithDefaultLayout` | 未写 layout 指令时的默认 layout 名 | `layout` |
| `WithSkipPrefix` | 跳过该前缀命名的文件/目录（如片段文件 `_partial.html`） | 不跳过 |
| `WithFuncMap` | 注入自定义模板函数 | — |
| `WithFS` | 从 `fs.FS`（如 `go:embed`）加载模板 | 宿主文件系统 |
| `WithReload` | 开发期热重载 | 关闭 |
| `WithReloadInterval` | 热重载变更检查间隔（0 表示每次渲染检查） | `0` |
| `WithLogger` | 注入日志输出 | 静默 |

## 与同类方案对比

| | Gin `LoadHTMLGlob` | `multitemplate` 裸用 | `goview` | **gintemp** |
|---|---|---|---|---|
| layout 组合 | 不支持 | 手写 `r.Add` 样板 | 目录约定 | 页面一行指令，可继承嵌套 |
| 目录约定 | 无 | 自定 | 有 | views / layouts / widgets |
| 公共组件 | 手动 include | 手动 | 支持 | widgets + `partial` |
| 热重载 | 不支持 | 不支持 | 支持 | 支持（含坏模板保底、可节流） |
| `go:embed` | 不支持 | 需自行封装 | 部分 | `WithFS` 原生支持 |
| 错误处理 | panic | panic | 返回 error | 全链路返回 error |

## 常见问题

- 渲染时报 `no such template "xxxxx"`：模版中引用了某个定义未实现，检查模版中 define 是否实现。
- 加载时报 `... has the same file name ...`：view / layout / widget 之间存在同名文件，
  同名文件会互相覆盖解析树，请重命名其一（如 `views/home/index.html` 而非 `views/index.html`）。
- layout 指令（如 `{{layout "layout" .}}`）在模板加载时静态提取（基于 AST，仅识别顶层指令），
  需写在模板顶层，不要放进 `{{define}}` 或 `{{if}}` 内部；渲染时该指令为空操作。

## License

[MIT](LICENSE)
