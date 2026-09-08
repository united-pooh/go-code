import type { StreamingPart, TurnProjection } from '../../api/types';

export interface TurnNavigationItem {
  turnID: string;
  question: string;
  answer: string;
}

function excerpt(text: string, limit: number): string {
  const chars = Array.from(text.replace(/\s+/g, ' ').trim());
  return chars.length > limit ? chars.slice(0, limit).join('') + '…' : chars.join('');
}

function missingAnswer(status?: string): string {
  switch (status) {
    case 'pending': case 'queued': return '等待回答';
    case 'running': return '生成中';
    case 'cancelled': case 'interrupted': return '已中断';
    case 'failed': return '回答失败';
    default: return '暂无回答';
  }
}

export function buildTurnNavigation(turns: TurnProjection[], parts: Record<string, StreamingPart>): TurnNavigationItem[] {
  const streaming = new Map<string, string>();
  for (const part of Object.values(parts)) {
    if (part.kind === 'assistant' && part.text.trim()) {
      streaming.set(part.turn_id, (streaming.get(part.turn_id) ?? '') + part.text);
    }
  }
  const items: TurnNavigationItem[] = [];
  for (const turn of turns) {
    let question = '';
    let answer = '';
    for (const message of turn.messages) {
      const text = message.content?.trim();
      if (!text) continue;
      if (message.role === 'user' && !question) question = text;
      if (message.role === 'assistant') answer = text;
    }
    answer ||= streaming.get(turn.turn_id) ?? '';
    if (!question && !answer) continue;
    items.push({
      turnID: turn.turn_id,
      question: excerpt(question || '无提问文本', 120),
      answer: excerpt(answer || missingAnswer(turn.status), 280),
    });
  }
  return items;
}
