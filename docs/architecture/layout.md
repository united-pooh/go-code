# 目录地图与命名

[文档导航](../README.md)

```text
cmd/paw/main.go           可执行入口，仅委托 CLI
internal/
  entry/                 cli、interactive、oneshot、worker、serve、tracer
  app/                   工作区装配、服务、会话接线和关闭
  runtime/               actor、sessionactor、loop、task、goal、plan、streamma
  capability/            model、tool、mcp、skill
  storage/               session、es
  platform/              config、settings、pawpath
  ui/                    UI 契约、bubble、headless、web、complete、theme
  message/               跨层消息契约
  todo/                  待办状态、工具和归档
  tokentracer/            遥测采集、查询与看板
  review/                项目审查与评分报告生成
docs/                    长期项目说明，local 子目录除外
memory/                  本地跨会话记录
scripts/                 构建、开发、发布辅助入口
```

runtime、capability、storage、platform 是文件组织分组，本身不定义 Go 包、不引入转发层。叶子包仍独立，不能把目录排列误读为严格的单向依赖图。例如 Engine 依赖 UI 回调契约，但不依赖 Bubble 或 Web 实现。

## 普通对话的阅读路径

1. [cli.Main](../../internal/entry/cli/main.go) 解析并分发运行模式。
2. [交互入口](../../internal/entry/interactive/run.go) 或 [单轮入口](../../internal/entry/oneshot/run.go) 适配输入输出。
3. [app.BuildWorkspaceRuntime](../../internal/app/runtime_builder.go) 装配依赖，以 [WorkspaceRuntime](../../internal/app/runtime.go) 持有其生命周期。
4. [sessionactor.Host](../../internal/runtime/sessionactor/host.go) 管理会话执行和恢复。
5. [loop.Engine](../../internal/runtime/loop/engine.go) 执行模型请求、工具循环和上下文维护。

worker 入口也使用同一个装配点。serve 启动工作区服务；独立 tracer 只读遥测，不装配对话运行时。

## 命名约束

- `WorkspaceRuntime` 是装配结果，不叫 appContext。
- `SessionHost` 字段持有 `*sessionactor.Host`，不再称为 Runner。
- `Engine` 定义在 `engine.go`；它与 Actor 内核保持独立。
- `TaskRunner`、`TurnExecutor` 等真实执行接口保留其角色名称，不做无差别替换。
- `review` 只表示审查报告生成，不冒充通用 pipeline 执行框架。

## 路径与数据

工具工作区是用户代码的位置；内部状态根由 [pawpath](../../internal/platform/pawpath/paths.go) 解析，尊重 `PAW_CONFIG_HOME`，默认使用用户主目录下的 `.paw`。项目身份继续沿用已有 slug/hash 规则，不因源码迁包改变历史数据归属。

StreamMA 产物位于 `projects/<id>/artifacts/streamma-routing/`，审查报告位于 `projects/<id>/artifacts/review/`（含 `last-run-summary.json`）。报告中的生成文件清单相对报告目录；StreamMA 的纯格式化函数返回相对项目存储根的路径。解析存储路径失败会返回错误，不退回源码目录。测试使用临时配置根，源码树结构测试禁止包内出现 `.paw` 或 `.pipeline-workspace`。

新 Plan 文档写 `docs/local/plans`。旧 `docs/superpowers/plans` 按需无覆盖复制，新副本优先，原文档不修改。事件日志保持原样，恢复时将投影位置映射到当前文档目录。

目录迁移必须同时覆盖生产导入、测试、结构检查、脚本、前端 embed 资产和文档链接。旧 `cmd/agent` 不保留；新入口为 `cmd/paw`，`make build` 默认输出 `~/go/bin/paw`，可通过 `BINDIR` 覆盖输出目录。
