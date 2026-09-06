import { queryURL, validPage, validRequest } from './api';

test('query URL preserves all filters and resets pagination only for export', () => {
  const query = { view: 'requests' as const, period: 'all' as const, project: 'a/b', search: '中文 ?&', session: 'a/b/s', model: 'model', tool: 'Read', offset: 300, limit: 50 };
  const params = new URL(queryURL(query), 'http://localhost').searchParams;
  expect(params.get('offset')).toBe('300');
  expect(params.get('search')).toBe('中文 ?&');
  expect(params.get('project')).toBe('a/b');
  const exported = new URL(queryURL(query, '/api/global/export'), 'http://localhost').searchParams;
  expect(exported.get('tool')).toBe('Read');
  expect(exported.has('offset')).toBe(false);
  expect(exported.has('limit')).toBe(false);
});

test('rejects malformed page and detail shapes before rendering', () => {
  expect(validPage({ version: 1, requests: [], instances: [], issues: [] })).toBe(false);
  expect(validPage({ version: 1, generated_at: 'not a date', summary: {}, trend: [] })).toBe(false);
  expect(validRequest({ id: 'x', instance_id: 'i', attempts: [{ known: null }] })).toBe(false);
});
