# `internal/message`

文件: [types.go](../../internal/message/types.go)

#### `type Role string`

角色枚举。

当前值:
- `RoleSystem`
- `RoleUser`
- `RoleAssistant`

#### `type Message struct`

统一消息结构。

字段:
- `Role`
- `Content`
- `Parts`
- `ToolUse`
- `ToolUses`
- `ToolResult`
- `ToolResults`

用法:
- 普通文本消息: `Role + Content`
- 富文本多模态消息: `Role + Parts`
- 工具调用消息: `Role + ToolUse` / `Role + ToolUses`
- 工具结果消息: `Role + ToolResult` / `Role + ToolResults`

#### `type ContentPart` / `type ImagePart`

用途:
- 描述有序的 text/image 片段
- 图片以附件引用持久化，以内存 `Data` 物化到 provider 请求

#### `type ToolCall struct`

表示 assistant 发起的一次工具调用。

字段:
- `ID`
- `Name`
- `Input`

#### `type ToolResult struct`

表示工具执行结果。

字段:
- `ToolUseID`
- `Content`
- `IsError`

