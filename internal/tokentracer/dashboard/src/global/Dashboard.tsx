import { useEffect, useMemo, useRef, useState } from 'react';
import { formatCount } from '../trace/format';
import { requestKey } from './projections';
import { queryURL } from './api';
import { statusLabels } from './RequestDetail';
import { RequestInspector } from './RequestInspector';
import type { GlobalFilters, GlobalPage, GlobalQuery, GlobalView, RequestRow } from './types';

const views: { id: GlobalView; label: string; glyph: string }[] = [
  { id: 'overview', label: '概览', glyph: '◫' },
  { id: 'projects', label: '项目', glyph: '▱' },
  { id: 'sessions', label: '会话', glyph: '◷' },
  { id: 'models', label: '模型', glyph: '◇' },
  { id: 'tools', label: '工具', glyph: '⌘' },
  { id: 'requests', label: '请求', glyph: '↗' },
];
const titles: Record<GlobalView, string> = {
  overview: '消耗概览',
  projects: '项目消耗',
  sessions: '会话消耗',
  models: '模型消耗',
  tools: '工具原子消耗',
  requests: '请求记录',
};
function displayTime(value: string) {
  return new Date(value).toLocaleTimeString([], {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  });
}

export function GlobalDashboard({
  data,
  query,
  onQueryChange,
  pending,
  connection,
  onRetry,
  onDebug,
}: {
  data: GlobalPage;
  query: GlobalQuery;
  onQueryChange: (query: GlobalQuery) => void;
  pending: boolean;
  connection: 'live' | 'error';
  onRetry: () => void;
  onDebug?: () => void;
}) {
  const view = query.view;
  const filters = query;
  const setFilters = (update: (previous: GlobalFilters) => GlobalFilters) =>
    onQueryChange({ ...update(filters), view, offset: 0, limit: query.limit });
  const [selected, setSelected] = useState<RequestRow | null>(null);
  const [exporting, setExporting] = useState(false);
  const [exportError, setExportError] = useState(false);
  const exportController = useRef<AbortController | null>(null);
  useEffect(() => () => exportController.current?.abort(), []);
  const [theme, setTheme] = useState(() => {
    try {
      return localStorage.getItem('paw-tracer-theme') === 'dark' ? 'dark' : 'light';
    } catch {
      return 'light';
    }
  });
  useEffect(() => {
    document.documentElement.dataset.tracerTheme = theme;
    try {
      localStorage.setItem('paw-tracer-theme', theme);
    } catch {
      /* Preference storage can be unavailable. */
    }
  }, [theme]);
  const requests = data.requests;
  const summary = data.summary;
  const projectsByID = useMemo(
    () => new Map(data.instances.map((instance) => [instance.project_id, instance])),
    [data.instances],
  );
  const projectName = (id: string) => projectsByID.get(id)?.project_name ?? id;
  const trend = data.trend;
  const highest = Math.max(1, ...trend.map((bin) => bin.total));
  const title = filters.project ? projectName(filters.project) : titles[view];
  const navigate = (next: GlobalView) => {
    onQueryChange({
      view: next,
      project: '',
      period: filters.period,
      search: '',
      offset: 0,
      limit: query.limit,
    });
    setSelected(null);
  };
  const inspectGroup = (dimension: 'project_id' | 'session' | 'model' | 'tool', id: string) => {
    onQueryChange({
      ...query,
      [dimension === 'project_id' ? 'project' : dimension]: id,
      view: 'requests',
      offset: 0,
    });
    setSelected(null);
  };
  const exportData = async () => {
    const controller = new AbortController();
    exportController.current = controller;
    setExporting(true);
    setExportError(false);
    try {
      const response = await fetch(queryURL(query, '/api/global/export'), {
        cache: 'no-store',
        signal: controller.signal,
      });
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      const blob = await response.blob();
      if (controller.signal.aborted) return;
      const url = URL.createObjectURL(blob);
      const link = document.createElement('a');
      link.href = url;
      link.download = 'paw-token-tracer.json';
      link.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch {
      if (!controller.signal.aborted) setExportError(true);
    } finally {
      if (!controller.signal.aborted) setExporting(false);
    }
  };

  const requestRows = (items: RequestRow[]) => (
    <div className="request-list">
      <div className="table-heading">
        <span>请求 / 模型</span>
        <span>状态</span>
        <span>已报告 token</span>
      </div>
      {items.map((request) => (
        <button
          type="button"
          className={`request-row ${selected && requestKey(request) === requestKey(selected) ? 'selected' : ''}`}
          aria-label={`查看请求 ${request.id}`}
          key={requestKey(request)}
          onClick={() => setSelected(request)}
        >
          <div>
            <b>{request.model || '模型未报告'}</b>
            <small>
              {projectName(request.project_id)} · {displayTime(request.started_at)} ·{' '}
              {request.purpose || 'request'}
            </small>
          </div>
          <span className={`status-label ${request.status}`}>
            {statusLabels[request.status] ?? request.status}
          </span>
          <strong>
            {request.summary.reported > 0 ? formatCount(request.summary.total) : '—'}
            <small>{request.summary.attempts} 次发送</small>
          </strong>
        </button>
      ))}
    </div>
  );
  const groupedRows = (dimension: 'project_id' | 'session' | 'model') => {
    const groups = data.groups;
    const max = Math.max(1, ...groups.map((row) => row.summary.total));
    return (
      <div className="group-list">
        <div className="table-heading">
          <span>
            {dimension === 'project_id'
              ? '项目 / 工作区'
              : dimension === 'session'
                ? '会话'
                : '模型'}
          </span>
          <span>请求</span>
          <span>已报告 token</span>
        </div>
        {groups.map((row) => (
          <button
            type="button"
            key={row.id}
            className="group-row"
            onClick={() => inspectGroup(dimension, row.id)}
          >
            <div className="group-name">
              <span className="project-mark">
                {(dimension === 'project_id' ? projectName(row.id) : row.label)
                  .slice(0, 1)
                  .toUpperCase()}
              </span>
              <div>
                <b>{dimension === 'project_id' ? projectName(row.id) : row.label || '未报告'}</b>
                <small>
                  {dimension === 'project_id'
                    ? projectsByID.get(row.id)?.workspace
                    : dimension === 'model'
                      ? `${row.summary.attempts} 次发送`
                      : projectName(row.project_id ?? '')}
                </small>
              </div>
            </div>
            <span>{formatCount(row.summary.requests)}</span>
            <div className="group-amount">
              <strong>{formatCount(row.summary.total)}</strong>
              <span className="mini-bar">
                <i style={{ width: `${(row.summary.total / max) * 100}%` }} />
              </span>
            </div>
          </button>
        ))}
      </div>
    );
  };

  return (
    <div className={`global-shell ${selected ? 'has-detail' : ''}`}>
      <nav className="global-nav" aria-label="Token Tracer 导航">
        <div className="global-brand">
          <span className="paw-mark">p.</span>
          <div>
            <b>Paw</b>
            <span>Token Tracer</span>
          </div>
        </div>
        <span className="nav-caption">WORKSPACE INTELLIGENCE</span>
        <div className="nav-items">
          {views.map((item) => (
            <button
              type="button"
              key={item.id}
              className={view === item.id ? 'active' : ''}
              aria-current={view === item.id ? 'page' : undefined}
              onClick={() => navigate(item.id)}
            >
              <span aria-hidden="true">{item.glyph}</span>
              {item.label}
            </button>
          ))}
        </div>
        <div className="nav-bottom">
          <span className="local-indicator" />
          仅本机 · 本地存储<small>正文不写入遥测记录</small>
          {data.debug_available && onDebug && (
            <button type="button" onClick={onDebug}>
              打开 Dockview 调试 ↗
            </button>
          )}
        </div>
      </nav>
      <div className="global-workspace">
        <header className="global-topbar">
          <div>
            <span>全部工作区</span>
            <span className="breadcrumb-divider">/</span>
            <b>{views.find((item) => item.id === view)?.label}</b>
          </div>
          <div className="topbar-actions">
            <span className={`live-badge ${connection}`}>
              <i />
              {connection === 'live' ? '实时更新' : '连接中断'}
            </span>
            <button type="button" onClick={() => setTheme(theme === 'light' ? 'dark' : 'light')}>
              {theme === 'light' ? '深色' : '浅色'}
            </button>
          </div>
        </header>
        <main className="global-main" aria-label="全局消耗看板" aria-busy={pending}>
          <div className="page-heading">
            <div>
              <span className="eyebrow">TOKEN INTELLIGENCE</span>
              <h1>{title}</h1>
              <p>
                {view === 'tools'
                  ? '工具相关内容的发送估算，不等同于工具执行费用。'
                  : '所有 Paw 项目，一份持续更新的消耗记录。'}
              </p>
            </div>
            <div className="page-actions">
              <label className="period-control">
                <span className="sr-only">统计期间</span>
                <select
                  value={filters.period}
                  onChange={(event) =>
                    setFilters((previous) => ({
                      ...previous,
                      period: event.target.value as GlobalFilters['period'],
                    }))
                  }
                >
                  <option value="24h">最近 24 小时</option>
                  <option value="7d">最近 7 天</option>
                  <option value="30d">最近 30 天</option>
                  <option value="all">全部历史</option>
                </select>
              </label>
              <button
                type="button"
                disabled={exporting || pending}
                onClick={() => void exportData()}
              >
                {exporting ? '正在导出…' : '导出 ↗'}
              </button>
            </div>
          </div>
          {exportError && (
            <div role="alert" className="coverage-notice">
              导出失败。请确认服务连接后重试导出。
            </div>
          )}
          {pending && (
            <div role="status" className="coverage-notice">
              正在更新筛选；汇总暂时保留上次结果。
            </div>
          )}
          {connection === 'error' && (
            <div role="alert" className="coverage-notice">
              连接中断，保留最后一次快照。
              <button type="button" onClick={onRetry}>
                重试
              </button>
            </div>
          )}
          <section className="global-summary" aria-label="消耗汇总">
            <div className="total-anchor">
              <span className="eyebrow">已报告的 TOKEN</span>
              <div>
                <strong>{formatCount(summary.total)}</strong>
                <span>tokens</span>
              </div>
              <p>
                <span>费用 —</span>未配置价格，不按比例虚构账单
              </p>
            </div>
            <div className="summary-side">
              <div>
                <b>{data.project_count}</b>
                <span>项目</span>
              </div>
              <div>
                <b>{data.active_instances}</b>
                <span title="最近 30 秒有心跳，不保证进程此刻存活">近期活跃实例</span>
              </div>
              <div>
                <b>{summary.requests}</b>
                <span>请求 · {summary.attempts} 次发送</span>
              </div>
            </div>
          </section>
          <section className="metric-strip" aria-label="计量分布">
            <div>
              <span>
                输入 <small>含缓存</small>
              </span>
              <b>{summary.inputKnown ? formatCount(summary.input) : '—'}</b>
            </div>
            <div>
              <span>输出</span>
              <b>{summary.outputKnown ? formatCount(summary.output) : '—'}</b>
            </div>
            <div>
              <span>
                缓存读取 <small>输入子集</small>
              </span>
              <b>{summary.cacheKnown ? formatCount(summary.cacheRead) : '—'}</b>
            </div>
            <div>
              <span>数据覆盖</span>
              <b>
                {summary.attempts - summary.unknown}
                <small> / {summary.attempts} 次发送</small>
              </b>
            </div>
          </section>
          {(summary.unknown > 0 || data.issues.length > 0) && (
            <div className="coverage-notice">
              {summary.unknown > 0 &&
                `${summary.unknown} 次发送未报告完整 usage；总量仅包含已收到的数据。 `}
              {data.issues.length > 0 &&
                `采集提示：${[...new Set(data.issues.map((issue) => issue.kind))].join('、')}`}
            </div>
          )}
          {view === 'overview' && (
            <section className="trend-section">
              <div className="section-title">
                <div>
                  <h2>消耗趋势</h2>
                  <span className="minor">按请求开始时间 · {summary.running} 个请求进行中</span>
                </div>
                <span className="chart-legend">
                  <i />
                  已报告 token
                </span>
              </div>
              <div className="trend-chart" role="img" aria-label="按请求开始时间的 token 消耗趋势">
                {trend.map((bin, index) => (
                  <div
                    key={index}
                    className="trend-column"
                    title={`${new Date(bin.time).toLocaleString()} · ${formatCount(bin.total)} token · ${bin.count} 请求`}
                  >
                    <span style={{ height: `${(bin.total / highest) * 100}%` }} />
                  </div>
                ))}
              </div>
              <div className="chart-axis">
                <span>
                  {new Date(trend[0].time).toLocaleString([], {
                    month: 'short',
                    day: 'numeric',
                    hour: '2-digit',
                    minute: '2-digit',
                  })}
                </span>
                <span>现在</span>
              </div>
            </section>
          )}
          <section className="records-section">
            <div className="section-title">
              <h2>{view === 'overview' ? '项目消耗' : titles[view]}</h2>
              <div className="record-tools">
                {(filters.project || filters.model || filters.session || filters.tool) && (
                  <button
                    type="button"
                    onClick={() =>
                      setFilters((previous) => ({
                        project: '',
                        search: '',
                        period: previous.period,
                      }))
                    }
                  >
                    清除筛选 ×
                  </button>
                )}
                {view === 'requests' && (
                  <input
                    aria-label="搜索请求"
                    placeholder="搜索模型、会话或请求…"
                    value={filters.search}
                    onChange={(event) =>
                      setFilters((previous) => ({ ...previous, search: event.target.value }))
                    }
                  />
                )}
              </div>
            </div>
            {pending ? (
              <div className="global-empty">正在读取筛选记录…</div>
            ) : data.stored_requests === 0 ? (
              <div className="global-empty">
                <span>↗</span>
                <h3>等待第一个 Paw 请求</h3>
                <p>在任意项目启动新版 Paw，所有实例的消耗会自动汇聚到这里。</p>
                <code>paw tracer --open</code>
              </div>
            ) : summary.requests === 0 ? (
              <div className="global-empty">
                <h3>当前筛选没有记录</h3>
                <p>试试其他期间或清除筛选。</p>
              </div>
            ) : view === 'tools' ? (
              <>
                <p className="minor">
                  ≈ 表示 mixed_text_v1
                  内容估算。每次发送都单独计入；定义、参数、结果不是三个独立计费项。
                </p>
                <div className="tool-list">
                  <div className="tool-table-heading">
                    <span>工具</span>
                    <span>定义 ≈</span>
                    <span>参数 ≈</span>
                    <span>结果 ≈</span>
                    <span>出现次数</span>
                  </div>
                  {data.tools.map((row) => (
                    <button
                      type="button"
                      className="tool-row"
                      key={row.name}
                      onClick={() => inspectGroup('tool', row.name)}
                    >
                      <b>{row.name}</b>
                      <span>{formatCount(row.definitions)}</span>
                      <span>{formatCount(row.arguments)}</span>
                      <span>
                        {formatCount(row.results)}
                        {row.unknown > 0 ? ' + 未知' : ''}
                      </span>
                      <span>{row.occurrences}</span>
                    </button>
                  ))}
                </div>
                {data.tools.length === 0 && (
                  <p className="coverage-notice">
                    当前记录没有可用的工具原子；未采集不等于零消耗。
                  </p>
                )}
              </>
            ) : view === 'requests' ? (
              requestRows(requests)
            ) : (
              groupedRows(
                view === 'models' ? 'model' : view === 'sessions' ? 'session' : 'project_id',
              )
            )}
            {data.pagination.total > 0 && (
              <nav className="records-pagination" aria-label="记录分页">
                <span>
                  {data.pagination.offset + 1}–
                  {Math.min(data.pagination.offset + data.pagination.limit, data.pagination.total)}{' '}
                  / {formatCount(data.pagination.total)} 条
                </span>
                <div>
                  <label>
                    每页{' '}
                    <select
                      aria-label="每页条数"
                      value={query.limit}
                      disabled={pending}
                      onChange={(event) =>
                        onQueryChange({ ...query, limit: Number(event.target.value), offset: 0 })
                      }
                    >
                      <option value={50}>50</option>
                      <option value={100}>100</option>
                      <option value={250}>250</option>
                    </select>
                  </label>
                  <button
                    type="button"
                    disabled={pending || data.pagination.offset === 0}
                    onClick={() =>
                      onQueryChange({
                        ...query,
                        offset: Math.max(0, data.pagination.offset - query.limit),
                      })
                    }
                  >
                    上一页
                  </button>
                  <button
                    type="button"
                    disabled={
                      pending || data.pagination.offset + query.limit >= data.pagination.total
                    }
                    onClick={() =>
                      onQueryChange({ ...query, offset: data.pagination.offset + query.limit })
                    }
                  >
                    下一页
                  </button>
                </div>
              </nav>
            )}
          </section>
          <footer className="global-footer">
            <span>供应商报告值与组件估算分开呈现 · 未报告 ≠ 0</span>
            <span>更新于 {displayTime(data.generated_at)}</span>
          </footer>
        </main>
      </div>
      {selected && (
        <RequestInspector
          target={selected}
          projectName={projectName(selected.project_id)}
          onClose={() => setSelected(null)}
        />
      )}
    </div>
  );
}
