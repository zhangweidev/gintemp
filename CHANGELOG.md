# Changelog

## v1.0.0 (2026-11-18)

首个稳定版本。核心 API（`LoadTemplates` / `NewGinTemp` / `Option` 系列）自此冻结，
后续破坏性变更将走 v2。

### 新增

- **`WithFS(fs.FS)`**：从任意 `fs.FS`（如 `go:embed` 的 `embed.FS`）加载模板，
  模板随二进制单文件部署；目录结构约定与文件系统模式一致。
  内部 IO 全部基于 `io/fs`，模板注册名天然为斜杠路径。
- **`{{partial "name" .}}`**：按名渲染 widget（支持 define 型与顶级内容型），
  查找顺序为 `name`、`<widgetDir>/name`（各含扩展名变体）。
- **layout 继承（嵌套 layout）**：layout 文件顶部的 layout 指令指定父 layout，
  `{{define}}` / `{{block}}` 覆盖外层同名块，最外层 layout 的顶层内容为渲染根；
  带环检测与层数上限（10）防护。
- **`WithReloadInterval`**：热重载变更检查节流，默认 0（每次渲染检查）。
- 基准测试 `BenchmarkLoad` / `BenchmarkRender`（参考值：Apple M5 Pro 上
  Load ≈ 0.64ms/op，Render ≈ 0.58µs/op）。

### 修复

- Windows 下模板注册名含反斜杠导致 `c.HTML("auth/login.html", ...)` 匹配不到模板。
- view 与 layout 同名文件时静默覆盖 layout 根模板 —— 现在加载期报错并提示重命名。
- widgets 目录下同名文件静默互相覆盖 —— 现在加载期报错。
- 模板目录名含 `[` `]` 等 glob 特殊字符时文件发现失效（`filepath.Glob` → `fs.ReadDir`）。

### 变更

- layout 指令改为基于模板 AST（`text/template/parse`）提取：只识别**顶层**的
  `{{layout "name" .}}` 字符串字面量调用；`{{define}}` / `{{if}}` / 注释内部的同名文本
  不再被误识别为布局声明。语法错误由解析阶段报出。
- layout 文件顶部的 layout 指令语义从"忽略"变为"指定父 layout"（布局继承）。
- `go` 指令下限 1.23 → 1.24；`quic-go` 升至 v0.59.1（清除其传递依赖漏洞）。

### 已知安全说明

`golang.org/x/net` 传递依赖的公开漏洞修复版（v0.60.0）要求 `go 1.26`，
为维持较低的 go 下限暂不升级；该模块为间接依赖，使用方可按自身约束
`go get golang.org/x/net@latest` 自行提升。Go 标准库漏洞请升级 Go 补丁版解决。

## v0.0.x

历史版本（2020–2025）见 [git tags](https://github.com/zhangweidev/gintemp/tags)。
