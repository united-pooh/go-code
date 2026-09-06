import type { GlobalRequest, RequestSummary, Tokens } from './types';

export function requestKey(request: Pick<GlobalRequest, 'instance_id' | 'id'>): string {
  return `${request.instance_id}/${request.id}`;
}
export function totalTokens(tokens?: Tokens): number {
  return tokens ? tokens.input + tokens.output + tokens.cache_read + tokens.cache_creation : 0;
}
export function summarizeRequests(requests: GlobalRequest[]): RequestSummary {
  const summary = {
    requests: 0,
    attempts: 0,
    unknown: 0,
    reported: 0,
    total: 0,
    input: 0,
    inputKnown: 0,
    output: 0,
    cacheRead: 0,
    cacheCreation: 0,
    reasoning: 0,
    cacheKnown: 0,
    outputKnown: 0,
    running: 0,
  };
  const seen = new Set<string>();
  for (const request of requests) {
    const key = requestKey(request);
    if (seen.has(key)) continue;
    seen.add(key);
    if (request.summary) {
      for (const field of Object.keys(summary) as (keyof RequestSummary)[])
        summary[field] += request.summary[field];
      continue;
    }
    summary.requests++;
    summary.running += Number(request.status === 'running');
    for (const attempt of request.attempts) {
      summary.attempts++;
      summary.unknown += Number(
        !attempt.known.input || !attempt.known.output || attempt.usage_finality !== 'final',
      );
      summary.cacheKnown += Number(attempt.known.cache_read);
      summary.outputKnown += Number(attempt.known.output);
      summary.inputKnown += Number(attempt.known.input);
      const tokens = attempt.tokens;
      if (!tokens) continue;
      summary.reported += Number(Object.values(attempt.known).some(Boolean));
      summary.total += totalTokens(tokens);
      if (attempt.known.input)
        summary.input += tokens.input + tokens.cache_read + tokens.cache_creation;
      summary.output += tokens.output;
      summary.cacheRead += tokens.cache_read;
      summary.cacheCreation += tokens.cache_creation;
      summary.reasoning += tokens.reasoning;
    }
  }
  return summary;
}
