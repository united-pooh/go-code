import { lazy, Suspense, useEffect, useState } from 'react';
import { GlobalDashboard } from './Dashboard';
import { fetchJSON, initialQuery, queryURL, validPage } from './api';
import type { GlobalPage, GlobalQuery } from './types';

const DebugApp = lazy(() => import('../app/App').then((module) => ({ default: module.App })));

export function GlobalApp() {
  const [result, setResult] = useState<{ data: GlobalPage; url: string } | null>(null);
  const [query, setQuery] = useState<GlobalQuery>(initialQuery);
  const [error, setError] = useState(false);
  const [retry, setRetry] = useState(0);
  const [debug, setDebug] = useState(
    () => new URLSearchParams(location.search).get('view') === 'debug',
  );
  const url = queryURL(query);
  useEffect(() => {
    if (debug) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const refresh = async () => {
      try {
        const next = await fetchJSON(url, controller.signal, validPage);
        if (!controller.signal.aborted) {
          setResult({ data: next, url });
          setError(false);
        }
      } catch {
        if (!controller.signal.aborted) setError(true);
      } finally {
        if (!controller.signal.aborted) timer = setTimeout(() => void refresh(), 2000);
      }
    };
    void refresh();
    return () => {
      controller.abort();
      if (timer) clearTimeout(timer);
    };
  }, [debug, retry, url]);
  const changeView = (next: boolean) => {
    history.replaceState(null, '', next ? '?view=debug' : location.pathname);
    setDebug(next);
  };
  if (debug)
    return (
      <div className="debug-mode">
        <div className="debug-return">
          <button type="button" onClick={() => changeView(false)}>
            ← 全局消耗概览
          </button>
          <span>当前实例 · Dockview 调试工作台</span>
        </div>
        <div className="debug-content">
          <Suspense fallback={<p>正在加载调试工作台…</p>}>
            <DebugApp />
          </Suspense>
        </div>
      </div>
    );
  if (result)
    return (
      <GlobalDashboard
        data={result.data}
        query={query}
        onQueryChange={setQuery}
        pending={result.url !== url}
        connection={error ? 'error' : 'live'}
        onRetry={() => setRetry((value) => value + 1)}
        onDebug={() => changeView(true)}
      />
    );
  return (
    <div className="global-loading">
      <header>
        <span className="paw-mark">p.</span> Paw Token Tracer
      </header>
      <main>
        <h1>{error ? '无法连接全局消耗记录' : '正在读取本地消耗记录'}</h1>
        <p>
          {error
            ? '请确认服务仍在运行。历史记录保留在 Paw 全局存储中。'
            : '跨项目、跨会话，统一查看已报告的 token。'}
        </p>
        {error && (
          <button type="button" onClick={() => setRetry((value) => value + 1)}>
            重新连接
          </button>
        )}
      </main>
    </div>
  );
}
