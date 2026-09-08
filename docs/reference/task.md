# `internal/runtime/task`

文件: [manager.go](../../internal/runtime/task/manager.go)

#### `type Manager struct`

字段:
- `model` (`loop.ModelStreamer`) — 子 agent 使用的模型客户端
- `store` (`Store`) — 会话存储
- `root` — 工作区根路径
- `settings` (`SettingsProvider`) — 设置提供者
- `notifier` (`Notifier`) — 通知器（用于 UI 通知）
- `launcher` (`Launcher`) — 进程启动器
- `registry` (`taskRegistry`) — 任务注册表
- `depth` / `maxDepth` — 当前递归深度 / 最大深度（默认 4）
- `parentTaskID` — 父任务 ID
- `actors` — TaskActor / RegistryActor 宿主；任务状态经 actor 和注册表投影，不再由 Manager 单独维护 tasks/running map

职责:
- 管理子 agent 任务的生命周期（创建、运行、查询、停止）
- 控制递归嵌套深度防止无限循环

##### `NewManager(cfg Config) *Manager`

职责:
- 创建 Manager 实例，默认 maxDepth=4

##### `Run(ctx, req) (Result, error)`

职责:
- 同步运行子 agent，等待执行完成并返回结果

##### `Stream(ctx, req) (Stream, error)`

职责:
- 以同步方式启动子 agent 并返回流式事件 channel

##### `Launch(ctx, req) (TaskSnapshot, error)`

职责:
- 后台启动子 agent，立即返回任务快照，异步等待完成

##### `Stop(ctx, id) (TaskSnapshot, error)`

职责:
- 停止指定 ID 的后台任务

##### `Status(id) (TaskSnapshot, bool)`

职责:
- 查询指定任务的状态

##### `ListTasks() []TaskSnapshot`

职责:
- 列出所有任务（内存 + 磁盘），按启动时间排序

##### `TotalTaskTokens(parentSessionID string) int`

职责:
- 返回指定父会话下全部已完成任务的 token 总量

##### 内置 Tool 实现

Manager 同文件中实现了四个供 LLM 调用的工具：

- **`Task`** — 启动子 agent，支持 sync/background 两种运行模式
- **`TaskStatus`** — 查询任务状态，按 ID 查或列出所有
- **`TaskStop`** — 按 ID 停止运行中的任务
- **`TaskWait`** — 等待指定任务中的任意一个结束或超时

#### `type Request struct`

字段:
- `ParentSessionID` — 父会话 ID（fork 模式使用）
- `Prompt` — 子任务提示
- `Description` — 任务描述
- `ContextMode` — `"empty"`（空上下文）或 `"fork"`（继承父会话）
- `RunMode` — `"sync"` 或 `"background"`

#### `type Result struct`

字段:
- `AgentID` — 子 agent ID
- `SessionID` — 子会话 ID
- `Content` — 执行结果文本
- `ExitCode` — 退出码
- `Depth` — 递归深度

#### `type TaskSnapshot struct`

字段:
- `ID` — 任务 ID
- `Name` / `Color` — 任务展示名与颜色
- `SessionID` / `ParentSessionID`
- `Description` / `Prompt` / `SystemPrompt`
- `ContextMode` / `RunMode`
- `Status` — running / completed / failed / stopped
- `TranscriptPath` / `OutputPath`
- `PID` / `ExitCode`
- `Depth` / `ParentTaskID`
- `StartedAt` / `FinishedAt`
- `Content` / `Error`
- `UsedTokens` / `Usage`

