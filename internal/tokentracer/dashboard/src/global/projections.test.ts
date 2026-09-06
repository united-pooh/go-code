import { summarizeRequests, requestKey } from './projections';
import type { GlobalRequest } from './types';

const request = (project: string, instance: string, output: number): GlobalRequest => ({
  id: 'same-request', instance_id: instance, project_id: project, scope: { session_id: 'session' }, provider: 'fixture', model: 'model', started_at: '2026-09-07T10:00:00Z', updated_at: '2026-09-07T10:00:01Z', status: 'completed',
  attempts: [{ number: 1, transport: 'openai-compatible', started_at: '2026-09-07T10:00:00Z', status: 'headers', tokens: { input: 80, cache_read: 20, cache_creation: 0, output, reasoning: 3 }, known: { input: true, output: true, cache_read: true, cache_creation: false, reasoning: true }, atoms: [{ path: '/tools/0', category: 'tool_definition', tool_name: 'Read', wire_bytes: 100, estimated_tokens: 25, estimator: 'mixed_text_v1' }] }],
});

test('keys by identity and sums detail attempts without adding reasoning twice', () => {
  const requests = [request('p1', 'a', 5), request('p2', 'b', 7)];
  expect(requestKey(requests[0])).not.toBe(requestKey(requests[1]));
  expect(summarizeRequests(requests).total).toBe(212);
});

test('an attempt without usage remains unknown, including during retries', () => {
  const input = request('p1', 'a', 5);
  input.attempts.push({ number: 2, transport: 'openai-compatible', started_at: input.started_at, status: 'network_error', known: { input: false, output: false, cache_read: false, cache_creation: false, reasoning: false } });
  input.attempts[0].usage_finality = 'final';
  expect(summarizeRequests([input])).toMatchObject({ requests: 1, attempts: 2, unknown: 1, total: 105 });
});

test('partial usage is visible without inventing an input breakdown', () => {
  const input = request('p1', 'a', 5);
  input.attempts[0].known.input = false;
  expect(summarizeRequests([input])).toMatchObject({ total: 105, input: 0, inputKnown: 0, reported: 1, unknown: 1 });
});

test('detail uses server accounting without summing response replays again', () => {
  const input = request('p1', 'a', 5);
  const accounted = summarizeRequests([input]);
  input.attempts.push({ ...input.attempts[0], number: 2 });
  Object.assign(input, { summary: { ...accounted, attempts: 2, reported: 2, unknown: 1 } });
  expect(summarizeRequests([input])).toMatchObject({ total: 105, attempts: 2, unknown: 1 });
});

test('reported fields with partial finality cannot claim full coverage', () => {
  const input = request('p1', 'a', 5);
  Object.assign(input.attempts[0], { usage_finality: 'partial' });
  expect(summarizeRequests([input])).toMatchObject({ total: 105, reported: 1, unknown: 1 });
});
