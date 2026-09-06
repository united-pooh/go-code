import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { GlobalApp } from './GlobalApp';
import { initialQuery } from './api';
import { summarizeRequests } from './projections';
import type { GlobalPage, GlobalQuery, GlobalRequest } from './types';

const detail: GlobalRequest = { id: 'request-0', instance_id: 'instance', project_id: 'project', provider: 'fixture', model: 'fixture-model', started_at: '2026-09-07T10:00:00Z', updated_at: '2026-09-07T10:00:01Z', status: 'completed', scope: { session_id: 'session', purpose: 'conversation' }, attempts: [{ number: 1, transport: 'openai-compatible', status: 'headers', started_at: '2026-09-07T10:00:00Z', tokens: { input: 100, output: 5, cache_read: 0, cache_creation: 0, reasoning: 0 }, known: { input: true, output: true, cache_read: false, cache_creation: false, reasoning: false }, atoms_state: 'complete', atoms: [{ path: '/messages/0/content', category: 'user_prompt', wire_bytes: 32, estimated_tokens: 8, estimator: 'mixed_text_v1' }] }] };
function fixturePage(query: GlobalQuery, count = 601): GlobalPage {
  const summary = summarizeRequests(Array.from({ length: count }, (_, i) => ({ ...detail, id: `request-${i}` })));
  const requests = query.view === 'requests' ? Array.from({ length: Math.min(query.limit, count-query.offset) }, (_, index) => ({ id: `request-${query.search || query.offset+index}`, instance_id: 'instance', project_id: 'project', model: 'fixture-model', started_at: detail.started_at, status: 'completed', purpose: 'conversation', summary: summarizeRequests([detail]) })) : [];
  return { version: 2, generated_at: '2026-09-07T11:00:00Z', query, summary, stored_requests: count, project_count: count ? 1 : 0, active_instances: count ? 1 : 0, issues: [],
    instances: [{ id: 'instance', project_id: 'project', project_name: 'paw-project', workspace: '/work/paw-project', started_at: detail.started_at, updated_at: detail.updated_at, status: 'recent' }],
    requests, groups: count && query.view !== 'requests' ? [{ id: 'project', label: 'project', summary }] : [], tools: [], trend: Array.from({ length: 24 }, (_, i) => ({ time: Date.parse(detail.started_at)+i*3600000, total: i === 0 ? summary.total : 0, count: i === 0 ? count : 0 })), pagination: { offset: query.offset, limit: query.limit, total: query.view === 'requests' ? count : count ? 1 : 0 } };
}
function parsedQuery(url: string): GlobalQuery {
  const values = Object.fromEntries(new URL(url, 'http://localhost').searchParams);
  return { ...initialQuery, ...values, offset: Number(values.offset ?? 0), limit: Number(values.limit ?? 50) } as GlobalQuery;
}
const response = (data: unknown) => new Response(JSON.stringify(data), { status: 200 });
function mockServer(count = 601) {
  const mock = vi.fn(async (url: string) => url.startsWith('/api/global/request?') ? response({ ...detail, id: new URL(url, 'http://localhost').searchParams.get('request_id') }) : response(fixturePage(parsedQuery(url), count)));
  vi.stubGlobal('fetch', mock);
  return mock;
}
afterEach(() => { vi.unstubAllGlobals(); });

test('global overview drills into requests and labels estimates honestly', async () => {
  const user = userEvent.setup();
  const mock = mockServer();
  render(<GlobalApp />);
  expect(await screen.findByRole('heading', { name: '消耗概览' })).toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: '请求' }));
  expect(mock.mock.calls.some(([url]) => url.includes('/request?'))).toBe(false);
  await user.click(await screen.findByRole('button', { name: '查看请求 request-0' }));
  expect(await screen.findByText('输入提示词')).toBeInTheDocument();
  expect(screen.getByRole('complementary', { name: '请求明细' })).toHaveTextContent('≈8');
  expect(screen.getByText(/不是供应商逐项账单/)).toBeInTheDocument();
});

test('empty global storage is an empty state, not a connection failure', async () => {
  mockServer(0);
  render(<GlobalApp />);
  expect(await screen.findByText('等待第一个 Paw 请求')).toBeInTheDocument();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

test('project metadata lookup keeps the last instance and falls back to its id', async () => {
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    const page = fixturePage(parsedQuery(url), 1);
    page.instances.push({ ...page.instances[0], id: 'second-instance', project_name: 'latest-project', workspace: '/work/latest-project' });
    page.groups.push({ id: 'missing-project', label: 'missing-project', summary: page.summary });
    return response(page);
  }));
  render(<GlobalApp />);
  const project = await screen.findByRole('button', { name: /latest-project.*105/ });
  expect(project).toHaveTextContent('/work/latest-project');
  expect(screen.queryByRole('button', { name: /paw-project.*105/ })).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: /missing-project.*105/ })).toBeInTheDocument();
});

test('request rows show partial reported tokens and scope project counts', async () => {
  const user = userEvent.setup();
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    const page = fixturePage(parsedQuery(url), 1);
    page.instances.push({ ...page.instances[0], id: 'other', project_id: 'other-project', project_name: 'other' });
    page.summary.unknown = 1;
    for (const row of page.requests) row.summary.unknown = 1;
    return response(page);
  }));
  render(<GlobalApp />);
  await screen.findByRole('heading', { name: '消耗概览' });
  await user.click(screen.getByRole('button', { name: /paw-project.*105/ }));
  expect(await screen.findByRole('button', { name: '查看请求 request-0' })).toHaveTextContent('105');
  expect(screen.getByRole('region', { name: '消耗汇总' }).querySelector('.summary-side')).toHaveTextContent('1项目');
});

test('pagination asks the server for the next page without narrowing totals', async () => {
  const user = userEvent.setup();
  const mock = mockServer();
  render(<GlobalApp />);
  await screen.findByRole('heading', { name: '消耗概览' });
  await user.click(screen.getByRole('button', { name: '请求' }));
  await screen.findByRole('button', { name: '查看请求 request-0' });
  expect(screen.getAllByRole('button', { name: /查看请求/ })).toHaveLength(50);
  await user.click(screen.getByRole('button', { name: '下一页' }));
  await screen.findByRole('button', { name: '查看请求 request-50' });
  expect(screen.queryByRole('button', { name: '查看请求 request-0' })).not.toBeInTheDocument();
  expect(screen.getByRole('region', { name: '消耗汇总' })).toHaveTextContent('63,105');
  expect(mock.mock.calls.some(([url]) => url.includes('offset=50'))).toBe(true);
  await user.selectOptions(screen.getByLabelText('每页条数'), '250');
  await screen.findByRole('button', { name: '查看请求 request-0' });
  expect(screen.getAllByRole('button', { name: /查看请求/ })).toHaveLength(250);
});

test('aborts superseded filters and ignores a late stale response', async () => {
  const user = userEvent.setup();
  let resolveOld: (value: Response) => void = () => {};
  let oldSignal: AbortSignal | undefined;
  vi.stubGlobal('fetch', vi.fn((url: string, options: RequestInit) => {
    const query = parsedQuery(url);
    if (query.search === 'old') { oldSignal = options.signal as AbortSignal; return new Promise<Response>(resolve => { resolveOld = resolve; }); }
    return Promise.resolve(response(fixturePage(query, 1)));
  }));
  render(<GlobalApp />);
  await screen.findByRole('heading', { name: '消耗概览' });
  await user.click(screen.getByRole('button', { name: '请求' }));
  await user.type(screen.getByRole('textbox', { name: '搜索请求' }), 'old');
  await user.clear(screen.getByRole('textbox', { name: '搜索请求' }));
  await user.type(screen.getByRole('textbox', { name: '搜索请求' }), 'new');
  await screen.findByRole('button', { name: '查看请求 request-new' });
  expect(oldSignal?.aborted).toBe(true);
  await act(async () => resolveOld(response(fixturePage({ ...initialQuery, view: 'requests', search: 'old' }, 1))));
  expect(screen.queryByRole('button', { name: '查看请求 request-old' })).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: '查看请求 request-new' })).toBeInTheDocument();
});

test('a detail failure can be retried without losing the selected request', async () => {
  const user = userEvent.setup();
  let failed = false;
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    if (url.includes('/request?')) {
      if (!failed) { failed = true; return new Response('', { status: 503 }); }
      return response(detail);
    }
    return response(fixturePage(parsedQuery(url), 1));
  }));
  render(<GlobalApp />);
  await screen.findByRole('heading', { name: '消耗概览' });
  await user.click(screen.getByRole('button', { name: '请求' }));
  await user.click(await screen.findByRole('button', { name: '查看请求 request-0' }));
  await user.click(await screen.findByRole('button', { name: '重试明细' }));
  await waitFor(() => expect(screen.getByRole('complementary', { name: '请求明细' })).toHaveTextContent('输入提示词'));
});
