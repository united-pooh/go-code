# 开发与扩展

[文档导航](../README.md)

构建入口为 `cmd/paw`，不保留 `cmd/agent`。在仓库根运行 `make build` 默认生成或覆盖 `~/go/bin/paw`（目录不存在时自动创建）；可用 `make build BINDIR=bin` 指定输出目录。`make test` 运行完整 Go 测试，`make check` 运行 vet 和构建检查。直接运行用 `go run ./cmd/paw`。

浏览器工作台开发目录为 `internal/ui/web/ui`，看板仍在 `internal/tokentracer/dashboard`；两套资产分别由所在 Go 包 embed。修改前端后运行该目录的 npm test、npm run build，并运行相应 E2E。

## 自动发布（pre-push hook）

仓库自带一个随仓库分发的 git pre-push 钩子：**每次推送 `dev` 分支时，自动把最新 dev 快照构建成 `paw` 可执行文件并安装到 `~/go/bin/paw`**，方便直接用 `paw` 命令启动。

启用方式（克隆仓库后执行一次）：

```bash
git config core.hooksPath .githooks
```

行为说明：

- 钩子文件：`.githooks/pre-push`（源码副本 `scripts/pre-push.sh`）
- 触发条件：推送目标为 `refs/heads/dev`；其他分支直接放行
- 构建命令：`go build -trimpath -ldflags "-s -w" -o ~/go/bin/paw ./cmd/paw`
- 版本一致性：用被推送的 `refs/heads/dev` 快照构建（不在 dev 上时会自动创建临时 worktree），保证二进制与推送内容一致
- 构建失败会中止本次 push；安装目录可用 `GOBIN` 环境变量覆盖
- 钩子只在本机生效，不会影响 CI

## 扩展点

这里只列当前稳定扩展点。

### 增加一个新工具

位置:
- 新建 `internal/capability/tool/<name>/...`
- 在 [runtime_builder.go](../../internal/app/runtime_builder.go) 注册

要求:
- 实现 `tool.Tool`

最小步骤:
1. 定义 `struct`
2. 实现 `Name`
3. 实现 `Description`
4. 实现 `InputSchema`
5. 实现 `Run`
6. 在 `app.RegisterBuiltinTools` 中 `registry.Register(...)`

可选能力:
- 实现 `IsConcurrencySafe` 启用并行批处理
- 实现 `FileMutationTarget` 让 UI 展示真实文件差异

### 替换 UI

位置:
- 新建一个实现 `ui.UI` 的包

接入点:
- [交互入口](../../internal/entry/interactive/run.go) 或 [单轮入口](../../internal/entry/oneshot/run.go)，通过 `WorkspaceRuntimeOptions.Output` 传入 UI 实现。

### 替换模型提供方

方式 1:
- 直接改 `internal/capability/model` 的 HTTP 实现

方式 2:
- 新建一个实现 `loop.ModelStreamer` 的客户端
- 在 `app.BuildWorkspaceRuntime` 中调整模型客户端装配

### 增加本地命令

接入点:
- Bubble Tea 命令注册表 `internal/ui/bubble/command_registry.go`

### 自定义 Subagent 行为

位置:
- `internal/runtime/task/manager.go` 中的 `Manager` 结构

扩展方式:
- 通过 `Manager` 的 `Config` 结构传入自定义 `Launcher`、`Notifier`、`SettingsProvider`
- 修改 `maxDepth` 限制递归深度
- 实现新的 `Store` 接口替换默认 JSONL 存储

### 接入新 MCP server

位置:
- `~/.paw/mcp.toml`（设置 `PAW_CONFIG_HOME` 时为 `$PAW_CONFIG_HOME/mcp.toml`）
- `internal/capability/mcp/`（协议实现）

扩展方式:
- 添加 `[mcp_servers.<name>]` 表并 `enabled = true`
- 发现的能力自动以 `<server>__<tool>` 名称注册进 `tool.Registry`

## 当前不属于扩展面的函数

下列函数是内部实现细节，不建议作为外部依赖面：

- `loop` 中的输出状态函数
- `model/stream.go` 中的 SSE 解析函数
- `bash.go` 中的输入解码和缓冲细节
- `select/input.go` 中的输入解码与校验

如果要扩展功能，优先从这几个位置下手：
- `tool.Tool`
- `ui.UI`
- `loop.ModelStreamer`
- `app.BuildWorkspaceRuntime`
- `task.Manager`
- `tool.Registry.ReplaceNamespace`（动态工具命名空间）
