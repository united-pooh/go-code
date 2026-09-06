import { useEffect, useRef, useState } from 'react';
import { fetchJSON, validRequest } from './api';
import { RequestDetail } from './RequestDetail';
import { requestKey } from './projections';
import type { GlobalRequest, RequestRow } from './types';

export function RequestInspector({
  target,
  projectName,
  onClose,
}: {
  target: RequestRow;
  projectName: string;
  onClose: () => void;
}) {
  const [result, setResult] = useState<{ key: string; request: GlobalRequest } | null>(null);
  const [error, setError] = useState(false);
  const [retry, setRetry] = useState(0);
  const close = useRef<HTMLButtonElement>(null);
  const key = requestKey(target);
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    close.current?.focus();
    return () => previous?.focus();
  }, [key]);
  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const params = new URLSearchParams({ instance_id: target.instance_id, request_id: target.id });
    const refresh = async () => {
      try {
        const request = await fetchJSON(
          `/api/global/request?${params}`,
          controller.signal,
          validRequest,
        );
        if (request.id !== target.id || request.instance_id !== target.instance_id)
          throw new Error('request identity mismatch');
        if (!controller.signal.aborted) {
          setResult({ key, request });
          setError(false);
        }
      } catch {
        if (!controller.signal.aborted) setError(true);
      } finally {
        if (!controller.signal.aborted) timer = setTimeout(() => void refresh(), 2000);
      }
    };
    setError(false);
    void refresh();
    return () => {
      controller.abort();
      if (timer) clearTimeout(timer);
    };
  }, [key, target.id, target.instance_id, retry]);
  const request = result?.key === key ? result.request : null;
  if (request)
    return (
      <RequestDetail
        request={request}
        projectName={projectName}
        onClose={onClose}
        error={error}
        onRetry={() => setRetry((value) => value + 1)}
      />
    );
  return (
    <aside
      className="global-detail"
      aria-label="请求明细"
      aria-busy={!error}
      onKeyDown={(event) => {
        if (event.key === 'Escape') onClose();
      }}
    >
      <header>
        <h2>请求明细</h2>
        <button ref={close} type="button" onClick={onClose} aria-label="关闭请求明细">
          ×
        </button>
      </header>
      <div className="detail-scroll">
        <p role={error ? 'alert' : 'status'}>
          {error ? '请求明细读取失败，记录可能不再可用。' : '正在读取此请求的原子明细…'}
        </p>
        {error && (
          <button type="button" onClick={() => setRetry((value) => value + 1)}>
            重试明细
          </button>
        )}
      </div>
    </aside>
  );
}
