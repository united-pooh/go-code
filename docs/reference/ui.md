# `internal/ui`

#### 抽象层

文件: [ui.go](../../internal/ui/ui.go)

##### `type ToolCallEvent struct`

字段:
- `ID`
- `Name`
- `Input`
- `FileMutationKnown` — 是否知道文件变更目标
- `IsFileMutation` — 是否文件变更类工具
- `FileMutation` (`*FileMutationSnapshot`) — 变更前后快照

##### `type ToolResultEvent struct`

字段:
- `ToolUseID`
- `Name`
- `Content`
- `IsError`
- `FileMutationKnown`
- `IsFileMutation`
- `FileMutation` (`*FileMutationSnapshot`)

##### `type FileMutationSnapshot struct`

字段:
- `Before` / `After` — 变更前后内容
- `BeforeExists` / `AfterExists` — 变更前后文件是否存在

用途:
- 只用于 UI 展示真实修改差异，不写入 `message.ToolResult` 或 tracing payload

##### `type SystemEvent struct`

字段:
- `Title`
- `Body`
- `Color`

##### `type UI interface`

方法:
- `OnAssistantDelta(text string) error`
- `OnToolCall(event ToolCallEvent) error`
- `OnToolResult(event ToolResultEvent) error`
- `OnDone() error`

这是 loop 层唯一依赖的 UI 抽象。

##### `type ThinkingDeltaReceiver interface`

方法:
- `OnThinkingDelta(text string) error`

用途:
- 可选扩展：接收模型 thinking 流

##### `type SystemNotifier interface`

方法:
- `OnSystemMessage(event SystemEvent) error`

用途:
- 可选扩展：接收后台任务完成等系统事件

##### `type FileMutationConsumer interface`

方法:
- `ConsumesFileMutations() bool`

用途:
- 只有选择消费的 UI 才会让 runner 去检查变更目标或读取文件快照

#### Headless 实现

文件: [headless.go](../../internal/ui/headless/headless.go)

##### `type UI struct`

字段:
- `out`
- `mu`
- `wrote`

职责:
- 把 assistant delta 写到 stdout
- 把工具事件写成单独行
- 在一轮结束时补换行

##### `New(out io.Writer) *UI`

职责:
- 创建 headless UI

##### `OnAssistantDelta(text) error`

职责:
- 流式输出 assistant 文本

##### `OnToolCall(event) error`

职责:
- 输出工具调用事件

##### `OnToolResult(event) error`

职责:
- 输出工具结果摘要

##### `OnDone() error`

职责:
- 一轮结束时补一个换行

#### Bubble Tea 实现

目录: [bubble](../../internal/ui/bubble/)

职责:
- 完整 TUI：消息历史、输入框、context meter、worktree 状态行、slash 命令、补全弹窗、subagent 面板、Select dock、图片芯片、thinking 折叠
- 使用 ESC 聚合 reader 防止鼠标/键盘序列被读边界切断（`escCoalescingReader`）
- 输出带光标锚点修正的终端流

