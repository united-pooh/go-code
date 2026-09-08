# `internal/capability/model`

#### 配置层

文件: [config.go](../../internal/capability/model/config.go)

持久化、Schema、凭据、模型发现与热重载统一由 `internal/platform/config` 管理；本包只保留从 `config.jsonc` 快照合成的运行时请求配置，不再读取或写入旧 `~/.paw/config.json`。完整契约见上文“配置系统 v2”。

##### `type Config struct`

模型连接参数。

字段:
- `ProfileID` / `ProfileName`
- `Provider` / `Transport`
- `APIBaseURL`
- `APIPath`
- `APIKey`
- `APIKeyEnvName`
- `Model`
- `Models`
- `ExtraBody`（`RequestBody`，合并进每个 provider 请求体的任意 JSON 对象）
- `ModelExtraBody`（`map[string]RequestBody`，按模型名附加的请求体）
- `ContextLimitTokens` / `ModelContextLimitTokens`（上下文 token 上限，可按模型覆盖）
- `Timeout`
- `RetryCount` / `RetryCountSet`（来自 v2 Provider `retries`；网络失败或遇到 408/425/429/5xx 时的重试次数，显式 `0` 有效）
- `Stream` / `StreamSet`（来自 Provider/Model/工作区分层结果；显式 `false` 有效）
- `Profiles`（全部已配置 profile）

##### `type Profile struct`

字段:
- `ID` / `Name` / `Provider` / `Transport`
- `APIBaseURL` / `APIPath` / `APIKey` / `APIKeyEnvName`
- `Model` / `Models`
- `ExtraBody` / `ModelExtraBody`
- `ContextLimitTokens` / `ModelContextLimitTokens`
- `Timeout` / `RetryCount` / `Stream` / `StreamSet`
- `CredentialID`

##### 配置来源

应用启动链只通过 `internal/platform/config.Manager` 读取全局 `~/.paw/config.jsonc`，合并同一全局目录中的 `projects/<id>/config.jsonc` 项目级活动模型与安全模型参数覆盖，再把不可变运行时快照注入 `model.Client`。设置 `PAW_CONFIG_HOME` 时，两者统一使用该根目录；不再读取工作区 `.paw/config.jsonc`。旧 `~/.paw/config.json` 不再自动迁移、读取或回写。

provider 的 `discovery.enabled` 为 `true` 时，顶层进程会在启动时执行模型发现；`/config` 中修改 provider 或切换“模型发现”后会显式刷新。普通模型切换和被动文件重载只复用已确认结果或匹配缓存。

##### `type RequestBody map[string]any`

任意 JSON 对象，通过 `extraBody` / `modelExtraBody` 合并进 provider 请求体。受保护字段（如 `model`、`system`、`messages`、`tools`、`stream` 等）不允许出现在 extra body 中；`ValidateExtraRequestBodies` 会在加载配置时校验。

#### 请求/响应类型

文件: [types.go](../../internal/capability/model/types.go)

##### `type ChatCompletionsRequest struct`

字段:
- `Model`
- `Messages`
- `Stream`

##### `type ChatCompletionsResponse struct`

字段:
- `Choices[].Message`
- `Error`

这是当前最小响应投影，不是供应商完整 schema。

#### 客户端

文件: [client.go](../../internal/capability/model/client.go)

##### `type Client struct`

职责:
- 发送 HTTP 请求
- 解析同步响应
- 解析流式响应

字段:
- `httpClient`
- `cfg`

##### `NewClient(cfg Config) *Client`

职责:
- 创建模型客户端

##### `RunMessage(ctx, messages) (string, error)`

职责:
- 发起一次非流式请求

当前状态:
- CLI 主路径不依赖它
- 仍然保留作为同步调用能力

##### `StreamMessage(ctx, messages, tools) (<-chan StreamEvent, error)`

职责:
- 发起流式请求（按 `transport` 分发到 OpenAI-compatible 或 Anthropic-compatible 流式解析）
- 返回事件 channel
- `tools` 为原生工具定义（`[]model.ToolDefinition`），用于 LLM API 的原生工具调用请求

#### 流式层

文件: [stream.go](../../internal/capability/model/stream.go)

##### `type StreamEvent struct`

字段:
- `Delta`
- `Thinking`
- `Done`
- `Err`
- `Usage`

约定:
- 一次事件只表达一种状态

##### 关键内部函数

这些函数是 `model` 包内部的流式拆分点，不是外部扩展接口:

###### `newStreamScanner(body) *bufio.Scanner`

职责:
- 创建并配置 SSE 行扫描器

###### `handleStreamLine(ctx, line, events) (done bool, err error)`

职责:
- 处理单行 SSE 文本

###### `handleStreamPayload(ctx, payload, events) (done bool, err error)`

职责:
- 处理 `data:` 后的 payload

###### `decodeStreamChunk(payload) (chatCompletionsStreamResponse, error)`

职责:
- JSON 解码

###### `emitChunkEvents(ctx, chunk, events) bool`

职责:
- 把 chunk 转成 `StreamEvent`

###### `consumeStream(ctx, resp, events)`

职责:
- 后台消费整个 SSE 响应流

#### Responses 流式防御

`responses_transport.go`、`responses_sse.go`、`responses_failure.go`、`responses_replay.go`、`responses_completion.go` 将传输编排、分帧诊断、重放和完成提交分开，保持 `StreamMessage` / `StreamEvent` 接口不变。

1. **协议与诊断**：支持多行 SSE、BOM、CR/LF/CRLF；保留帧大小上限，错误包含事件阶段、底层错误类别与 request ID，不输出原始响应体。
2. **生命周期**：空闲 watchdog 与取消共用幂等关闭路径，清理计时器和 context callback，避免旧超时回调关闭已经恢复的连接。
3. **重试预算**：HTTP 与流失败共用 `retries + 1` 次总请求预算及退避，尊重 `Retry-After`，取消立即结束；认证、协议限制等确定性错误不盲目重试。
4. **重放与提交**：传输中断后的文本/思考以严格前缀核对去重；内容不一致就显式停止，已输出内容后的 provider failed 不重放。工具、用量及完成状态仅在完成校验后一起提交；坏帧恢复只信任完整完成快照，不能用残缺参数覆盖它。

这能恢复一部分瞬断或截断，不保证供应商故障永不发生，也不会自动切换模型或降级为非流式请求。

#### Anthropic 流式层

文件: [anthropic_stream.go](../../internal/capability/model/anthropic_stream.go)

职责:
- 针对 Anthropic Messages API 的流式解析（`message_start` / `content_block_start` / `content_block_delta` / `message_delta` 等）
- 处理 thinking、text、tool_use 内容块
- 支持 system prompt 的 `cache_control`（`type: "ephemeral"`）
- 把 provider 返回的 usage 汇总到 `StreamEvent.Usage`

