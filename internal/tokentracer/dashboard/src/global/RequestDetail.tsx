import { useEffect, useRef } from 'react';
import { formatCount, formatDuration } from '../trace/format';
import { summarizeRequests } from './projections';
import type { GlobalRequest } from './types';

export const atomLabels: Record<string, string> = {
  system_prompt: '系统提示词',
  user_prompt: '输入提示词',
  assistant_history: '历史回答',
  tool_definition: '工具定义',
  tool_arguments: '工具参数',
  tool_result: '工具结果',
  reasoning_history: '历史推理',
  multimodal: '多模态内容',
  provider_other: '其他内容',
  request_envelope: '请求封装',
};
export const statusLabels: Record<string, string> = {
  running: '进行中',
  completed: '已完成',
  failed: '失败',
  canceled: '已取消',
  incomplete: '未完成',
  interrupted: '已中断',
  started: '已发送',
  headers: '已响应',
  network_error: '网络错误',
};

export function RequestDetail({
  request,
  projectName,
  onClose,
  error,
  onRetry,
}: {
  request: GlobalRequest;
  projectName: string;
  onClose: () => void;
  error?: boolean;
  onRetry?: () => void;
}) {
  const close = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    close.current?.focus();
    return () => previous?.focus();
  }, [request.id]);
  const summary = summarizeRequests([request]);
  return (
    <aside
      className="global-detail"
      aria-label="请求明细"
      onKeyDown={(event) => {
        if (event.key === 'Escape') onClose();
      }}
    >
      <header>
        <div>
          <span className="eyebrow">REQUEST INSPECTOR</span>
          <h2>请求明细</h2>
        </div>
        <button ref={close} type="button" onClick={onClose} aria-label="关闭请求明细">
          ×
        </button>
      </header>
      <div className="detail-scroll">
        {error && (
          <p role="alert" className="coverage-notice">
            明细更新失败，保留最后记录。
            <button type="button" onClick={onRetry}>
              重试明细
            </button>
          </p>
        )}
        <p className="detail-project">{projectName}</p>
        <h3>{request.model || '模型未报告'}</h3>
        <div className="detail-meta">
          <span className={`status-label ${request.status}`}>
            {statusLabels[request.status] ?? request.status}
          </span>
          <span>
            {formatDuration(Date.parse(request.updated_at) - Date.parse(request.started_at))}
          </span>
        </div>
        <div className="detail-total">
          <b>{summary.reported ? formatCount(summary.total) : '—'}</b>
          <span>已报告 token</span>
        </div>
        {request.attempts.some(
          (attempt, index) =>
            attempt.provider_response_id &&
            request.attempts
              .slice(0, index)
              .some(
                (previous) =>
                  previous.provider_response_id === attempt.provider_response_id &&
                  previous.transport === attempt.transport,
              ),
        ) && (
          <p className="coverage-notice">
            同一响应的累计 usage 已在本请求内合并；下方保留每次发送的原始报告，不应再次相加。
          </p>
        )}
        <dl className="detail-fields">
          <div>
            <dt>会话</dt>
            <dd>{request.scope.session_id ?? '未报告'}</dd>
          </div>
          <div>
            <dt>请求</dt>
            <dd>{request.id}</dd>
          </div>
          <div>
            <dt>用途</dt>
            <dd>{request.scope.purpose ?? '未报告'}</dd>
          </div>
          <div>
            <dt>实例</dt>
            <dd>{request.instance_id}</dd>
          </div>
          {request.scope.task_id && (
            <div>
              <dt>子任务</dt>
              <dd>{request.scope.task_id}</dd>
            </div>
          )}
        </dl>
        {summary.unknown > 0 && (
          <p className="coverage-notice">
            {summary.unknown} 次发送缺少完整 usage，未报告不代表零消耗。
          </p>
        )}
        {request.attempts.map((attempt) => (
          <section className="attempt-section" key={attempt.number}>
            <div className="section-title">
              <h3>发送 #{attempt.number}</h3>
              <span>
                {attempt.http_status
                  ? `HTTP ${attempt.http_status}`
                  : (statusLabels[attempt.status] ?? attempt.status)}
              </span>
            </div>
            <p className="minor">
              {attempt.transport} · 供应商报告 / 按供应商定义 ·{' '}
              {attempt.usage_finality === 'final'
                ? '已确认最终 usage'
                : attempt.usage_finality === 'partial'
                  ? '部分报告，最终量未确认'
                  : '最终性未知（旧记录或未报告）'}
            </p>
            {attempt.provider_response_id && (
              <p className="minor">响应 ID：{attempt.provider_response_id}</p>
            )}
            <div className="attempt-usage">
              {(['input', 'output', 'cache_read', 'cache_creation', 'reasoning'] as const).map(
                (key) => (
                  <div key={key}>
                    <span>
                      {
                        {
                          input: '输入（含缓存）',
                          output: '输出',
                          cache_read: '缓存读取',
                          cache_creation: '缓存创建',
                          reasoning: '推理（输出子集）',
                        }[key]
                      }
                    </span>
                    <b>
                      {attempt.known[key] && attempt.tokens
                        ? formatCount(
                            key === 'input'
                              ? attempt.tokens.input +
                                  attempt.tokens.cache_read +
                                  attempt.tokens.cache_creation
                              : attempt.tokens[key],
                          )
                        : '—'}
                    </b>
                  </div>
                ),
              )}
            </div>
            <div className="section-title">
              <h3>输入原子分解</h3>
              <span className="estimate-tag">估算</span>
            </div>
            <p className="minor">
              组件按实际出站内容估算，不是供应商逐项账单。工具结果在后续请求中重发，会再次计入。
            </p>
            {attempt.atoms_state !== 'complete' && (
              <p className="coverage-notice">
                组件覆盖：{attempt.atoms_state ?? '未采集'}。不支持的内容保持未知。
              </p>
            )}
            <div className="atom-list">
              {(attempt.atoms ?? []).map((atom) => (
                <div
                  className="atom-row"
                  key={atom.path}
                  title={`${atom.path} · ${atom.estimator ?? '无 token 估算'}`}
                >
                  <div>
                    <b>{atomLabels[atom.category] ?? atom.category}</b>
                    {atom.tool_name && <span>{atom.tool_name}</span>}
                    <small>{formatCount(atom.wire_bytes)} B · 出站 JSON</small>
                  </div>
                  <strong>
                    {atom.estimated_tokens === undefined
                      ? '—'
                      : `≈${formatCount(atom.estimated_tokens)}`}
                  </strong>
                </div>
              ))}
            </div>
            <p className="minor">
              mixed_text_v1：ASCII 每 4 字符、非 ASCII 每字符估 1
              token。包含近似误差，不估计缓存或逐项费用。
            </p>
          </section>
        ))}
      </div>
    </aside>
  );
}
