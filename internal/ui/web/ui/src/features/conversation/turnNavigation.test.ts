import type { StreamingPart, TurnProjection } from '../../api/types';
import { buildTurnNavigation } from './turnNavigation';

it('uses one item per turn, the first question and last answer', () => {
  expect(buildTurnNavigation([{ turn_id: 't', messages: [
    { role: 'user', content: ' Q\nquestion ' }, { role: 'assistant', content: 'interim' },
    { role: 'user', content: 'steer' }, { role: 'assistant', content: 'final' },
  ] }], {})).toEqual([{ turnID: 't', question: 'Q question', answer: 'final' }]);
});

it.each([
  ['pending', '等待回答'], ['running', '生成中'], ['failed', '回答失败'],
  ['cancelled', '已中断'], ['interrupted', '已中断'], ['completed', '暂无回答'], [undefined, '暂无回答'],
])('labels missing answers for %s', (status, answer) => {
  expect(buildTurnNavigation([{ turn_id: 't', status, messages: [{ role: 'user', content: 'Q' }] }], {})[0].answer).toBe(answer);
});

it('excludes tool-only/empty turns and never previews hidden reasoning or tool content', () => {
  const turns: TurnProjection[] = [
    { turn_id: 'empty', messages: [] },
    { turn_id: 'tool', messages: [{ role: 'tool', content: 'secret output' }] },
    { turn_id: 'answer', messages: [{ role: 'assistant', content: 'A', assistant_parts: [{ type: 'reasoning', reasoning: { text: 'hidden' } }] }] },
  ];
  expect(buildTurnNavigation(turns, {})).toEqual([{ turnID: 'answer', question: '无提问文本', answer: 'A' }]);
});

it('uses same-turn streaming fallback, then authoritative snapshot text', () => {
  const parts: Record<string, StreamingPart> = {
    p: { part_id: 'p', session_id: 's', turn_id: 't', kind: 'assistant', text: 'live' },
    r: { part_id: 'r', session_id: 's', turn_id: 't', kind: 'reasoning', text: 'hidden' },
    other: { part_id: 'other', session_id: 's', turn_id: 'other', kind: 'assistant', text: 'unrelated' },
  };
  expect(buildTurnNavigation([{ turn_id: 't', messages: [] }], parts)[0]).toEqual({ turnID: 't', question: '无提问文本', answer: 'live' });
  expect(buildTurnNavigation([{ turn_id: 't', messages: [{ role: 'assistant', content: 'saved' }] }], parts)[0].answer).toBe('saved');
});

it('clips Unicode codepoints and keeps markup as plain text', () => {
  const item = buildTurnNavigation([{ turn_id: 't', messages: [
    { role: 'user', content: '🐾'.repeat(121) }, { role: 'assistant', content: '<b>A</b>\n' + '字'.repeat(300) },
  ] }], {})[0];
  expect(item.question).toBe('🐾'.repeat(120) + '…');
  expect(item.answer.startsWith('<b>A</b> ')).toBe(true);
  expect(Array.from(item.answer)).toHaveLength(281);
});
