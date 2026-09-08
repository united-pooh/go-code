# `internal/runtime/streamma`

目录: [internal/runtime/streamma](../../internal/runtime/streamma)

职责:
- 提供内存版 StreamMA multi-agent runtime
- 用 fake model 验证 `Exact+END_STEP` step 契约、增量 step fanout、append-only 上下文、DAG 调度、EOF/drain、fail-fast 和 replay

边界:
- 不接入真实模型 provider、NATS、Postgres 或 MinIO
- 不执行 stream 中出现的工具动作
- 不改变 `loop.Engine` 的单 agent 主链路

#### 核心类型

##### `type GraphSpec struct`

字段:
- `RunID`
- `Protocol`
- `StepPolicy`
- `Agents`
- `Edges`

职责:
- 描述 Chain、Tree、Graph DAG 拓扑
- source 节点接收 problem
- 每个 step 边界闭合后立即广播给 direct successors，不等待上游模型流整体结束

##### `type AgentSpec struct`

字段:
- `ID`
- `Role`
- `SystemPrompt`

职责:
- 描述单个 StreamMA agent 的稳定系统提示

##### `type EdgeSpec struct`

字段:
- `From`
- `To`

职责:
- 描述 DAG 中一条 direct successor 关系

##### `type StepPolicy struct`

字段:
- `Boundary`
- `MaxStepBytes`
- `RequireBoundary`

当前默认:
- `Boundary` 为空时使用 `END_STEP`
- step contract 是 `Exact+END_STEP`
- `RequireBoundary=true` 时，缺失最终 sentinel 的内容会触发 parser fatal，不产生 recovered step，也不会传播给下游 agent

##### `type AgentStreamer interface`

方法:
- `StreamAgent(ctx, invocation) (<-chan model.StreamEvent, error)`

职责:
- 接收结构化 `AgentInvocation`，包含 `run_id`、`agent_id`、role、system prompt、problem、invocation index、input event、新 inbound step 和 transcript snapshot
- 真实 `/streamma` 路径使用该接口把同一 logical agent 绑定到同一个 subagent session，并按论文模型增量 append 新 step

##### `type ModelStreamer interface`

方法:
- `StreamMessage(ctx, messages, tools) (<-chan model.StreamEvent, error)`

用途:
- 作为内存 runtime / fake model / 兼容测试的 adapter 输入
- adapter 会从 `Transcript` 构造完整 prompt，并以 `tools = nil` 调用模型；stream 中的 tool event 默认不会触发工具执行

##### `type StepPacket struct`

职责:
- Runtime 内部的结构化 step 包
- `Content.Text` 保存模型自然语言响应中的 step 原文内容，只移除独占一行的 sentinel 行

边界:
- LLM 原生输出不是 JSON 或结构体
- `StepPacket` 是系统包装层

##### `type FinalAnswerPacket struct`

职责:
- sink agent drain 后产生最终回答
- 记录 `Answer.Text` 和 `Support.UsedSteps`

##### `type RunResult struct`

字段:
- `RunID`
- `Status`
- `Final`
- `Events`
- `Error`

职责:
- 返回完成或失败后的可审计结果

#### Parser

文件: [parser.go](../../internal/runtime/streamma/parser.go)

##### `ParseStream(ctx, events, config) ([]StepPacket, error)`

行为:
- 只把单独成行且精确等于 `END_STEP` 的行作为 step 边界
- 保留 step 原文内容，包括前后空白和普通换行
- 代码块内、普通句子内、带额外空白的 `END_STEP` 不触发边界
- `RequireBoundary=false` 且缺失最终 sentinel 但仍有内容时才 forced close，并设置 `BoundaryRecovered`
- 超过 `MaxStepBytes` 时返回 parser fatal error

##### `StreamSteps(ctx, events, config, emit) error`

职责:
- 增量读取模型 stream
- 每识别到一个完整 step 就立即调用 `emit(StepPacket)`
- 不等待 `Done` 才返回所有 step

用途:
- Runtime 用它在上游 agent 仍在生成时，把已闭合 step 立刻投递给下游 agent

#### Context 与 Prompt

文件:
- [context.go](../../internal/runtime/streamma/context.go)
- [prompt.go](../../internal/runtime/streamma/prompt.go)

##### `type Transcript struct`

职责:
- 每个 agent 保存 append-only transcript
- inbound step 与 own step 按实际处理顺序追加
- 在真实 `/streamma` 路径中作为 runtime 审计投影；真实 `ctx_a` 落在每个 logical agent 的 subagent session history 中

##### `BuildPrompt(transcript) []message.Message`

职责:
- 从 transcript 构造模型输入
- 固定 system prompt 和 original problem
- 不把 event id、timestamp、trace、seq 等动态 metadata 写进 prompt
- 只用于 fake model、内存 runtime 兼容和审计验收；真实 subagent 路径不会反复发送完整 `base + transcript`

##### `BuildPromptSegments(transcript) []PromptSegment`

职责:
- 暴露 cache-stable prompt segment 投影
- 供 prefix cache 相关验收和后续适配器复用

#### Runtime

文件: [runtime.go](../../internal/runtime/streamma/runtime.go)

##### `NewRuntime(config) (*Runtime, error)`

职责:
- 编译 DAG
- 注入 `AgentStreamer`；旧 `ModelStreamer` 会通过兼容 adapter 包装为 `AgentStreamer`
- 初始化 broker、event log 和 agent runtime state

##### `RunGraph(ctx, spec, model, problem) (RunResult, error)`

行为:
- Chain、Tree、Graph 都由同一 DAG runtime 执行
- Runtime 边读模型 stream 边提交 `StepPacket`，step 一闭合就 `FanoutStep`
- 下游 agent 可在上游 agent 尚未 `Done` 时启动自己的模型调用
- 多前驱节点是 arrival-triggered，任意前驱 step 到达即可调用，不等待同步 barrier
- 单个 agent 内部仍按队列顺序串行处理 inbound delivery，避免同一 transcript 被多个 invocation 同时改写
- 真实 subagent 路径按 `run_id + agent_id` 维护 session pool：同一 logical agent 多次 invocation 复用同一个 sessionID，不同 agent 和不同 run 不共享 session
- 首次 subagent invocation 写入 stable system prompt、original problem 和当前输入；后续 invocation 只写入新增 inbound step，避免重复写入旧 transcript
- `/streamma-trace` 的 `subagent.started` / `subagent.finished` 会显示 invocation index、sessionID 和 provider 返回的 usage/cache 字段；cache 命中只来自 provider usage，不做本地估算
- 非 source agent 只有在所有 predecessors EOF、队列 drain、无 active invocation 后结束
- critical agent 调用失败、parser fatal、EOF 不一致等错误会 fail-fast

#### Event Log 与 Replay

文件:
- [event_log.go](../../internal/runtime/streamma/event_log.go)
- [replay.go](../../internal/runtime/streamma/replay.go)

##### `type EventLog struct`

职责:
- 记录 problem、step committed、upstream EOF、final answer、run failed 等事件
- 为事件补全 `Seq`、`EventID`、`Timestamp`

##### `Replay(events) ReplaySummary`

职责:
- 不调用模型，仅根据已提交事件重建 step 序列、final/failed summary、failure point
- 根据 step dependencies 重建每个 agent 的 transcript 投影和 lifecycle 状态

用途:
- 将失败 run 重放到同一失败点
- 给验收测试和后续生产适配器提供可审计日志基础

