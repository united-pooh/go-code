import { expect, test, type Page } from '@playwright/test';
import type { SessionSnapshot } from '../src/api/types';

function conversation(count = 30): SessionSnapshot {
  return {
    session_id: 'nav', session_version: 1, event_sequence: 0, stream_id: 'nav-stream', earlier_cursor: 'earlier',
    turns: Array.from({ length: count }, (_, index) => ({
      turn_id: `turn-${index + 1}`, status: 'completed', messages: [
        { role: 'user', content: `问题 ${index + 1}：检查会话导航和正文排版。` },
        { role: 'assistant', assistant_parts: [{ type: 'reasoning', reasoning: { text: '合成轨迹：先核对内容，再检查滚动位置。'.repeat(4) } }] },
        { role: 'assistant', content: `## 回答 ${index + 1}\n\n` + '这是合成测试正文，用来验证阅读位置和浮钮覆盖，不是真实会话。'.repeat(12) },
      ],
    })),
  };
}

async function openConversation(page: Page, count = 30) {
  const snapshot = conversation(count);
  await page.addInitScript(() => {
    const sources = new Set<EventTarget>();
    class FixtureEventSource extends EventTarget {
      onopen: (() => void) | null = null;
      onerror: (() => void) | null = null;
      constructor() { super(); sources.add(this); queueMicrotask(() => this.onopen?.()); }
      close() { sources.delete(this); }
    }
    Object.defineProperty(window, 'EventSource', { value: FixtureEventSource });
    window.addEventListener('fixture:sse', event => {
      const data = (event as CustomEvent).detail;
      for (const source of sources) source.dispatchEvent(new MessageEvent(data.type, { data: JSON.stringify(data) }));
    });
  });
  await page.route('**/api/**', route => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/bootstrap') return route.fulfill({ json: {
      schema_version: 1, recent_workspaces: [], loaded_workspaces: [{ id: 'w', path: '/synthetic', name: 'Navigation fixture', last_opened_at: '' }], loaded_runtimes: 1,
    } });
    if (path === '/api/workspaces/w/sessions') return route.fulfill({ json: { items: ['nav', 'other'].map(id => ({ session_id: id, title: id === 'nav' ? '导航验证（合成数据）' : '另一段合成会话', created_at: '2026-09-08T00:00:00Z', last_used_at: '2026-09-08T00:00:00Z', transcript_size: 1 })) } });
    if (path === '/api/workspaces/w/sessions/nav') return route.fulfill({ json: snapshot });
    if (path === '/api/workspaces/w/sessions/other') return route.fulfill({ json: { ...conversation(2), session_id: 'other', earlier_cursor: undefined } });
    if (path.endsWith('/model-options')) return route.fulfill({ json: { models: [], active_model_id: '', effort_options: [] } });
    if (path.endsWith('/events')) return route.fulfill({ status: 204 });
    return route.fulfill({ status: 404, json: { error: `unexpected fixture route: ${path}` } });
  });
  await page.goto('/');
  await expect(page.locator('article.message.user')).toHaveCount(count);
  return snapshot;
}

test('dense tick spacing stays compact while mobile menu keeps touch targets', async ({ page }, testInfo) => {
  await openConversation(page);
  for (const width of [1440, 900]) {
    await page.setViewportSize({ width, height: 900 });
    const ticks = page.locator('.turn-tick');
    await expect(ticks).toHaveCount(30);
    const geometry = await ticks.evaluateAll(nodes => nodes.map((node, index) => ({
      pitch: index ? node.getBoundingClientRect().top - nodes[index - 1].getBoundingClientRect().top : 10,
      stroke: node.querySelector('span')!.getBoundingClientRect().height,
    })));
    expect(geometry).toEqual(Array.from({ length: 30 }, () => ({ pitch: 10, stroke: 2 })));
    expect(await page.locator('.turn-list').evaluate(el => el.scrollHeight - el.clientHeight)).toBe(0);
    await page.screenshot({ path: testInfo.outputPath(`dense-ticks-${width}.png`), animations: 'disabled' });
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole('button', { name: '轮次导航', exact: true }).click();
  await expect(page.locator('.turn-menu-item').first()).toHaveCSS('min-height', '44px');
});

test('scrollbar belongs to workspace edge and return button is opaque over text', async ({ page }, testInfo) => {
  await openConversation(page);
  const viewport = page.locator('.conversation-view');
  await viewport.evaluate(el => { el.scrollTop = 500; });
  const notice = page.getByRole('button', { name: '↓ 回到最新', exact: true });
  await expect(notice).toBeVisible();
  await expect(notice).toHaveCSS('background-color', 'rgb(255, 255, 255)');
  expect(await notice.evaluate(el => Number(getComputedStyle(el).zIndex) - Number(getComputedStyle(document.querySelector('.dock')!).zIndex))).toBeGreaterThan(0);
  const geometry = await viewport.evaluate(el => ({
    right: el.getBoundingClientRect().right,
    edge: el.closest('.main-workspace')!.getBoundingClientRect().right,
    contentWidth: el.querySelector('.conversation-content')!.getBoundingClientRect().width,
  }));
  expect(geometry.right).toBe(geometry.edge);
  expect(geometry.contentWidth).toBe(768);
  await notice.hover();
  await expect(notice).toHaveCSS('background-color', 'rgb(255, 255, 255)');
  await notice.focus();
  await expect(notice).toHaveCSS('background-color', 'rgb(255, 255, 255)');
  await page.screenshot({ path: testInfo.outputPath('opaque-notice.png'), animations: 'disabled' });
  await notice.click();
  await expect(notice).toHaveCount(0);
});

test('desktop previews, jumps and tracks reading without widening the transcript', async ({ page }, testInfo) => {
  await openConversation(page);
  const sixth = page.getByRole('button', { name: '已加载第 6 轮：问题 6：检查会话导航和正文排版。', exact: true });
  await sixth.hover();
  await expect(page.getByRole('tooltip')).toContainText('合成测试正文');
  await page.getByRole('tooltip').hover();
  await expect(page.getByRole('tooltip')).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('navigation-preview.png'), animations: 'disabled' });
  await sixth.click();
  await expect(sixth).toHaveAttribute('aria-current', 'true');
  await expect(page.getByRole('tooltip')).toHaveCount(0);
  const anchor = page.locator('[data-turn-id="turn-6"][data-turn-question]');
  await expect(anchor).toBeFocused();
  await expect.poll(() => anchor.evaluate(el => el.getBoundingClientRect().top - el.closest('.conversation-view')!.getBoundingClientRect().top)).toBeCloseTo(24, 0);
  await page.screenshot({ path: testInfo.outputPath('navigation-wide.png'), animations: 'disabled' });
  await page.getByRole('button', { name: '轨迹', exact: true }).click();
  await page.screenshot({ path: testInfo.outputPath('navigation-trace.png'), animations: 'disabled' });
  await sixth.click();
  await expect.poll(() => anchor.evaluate(el => el.getBoundingClientRect().top - el.closest('.conversation-view')!.getBoundingClientRect().top)).toBeCloseTo(24, 0);
  await page.getByRole('button', { name: '对话', exact: true }).click();
  await page.screenshot({ path: testInfo.outputPath('navigation-trace-collapsed.png'), animations: 'disabled' });
  await sixth.click();

  await sixth.focus();
  await page.keyboard.press('ArrowDown');
  await expect(page.getByRole('button', { name: '已加载第 7 轮：问题 7：检查会话导航和正文排版。' })).toBeFocused();
  await expect(sixth).toHaveAttribute('aria-current', 'true');
  await page.keyboard.press('Enter');
  await expect(page.locator('.turn-tick[aria-current=true]')).toHaveAttribute('aria-label', /^已加载第 7 轮/);

  const input = page.getByLabel('消息');
  await input.fill(Array.from({ length: 20 }, (_, i) => `第${i}行`).join('\n'));
  await expect(input).toHaveCSS('height', '200px');
  await expect.poll(() => page.locator('.scroll-to-latest').evaluate(el => {
    const composer = document.querySelector('.composer')!;
    return composer.getBoundingClientRect().top - el.getBoundingClientRect().bottom;
  })).toBeGreaterThanOrEqual(12);
  await page.screenshot({ path: testInfo.outputPath('navigation-expanded-input.png'), animations: 'disabled' });
  await page.setViewportSize({ width: 1440, height: 450 });
  await sixth.hover();
  await expect(page.getByRole('tooltip')).toBeVisible();
  await expect(page.locator('.turn-preview-card')).toHaveCSS('overflow-y', 'auto');
  await expect.poll(() => page.locator('.turn-preview-card').evaluate(el => document.querySelector('.composer')!.getBoundingClientRect().top - el.getBoundingClientRect().bottom)).toBeGreaterThanOrEqual(12);
  await page.screenshot({ path: testInfo.outputPath('navigation-short-window.png'), animations: 'disabled' });
  await page.setViewportSize({ width: 1440, height: 900 });
  await input.fill('');

  await page.setViewportSize({ width: 900, height: 900 });
  await expect(page.locator('.turn-tick').first()).toHaveCSS('width', '21px');
  await page.screenshot({ path: testInfo.outputPath('navigation-narrow.png'), animations: 'disabled' });
  await page.getByRole('button', { name: '切换会话面板' }).click();
  await expect.poll(() => page.locator('.conversation-view').evaluate(el => el.getBoundingClientRect().right - el.closest('.main-workspace')!.getBoundingClientRect().right)).toBe(0);
});

test('100 loaded turns stay reachable and the narrow menu restores focus', async ({ page }, testInfo) => {
  const errors: string[] = [];
  page.on('pageerror', error => errors.push(error.message));
  await openConversation(page, 100);
  expect(await page.locator('.turn-list').evaluate(el => el.scrollHeight > el.clientHeight)).toBe(true);
  const first = page.getByRole('button', { name: '已加载第 1 轮：问题 1：检查会话导航和正文排版。' });
  await first.focus();
  await page.keyboard.press('End');
  await expect(page.getByRole('button', { name: '已加载第 100 轮：问题 100：检查会话导航和正文排版。' })).toBeFocused();
  await page.keyboard.press('Home');
  await expect(first).toBeFocused();
  await page.setViewportSize({ width: 390, height: 844 });
  const toggle = page.getByRole('button', { name: '轮次导航', exact: true });
  await expect(toggle).toBeVisible();
  await expect(toggle).toBeFocused();
  await toggle.click();
  await expect(page.getByRole('dialog')).toContainText('已加载 100 轮 · 更早记录未加载');
  await page.screenshot({ path: testInfo.outputPath('navigation-mobile.png'), animations: 'disabled' });
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(390);
  await page.keyboard.press('Escape');
  await expect(toggle).toBeFocused();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await page.setViewportSize({ width: 1440, height: 900 });
  await expect(page.locator('.turn-tick[tabindex="0"]')).toBeFocused();
  await expect(toggle).toHaveCount(0);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(toggle).toBeFocused();
  await toggle.click();
  await page.getByRole('button', { name: '已加载第 4 轮：问题 4：检查会话导航和正文排版。' }).click();
  await expect(page.locator('[data-turn-id="turn-4"][data-turn-question]')).toBeFocused();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await page.screenshot({ path: testInfo.outputPath('navigation-mobile-reading.png'), animations: 'disabled' });
  expect(errors).toEqual([]);
});

test('new stream content preserves historical reading, then returns and resets on session switch', async ({ page }, testInfo) => {
  const snapshot = await openConversation(page);
  await page.getByRole('button', { name: '已加载第 6 轮：问题 6：检查会话导航和正文排版。' }).click();
  const viewport = page.locator('.conversation-view');
  const position = await viewport.evaluate(el => el.scrollTop);
  const emit = async (sequence: number, type: string, payload: object) => {
    await page.evaluate(detail => window.dispatchEvent(new CustomEvent('fixture:sse', { detail })), {
      schema_version: 1, stream_id: 'nav-stream', sequence, workspace_id: 'w', session_id: 'nav', turn_id: 'turn-31',
      type, payload, time: '2026-09-08T00:01:00Z', entity_version: 2,
    });
  };
  await emit(1, 'turn.started', {});
  await emit(2, 'assistant.part.started', { part_id: 'p31', part_index: 0, kind: 'assistant' });
  await emit(3, 'assistant.delta', { part_id: 'p31', offset: 0, text: '这是新的流式回答。'.repeat(20) });
  await expect(page.getByRole('button', { name: '↓ 1 条新消息' })).toBeVisible();
  await expect(page.locator('.message.assistant.live')).toContainText('新的流式回答');
  await expect(page.locator('.turn-tick')).toHaveCount(31);
  expect(await viewport.evaluate(el => el.scrollTop)).toBe(position);
  await expect(page.locator('.turn-tick[aria-current=true]')).toHaveAttribute('aria-label', /^已加载第 6 轮/);
  snapshot.turns.push({ turn_id: 'turn-31', status: 'completed', messages: [{ role: 'user', content: '新问题' }, { role: 'assistant', content: '最终回答' }] });
  snapshot.event_sequence = 4;
  await emit(4, 'turn.completed', {});
  await expect(page.locator('.message.assistant').last()).toContainText('最终回答');
  await expect(page.locator('.turn-tick')).toHaveCount(31);
  expect(await viewport.evaluate(el => el.scrollTop)).toBe(position);
  await page.screenshot({ path: testInfo.outputPath('navigation-unread.png'), animations: 'disabled' });
  await page.getByRole('button', { name: /条新消息/ }).click();
  await expect(page.locator('.scroll-to-latest')).toHaveCount(0);
  await expect(page.locator('.turn-tick[aria-current=true]')).toHaveAttribute('aria-label', /^已加载第 31 轮/);
  await page.locator('.turn-tick').last().hover();
  await expect(page.getByRole('tooltip')).toContainText('最终回答');
  await page.getByRole('button', { name: /另一段合成会话/ }).click();
  await expect(page.locator('.turn-tick')).toHaveCount(2);
  await expect(page.getByRole('tooltip')).toHaveCount(0);
  await expect(page.locator('.turn-tick[aria-current=true]')).toHaveAttribute('aria-label', /^第 2 轮/);
});
