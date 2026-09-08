# 执行层次与职责

[文档导航](../README.md)

可执行文件 → entry 模式适配 → app.WorkspaceRuntime → sessionactor.Host → loop.Engine → 模型与工具。

entry 负责输入输出与进程生命周期；app 负责依赖装配和会话接线；Host 负责调度与恢复；Engine 负责对话和工具循环。runtime/capability/storage/platform 是目录分组，不是新的 Go 包或强行套入的严格层级；跨包依赖以实际契约为准。Engine 仍不导入 Actor 内核，结构测试继续检查生产装配经 Host。

## 分层

下面按消息、模型、工具、UI、对话引擎介绍核心概念；它们不是完整的运行时依赖图，当前装配和会话边界见首页“源码导航”。

补充:
- `app` 管理工作区运行时；`sessionactor` 管理会话生命周期；`actor`/`es` 提供事件执行与持久化基础；`session`、`settings`、`task` 分别负责会话存储、用户设置、子任务调度。
- `internal/capability/skill` 负责发现本地 `skill-name/SKILL.md`、解析输入中的 skill 引用，并把选中的 skill 文件格式化为当前 turn 的 system context。
- `internal/runtime/streamma` 是独立的内存版 multi-agent runtime，通过 `/streamma <prompt>` 接入 `loop.Engine` 的显式分支；交互入口使用真实 task worker，生产版 NATS、Postgres、MinIO 适配器仍未接入。
- `internal/tokentracer` 提供 token 用量追踪与本地 HTTP dashboard。

### 1. `message`

职责:
- 定义统一消息模型

边界:
- 不知道模型协议细节
- 不知道 UI
- 不知道工具注册和执行

#### 核心类型

##### `type Role string`

三个角色常量：
- `RoleSystem` — 系统提示
- `RoleUser` — 用户消息
- `RoleAssistant` — 助手（模型）消息

##### `type Message struct`

字段:
- `Role` — 消息角色
- `Content` — 文本内容
- `Parts` (`[]ContentPart`) — 有序 text/image 片段（富文本多模态消息；`Content` 保留为兼容表示）
- `ToolUse` (`*ToolCall`) — 工具调用请求（assistant 发出，单个）
- `ToolUses` (`[]ToolCall`) — 工具调用请求（assistant 发出，多个）
- `ToolResult` (`*ToolResult`) — 工具执行结果（user 角色发出，单个）
- `ToolResults` (`[]ToolResult`) — 工具执行结果（user 角色发出，多个）

##### `type ContentPart struct`

字段:
- `Type` — `text` 或 `image`
- `Text` — 文本内容
- `Image` (`*ImagePart`) — 图片片段

##### `type ImagePart struct`

字段:
- `MIMEType` — 图片 MIME 类型
- `Attachment` — 附件相对引用（持久化时写入，不写 base64）
- `Data` (`[]byte`) — 仅在提交/物化请求时内存持有，JSON 序列化忽略

##### `type ToolCall struct`

字段:
- `ID` — 调用唯一标识
- `Name` — 工具名称
- `Input` (`json.RawMessage`) — 调用参数（原始 JSON 字节，延迟解析）

##### `type ToolResult struct`

字段:
- `ToolUseID` — 对应 ToolCall 的 ID
- `Content` — 执行结果文本
- `IsError` — 是否出错

消息模型遵循 Claude/Anthropic 风格的对话格式：assistant 消息可包含工具调用请求，user 消息可包含工具执行结果，实现多轮工具闭环。

### 2. `model`

职责:
- 负责和模型服务通信
- 负责 HTTP 请求/响应
- 负责把流式响应转成事件

边界:
- 不负责对话循环
- 不负责工具执行
- 不负责 stdout 渲染

### 3. `tool`

职责:
- 定义工具接口
- 提供工具注册表
- 提供具体工具实现

边界:
- 不知道模型
- 不知道 UI
- 不知道 turn loop

### 4. `ui`

职责:
- 接收 loop 产生的渲染事件
- 决定如何输出到终端

边界:
- 不调用模型
- 不执行工具

### 5. `loop`

职责:
- 驱动 agent turn
- 调模型
- 识别 tool use
- 调工具
- 回灌 tool result
- 维护内存中的对话历史
- 记录最近一次模型流返回的真实 usage，供 context meter 展示
- 读取 `AGENTS.md` 项目指令并注入 system prompt
- 触发上下文自动压缩（保留完整 journal）
- 通过 Turn Journal 增量持久化每轮消息（turn_started / assistant_message / tool_result / turn_completed / turn_failed）

这是当前系统的协调层。

## 运行链路

当前运行链路如下:

```text
main
  -> entry/cli.Main → 对应 entry 模式
  -> app.BuildWorkspaceRuntime
  -> sessionactor.Host.RunTurn
  -> session actor 命令 / 恢复
  -> loop.Engine.RunRichTurnWithTiming
      -> model.Client.StreamMessage
      -> ui.OnThinkingDelta / ui.OnAssistantDelta / ui.OnDone
      -> parse tool_use
      -> tool.Registry.Get
      -> tool.Run
      -> ui.OnToolCall / ui.OnToolResult
      -> model.Client.StreamMessage
      -> final assistant message
```

Subagent 工作模式链路:

```text
main (-task-worker)
  -> read worker.start from stdin
  -> runWorkerTurn
  -> app.BuildWorkspaceRuntime (with WorkerContext depth/parentTaskID)
  -> sessionactor.Host.RunTurn
  -> write worker.result to stdout
  -> exit
```

## 抽象层总结

当前有 3 个核心抽象层。

### 1. 模型抽象

接口: `loop.ModelStreamer`

用途:
- 隔离 `Runner` 和具体模型客户端

替换方式:
- 只要实现 `StreamMessage(...)`，就可以替换 `model.Client`

### 2. 工具抽象

接口: `tool.Tool`

用途:
- 隔离 loop 和具体工具实现

替换方式:
- 新工具实现接口后注册即可
- 可选扩展：`tool.ConcurrencySafeTool`（并发批处理）、`tool.FileMutationTool`（文件变更快照）

### 3. UI 抽象

接口: `ui.UI`

用途:
- 隔离 loop 和具体输出方式

替换方式:
- 可将 `headless` 换成 TUI、日志型 UI、测试 UI
- 可选扩展：`ui.ThinkingDeltaReceiver`、`ui.SystemNotifier`、`ui.FileMutationConsumer`
