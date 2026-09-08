# `internal/capability/tool`

#### 抽象层

文件: [tool.go](../../internal/capability/tool/tool.go)

##### `type Tool interface`

当前工具抽象。

方法:
- `Name() string`
- `Description() string`
- `Run(ctx context.Context, input json.RawMessage) (string, error)`
- `InputSchema() json.RawMessage`

扩展规则:
- 新工具只要实现这个接口，并注册到 `Registry`，不需要改 loop 核心逻辑

##### `type ConcurrencySafeTool interface`

方法:
- `IsConcurrencySafe(input json.RawMessage) bool`

用途:
- 声明某次调用是否可并发执行；runner 会把连续的安全工具调用并行批处理，非安全调用仍串行

##### `type FileMutationTool interface`

方法:
- `FileMutationTarget(input json.RawMessage) (FileMutationTarget, error)`

用途:
- runner 在不执行工具的前提下安全检查目标文件（路径 + 写入前是否存在）
- 当前由 `WriteTool` 和 `EditTool` 实现，用于向 UI 提供真实修改差异

##### `type FileMutationTarget struct`

字段:
- `Path` — 解析后的工作区绝对路径
- `BeforeExists` — 写入前目标是否存在

#### 注册层

文件: [register.go](../../internal/capability/tool/register.go)

##### `type Registry struct`

职责:
- 按名称保存工具实例
- 支持 namespace 级原子替换（MCP 动态工具）

##### `NewRegistry() *Registry`

职责:
- 创建注册表

##### `Register(tool Tool)`

职责:
- 注册工具

##### `Get(name string) (Tool, bool)`

职责:
- 按名称查找工具

##### `ReplaceNamespace(namespace string, tools []Tool) error`

职责:
- 原子替换属于某逻辑 namespace 的全部工具
- 不与其它 namespace 的工具发生覆盖冲突

用途:
- MCP 能力快照变化时更新注册表

##### `RemoveNamespace(namespace string)`

职责:
- 移除某逻辑 namespace 的全部工具

##### `Describe() []string`

职责:
- 生成工具说明文本
- 当前也会附带 `input_schema`

用途:
- 给 `Runner` 拼 system prompt

##### `DescribeBrief() []string`

职责:
- 生成不带 schema 的工具说明文本

##### `Definitions() []model.ToolDefinition`

职责:
- 返回 `[]model.ToolDefinition`，用于 LLM API 的原生工具调用请求

##### `IsConcurrencySafe(name string, input []byte) bool`

职责:
- 查询某次调用是否可并发执行

#### 文件工具

文件:
- [path.go](../../internal/capability/tool/file/path.go)
- [ls.go](../../internal/capability/tool/file/ls.go)
- [read.go](../../internal/capability/tool/file/read.go)
- [read_state.go](../../internal/capability/tool/file/read_state.go)
- [write.go](../../internal/capability/tool/file/write.go)
- [edit.go](../../internal/capability/tool/file/edit.go)
- [atomic.go](../../internal/capability/tool/file/atomic.go)
- [mutation_path.go](../../internal/capability/tool/file/mutation_path.go)
- [grep.go](../../internal/capability/tool/file/grep.go)
- [glob.go](../../internal/capability/tool/file/glob.go)

##### `type LSTool struct`

字段:
- `Root`
- `ReadRoots`

方法:
- `Name()`
- `Description()`
- `InputSchema()`
- `Run(ctx, input)`

职责:
- 列出某个目录下的文件名

输入格式:

```json
{
  "path": "."
}
```

##### `type ReadTool struct`

字段:
- `Root`
- `ReadRoots`
- `ReadState` (`*ReadStateStore`)

方法:
- `Name()`
- `Description()`
- `InputSchema()`
- `IsConcurrencySafe(input) bool`
- `Run(ctx, input)`

职责:
- 读取工作区内文件内容
- 读取成功后把内容哈希记录到 `ReadStateStore`，作为后续 Edit/Write 的基线

输入格式:

```json
{
  "file_path": "go.mod"
}
```

##### `type WriteTool struct`

字段:
- `Root`
- `ReadState` (`*ReadStateStore`)

方法:
- `Name()`
- `Description()`
- `InputSchema()`
- `FileMutationTarget(input) (FileMutationTarget, error)`
- `Run(ctx, input)`

职责:
- 覆盖写入工作区内文件（`atomicWriteFile` 原子写入）
- 若模型此前 Read 过该文件，写入前校验磁盘内容仍匹配记录基线，防止丢失更新

输入格式:

```json
{
  "file_path": "notes.txt",
  "content": "hello"
}
```

##### `type EditTool struct`

字段:
- `Root`
- `ReadState` (`*ReadStateStore`)

方法:
- `Name()`
- `Description()`
- `InputSchema()`
- `FileMutationTarget(input) (FileMutationTarget, error)`
- `Run(ctx, input)`

职责:
- 对工作区内已先用 Read 读取的文件做精确字符串替换（对齐 Claude Code 的 Edit 契约）
- 目标必须是常规文件、必须先被 Read 记录基线、`old_string` 必须逐字节匹配且默认唯一
- 写入使用 `atomicWriteFile` 原子替换；成功后更新 ReadState 基线

输入格式:

```json
{
  "file_path": "internal/foo.go",
  "old_string": "return 1",
  "new_string": "return 2",
  "replace_all": false
}
```

行为:
- `old_string` 未命中 → 报错并提示必须与文件内容精确匹配
- 命中多处且未设 `replace_all` → 报错并提示补充上下文或设置 `replace_all=true`
- 自上次 Read 后文件被外部修改 → 报错并要求重新 Read

##### `type ReadStateStore struct`

职责:
- 按路径记录最近一次 Read 的内容哈希（sha256）
- 为 Edit/Write 提供 stale-write / lost-update 保护

方法:
- `Record(path, content)` — 记录基线
- `Verify(path, current) error` — 有基线时校验当前内容仍匹配；无基线时宽松返回 nil
- `VerifyRequired(path, current) error` — 必须有基线且匹配（Edit 使用）
- `RecordAfterWrite(path, content)` — 写入后刷新基线，避免连续 Edit 误报

##### `atomicWriteFile(target, content, mode) error`

职责:
- 同目录写临时文件后 rename，崩溃不会留下半写文件
- 自动创建父目录并显式应用权限位

##### `resolveMutationPath(root, target, allowMissing) (string, bool, error)`

职责:
- 解析工作区内路径
- 阻止路径经符号链接逃出工作区根目录
- `allowMissing=true` 时解析最近存在的祖先，校验缺失后缀不越界

##### `resolvePathWithinRoot(root, target) (string, error)`

职责:
- 解析文件路径
- 阻止路径逃出工作区根目录

##### `type GrepTool struct`

字段:
- `Root`
- `ReadRoots`

方法:
- `Name()`
- `Description()`
- `InputSchema()`
- `Run(ctx, input)`

职责:
- 在工作区内按内容搜索文本

输入格式:

```json
{
  "pattern": "RunTurn",
  "path": "internal",
  "literal": true,
  "max_results": 20
}
```

##### `type GlobTool struct`

字段:
- `Root`
- `ReadRoots`

方法:
- `Name()`
- `Description()`
- `InputSchema()`
- `Run(ctx, input)`

职责:
- 在工作区内按 glob 模式匹配路径

输入格式:

```json
{
  "pattern": "**/*.go",
  "path": "internal",
  "max_results": 50
}
```

#### 命令执行工具

文件: [bash.go](../../internal/capability/tool/exec/bash.go)

##### `type BashTool struct`

字段:
- `Root`

职责:
- 在工作区内执行 shell 命令

输入格式:

```json
{
  "command": "go test ./...",
  "cwd": ".",
  "timeout_seconds": 30
}
```

##### `Run(ctx, input) (string, error)`

职责:
- 解码输入
- 校验工作目录
- 设置超时
- 执行子进程
- 合并 stdout/stderr
- 限制输出长度

##### 关键内部函数

###### `decodeBashInput(input) (bashInput, error)`

职责:
- 校验 `command`

###### `resolveTimeout(timeoutSeconds) time.Duration`

职责:
- 解析超时

###### `resolveWorkingDir(root, cwd) (string, error)`

职责:
- 解析并校验工作目录不越界

###### `type limitedBuffer struct`

职责:
- 截断工具输出，避免结果无限增长

#### 网络工具

文件: [webfetch.go](../../internal/capability/tool/webfetch/webfetch.go)

##### `type Tool struct`

字段:
- `Client`

职责:
- 发起 HTTP(S) GET 请求
- 返回状态行、响应头中的 `Content-Type` 和响应体文本
- 响应体截断到 32 KiB，超出时追加 `[response truncated]`

输入格式:

```json
{
  "url": "https://example.com",
  "timeout_seconds": 30
}
```

#### 选择工具（交互）

目录: [select](../../internal/capability/tool/select/)

职责:
- 在主 TUI 中渲染阻塞式单选/多选 prompt，等待用户提交或取消
- 通过 `Broker` 把工具调用桥接到 Bubble Tea UI

##### `type Broker struct`

职责:
- 维护请求队列与活动请求
- `Ask(ctx, request)` 阻塞等待用户结果
- `NextEvent(ctx)` / `Complete(id, result)` / `Close()` 供 UI 侧消费与回填

##### `type Request struct`

字段:
- `ID` — 由 broker 分配的请求 ID（`select-<n>`）
- `Prompt`
- `Mode` — `single` / `multiple`
- `Options` — `[]Option{ID, Label, Description}`
- `InitialSelectedIDs`
- `MinSelect` / `MaxSelect`

##### `type Result struct`

字段:
- `Cancelled`
- `SelectedOptions` — `[]SelectedOption{ID, Label}`

行为:
- 单选模式必须恰好选中一个选项；多选模式校验 `min_select` / `max_select`
- 支持一个 `custom_option` 自定义选项（`id` 为保留值 `custom_option`，需提供 label）
- 结果会与请求的 canonical 选项做一致性校验，非法结果返回错误

输入格式:

```json
{
  "prompt": "Pick one",
  "mode": "single",
  "options": [
    {"id": "a", "label": "Option A"},
    {"id": "b", "label": "Option B"}
  ]
}
```

#### MCP 工具适配

文件: [mcp/tool.go](../../internal/capability/tool/mcp/tool.go)

##### `type Tool struct`

字段:
- `spec` (`coremcp.ToolSpec`)
- `broker` (`coremcp.Broker`)

职责:
- 把一个 MCP 能力适配为 Paw 的普通工具接口
- broker 可以是主进程 Manager，也可以是 subagent 转发代理

方法:
- `Name()` / `Description()` / `InputSchema()` / `Namespace()` / `Run(ctx, input)` / `Spec()`

