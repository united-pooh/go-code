# `internal/tokentracer`

目录: [tokentracer](../../internal/tokentracer)

职责:
- 记录普通对话、工具调用、StreamMA runtime events、subagent usage/cache 的 token 用量
- 提供本地 HTTP dashboard（`/` 实时页面、`/api/state` 快照、`/events` SSE）

核心类型:
- `Tracer` — 内存聚合器，按 pipeline → stage → agent 组织
- `Snapshot` — 可审计的完整快照（含 timeline 与事件流）
- `Timeline` / `Event` — 时间线视图与事件记录
