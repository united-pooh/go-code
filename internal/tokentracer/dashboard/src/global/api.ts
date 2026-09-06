import type { GlobalPage, GlobalQuery, GlobalRequest } from './types';

export const initialQuery: GlobalQuery = {
  view: 'overview',
  period: '24h',
  project: '',
  search: '',
  offset: 0,
  limit: 50,
};

export function queryURL(query: GlobalQuery, path = '/api/global'): string {
  const params = new URLSearchParams();
  for (const key of [
    'view',
    'period',
    'project',
    'search',
    'session',
    'model',
    'tool',
    'offset',
    'limit',
  ] as const) {
    if (path.endsWith('/export') && (key === 'offset' || key === 'limit')) continue;
    const value = query[key];
    if (value !== undefined && value !== '') params.set(key, String(value));
  }
  return `${path}?${params}`;
}

const object = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null;
const number = (value: unknown): value is number =>
  typeof value === 'number' && Number.isFinite(value) && value >= 0;
const fields = (value: unknown, keys: string[]) =>
  object(value) && keys.every((key) => typeof value[key] === 'string');
const numbers = (value: unknown, keys: string[]) =>
  object(value) && keys.every((key) => number(value[key]));
const summary = (value: unknown) =>
  numbers(value, [
    'requests',
    'attempts',
    'unknown',
    'reported',
    'total',
    'input',
    'inputKnown',
    'output',
    'outputKnown',
    'cacheRead',
    'cacheCreation',
    'cacheKnown',
    'reasoning',
    'running',
  ]);
const date = (value: unknown) => typeof value === 'string' && Number.isFinite(Date.parse(value));

export function validPage(value: unknown): value is GlobalPage {
  if (
    !object(value) ||
    value.version !== 2 ||
    !date(value.generated_at) ||
    !summary(value.summary) ||
    !numbers(value, ['stored_requests', 'project_count', 'active_instances'])
  )
    return false;
  if (
    !object(value.query) ||
    !['overview', 'projects', 'sessions', 'models', 'tools', 'requests'].includes(
      String(value.query.view),
    ) ||
    !['24h', '7d', '30d', 'all'].includes(String(value.query.period))
  )
    return false;
  if (
    !numbers(value.pagination, ['offset', 'limit', 'total']) ||
    !object(value.pagination) ||
    Number(value.pagination.limit) < 1 ||
    Number(value.pagination.limit) > 250
  )
    return false;
  return (
    Array.isArray(value.instances) &&
    value.instances.every((instance) =>
      fields(instance, ['id', 'project_id', 'project_name', 'workspace', 'status']),
    ) &&
    Array.isArray(value.issues) &&
    value.issues.every((issue) => fields(issue, ['kind'])) &&
    Array.isArray(value.requests) &&
    value.requests.length <= 250 &&
    value.requests.every(
      (row) =>
        fields(row, ['id', 'instance_id', 'project_id', 'model', 'status', 'purpose']) &&
        object(row) &&
        date(row.started_at) &&
        summary(row.summary),
    ) &&
    Array.isArray(value.groups) &&
    value.groups.length <= 250 &&
    value.groups.every(
      (row) => fields(row, ['id', 'label']) && object(row) && summary(row.summary),
    ) &&
    Array.isArray(value.tools) &&
    value.tools.length <= 250 &&
    value.tools.every(
      (row) =>
        fields(row, ['name']) &&
        numbers(row, ['occurrences', 'definitions', 'arguments', 'results', 'unknown']),
    ) &&
    Array.isArray(value.trend) &&
    value.trend.length === 24 &&
    value.trend.every((bin) => numbers(bin, ['time', 'total', 'count']))
  );
}

export function validRequest(value: unknown): value is GlobalRequest {
  if (
    !object(value) ||
    !fields(value, ['id', 'instance_id', 'project_id', 'provider', 'model', 'status']) ||
    !date(value.started_at) ||
    !date(value.updated_at) ||
    !object(value.scope) ||
    !Array.isArray(value.attempts)
  )
    return false;
  if (value.summary !== undefined && !summary(value.summary)) return false;
  return value.attempts.every(
    (attempt) =>
      object(attempt) &&
      number(attempt.number) &&
      fields(attempt, ['transport', 'status']) &&
      object(attempt.known) &&
      (attempt.provider_response_id === undefined ||
        typeof attempt.provider_response_id === 'string') &&
      (attempt.usage_finality === undefined ||
        ['final', 'partial'].includes(String(attempt.usage_finality))) &&
      ['input', 'output', 'cache_read', 'cache_creation', 'reasoning'].every(
        (key) => typeof (attempt.known as Record<string, unknown>)[key] === 'boolean',
      ) &&
      (attempt.tokens === undefined ||
        numbers(attempt.tokens, [
          'input',
          'output',
          'cache_read',
          'cache_creation',
          'reasoning',
        ])) &&
      (attempt.atoms === undefined ||
        (Array.isArray(attempt.atoms) &&
          attempt.atoms.every(
            (atom) =>
              fields(atom, ['path', 'category']) &&
              object(atom) &&
              number(atom.wire_bytes) &&
              (atom.estimated_tokens === undefined || number(atom.estimated_tokens)),
          ))),
  );
}

export async function fetchJSON<T>(
  url: string,
  signal: AbortSignal,
  validate: (value: unknown) => value is T,
): Promise<T> {
  const response = await fetch(url, { cache: 'no-store', signal });
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
  const data: unknown = await response.json();
  if (!validate(data)) throw new Error('invalid telemetry response');
  return data;
}
