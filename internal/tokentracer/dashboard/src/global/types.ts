export interface Tokens {
  input: number;
  output: number;
  cache_read: number;
  cache_creation: number;
  reasoning: number;
}
export interface Knowledge {
  total?: boolean;
  input: boolean;
  output: boolean;
  cache_read: boolean;
  cache_creation: boolean;
  reasoning: boolean;
}
export interface Atom {
  path: string;
  category: string;
  tool_name?: string;
  wire_bytes: number;
  estimated_tokens?: number;
  estimator?: string;
}
export interface Attempt {
  number: number;
  transport: string;
  started_at: string;
  status: string;
  http_status?: number;
  tokens?: Tokens;
  known: Knowledge;
  usage_source?: string;
  precision?: string;
  provider_response_id?: string;
  usage_finality?: 'final' | 'partial';
  atoms?: Atom[];
  atoms_state?: string;
  request_body_bytes?: number;
}
export interface GlobalRequest {
  id: string;
  instance_id: string;
  project_id: string;
  scope: {
    session_id?: string;
    turn_id?: string;
    task_id?: string;
    parent_task_id?: string;
    parent_session_id?: string;
    purpose?: string;
  };
  provider: string;
  model: string;
  started_at: string;
  updated_at: string;
  status: string;
  attempts: Attempt[];
  summary?: RequestSummary;
}
export interface Instance {
  id: string;
  project_id: string;
  project_name: string;
  workspace: string;
  parent_instance_id?: string;
  started_at: string;
  updated_at: string;
  status: string;
  error?: string;
}
export interface GlobalSnapshot {
  version: number;
  generated_at: string;
  instances: Instance[];
  requests: GlobalRequest[];
  total: Tokens;
  coverage: { requests: number; attempts: number; reported_attempts: number };
  issues: { instance_id?: string; kind: string }[];
  debug_available?: boolean;
}
export interface GlobalFilters {
  project: string;
  search: string;
  period: '24h' | '7d' | '30d' | 'all';
  model?: string;
  session?: string;
  tool?: string;
}
export type GlobalView = 'overview' | 'projects' | 'sessions' | 'models' | 'tools' | 'requests';
export interface GlobalQuery extends GlobalFilters {
  view: GlobalView;
  offset: number;
  limit: number;
}
export interface RequestSummary {
  requests: number;
  attempts: number;
  unknown: number;
  reported: number;
  total: number;
  input: number;
  inputKnown: number;
  output: number;
  outputKnown: number;
  cacheRead: number;
  cacheCreation: number;
  cacheKnown: number;
  reasoning: number;
  running: number;
}
export interface RequestRow {
  id: string;
  instance_id: string;
  project_id: string;
  model: string;
  started_at: string;
  status: string;
  purpose: string;
  summary: RequestSummary;
}
export interface GlobalGroup {
  id: string;
  label: string;
  project_id?: string;
  summary: RequestSummary;
}
export interface ToolRow {
  name: string;
  occurrences: number;
  definitions: number;
  arguments: number;
  results: number;
  unknown: number;
}
export interface GlobalPage {
  version: number;
  generated_at: string;
  query: GlobalQuery;
  instances: Instance[];
  issues: GlobalSnapshot['issues'];
  summary: RequestSummary;
  stored_requests: number;
  project_count: number;
  active_instances: number;
  requests: RequestRow[];
  groups: GlobalGroup[];
  tools: ToolRow[];
  trend: { time: number; total: number; count: number }[];
  pagination: { offset: number; limit: number; total: number };
  debug_available?: boolean;
}
