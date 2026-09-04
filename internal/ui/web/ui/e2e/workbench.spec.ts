import { expect, test, type Page } from '@playwright/test';
import { readFileSync, existsSync } from 'node:fs';

// The fixture server prints a single-use bootstrap token to its log. The
// token is exchanged once through the API request context (which shares the
// browser context's cookie jar), so the tests reuse the same session.

function bootstrapToken(): string {
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    if (existsSync('/tmp/paw-webfixture.log')) {
      const match = readFileSync('/tmp/paw-webfixture.log', 'utf8').match(/https?:\/\/\S+#bootstrap=\S+/);
      if (match) {
        return new URLSearchParams(match[0].trim().split('#')[1] ?? '').get('bootstrap') ?? '';
      }
    }
    Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 200);
  }
  throw new Error('fixture bootstrap URL did not appear in /tmp/paw-webfixture.log');
}

let sessionCookie: { name: string; value: string } | null = null;

async function authenticate(page: Page): Promise<void> {
  if (sessionCookie) {
    await page.context().addCookies([{ ...sessionCookie, domain: '127.0.0.1', path: '/' }]);
    return;
  }
  // The HttpOnly session cookie is not visible to page JS and the
  // APIRequestContext does not share the browser's cookie jar, so the
  // Set-Cookie value is copied into the context explicitly.
  const response = await page.request.post('/api/auth/exchange', {
    data: { token: bootstrapToken() },
    headers: { 'Content-Type': 'application/json' }
  });
  if (!response.ok) throw new Error(`bootstrap exchange failed: ${response.status}`);
  const setCookie = response.headers()['set-cookie'] ?? '';
  await response.dispose();
  const [pair] = setCookie.split(';');
  const [name, value] = pair.split('=');
  if (!name || !value) throw new Error('fixture did not return a session cookie');
  sessionCookie = { name, value };
  await page.context().addCookies([{ ...sessionCookie, domain: '127.0.0.1', path: '/' }]);
}

async function openWorkbench(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto('/');
}

test('workbench lists the seeded session and replays its conversation', async ({ page }) => {
  await openWorkbench(page);
  await expect(page.getByText('工作区包含 README.md 与 internal/ 目录。')).toBeVisible({ timeout: 15_000 });
});

test('composer submits a message and streams the fixture response', async ({ page }) => {
  await openWorkbench(page);
  // 第一个快照在 bootstrap 后异步加载；等待消息输入框变为可写，
  // 说明会话快照（含正确 session_version）已就绪。
  await expect(page.getByLabel('消息')).toBeEditable({ timeout: 15_000 });
  const composer = page.getByLabel('消息');
  await composer.fill('hello fixture');
  await composer.press('Enter');
  // fixture runner 同步完成 turn；等待用户消息出现说明提交已生效。
  await expect(page.locator('article.message.assistant', { hasText: 'hello fixture' })).toBeVisible({ timeout: 15_000 });
  // 提交后立刻检查用户消息的 article 是否包含助手回复；fixture 是同步完成。
  // 提交后刷新由 App.refreshNow 触发；助手回复渲染到对话视图后断言。
  await expect(page.locator('.conversation-view', { hasText: 'fixture 回复：已收到消息 hello fixture' })).toBeVisible({ timeout: 15_000 });
  await expect(composer).toHaveValue('');
  await expect(composer).toHaveCSS('height', '36px');
});

test('composer expands for multiline drafts and collapses after clearing or sending', async ({ page }, testInfo) => {
  const pageErrors: string[] = [];
  page.on('pageerror', error => pageErrors.push(error.message));
  // The local fixture has no model controller; seed only the model-card API.
  await page.route('**/api/workspaces/*/model-options', route => route.fulfill({
    json: {
      active_model_id: 'fixture/local',
      models: [{ id: 'fixture/local', name: 'Local fixture', provider: 'fixture', source: 'configured', reasoning_capable: true, effort: 'high' }],
      effort_options: ['default', 'low', 'medium', 'high'],
    },
  }));
  await openWorkbench(page);
  const input = page.getByLabel('消息');
  await expect(input).toBeEditable({ timeout: 15_000 });
  await expect(input).toHaveAttribute('rows', '1');
  await expect(page.locator('.composer-hint')).toHaveCount(0);
  await expect(input).toHaveCSS('height', '36px');
  await expect(page.locator('.composer')).toHaveCSS('height', '54px');
  await expect(page.locator('.deck-peek')).toHaveText('Local fixture · 高');
  await page.screenshot({ path: testInfo.outputPath('composer-single-line.png'), animations: 'disabled' });

  await input.fill('第一行');
  await input.press('Shift+Enter');
  await input.pressSequentially('第二行');
  await expect(input).toHaveValue('第一行\n第二行');
  await expect(input).toHaveCSS('height', '61px');

  const draft = '请梳理本项目的执行层次。\n列出入口、运行时与工具之间的关系。\n保留关键代码路径，说明调用方向。';
  await input.fill(draft);
  await expect(input).toHaveCSS('height', '86px');
  await page.reload();
  await expect(input).toHaveValue(draft);
  await expect(input).toHaveCSS('height', '86px');
  await page.screenshot({ path: testInfo.outputPath('composer-multiline.png'), animations: 'disabled' });

  await input.fill('');
  await expect(input).toHaveCSS('height', '36px');
  await input.fill('compact composer\nfixture check');
  await expect(input).toHaveCSS('height', '61px');
  await input.press('Enter');
  await expect(input).toHaveValue('');
  await expect(input).toHaveCSS('height', '36px');
  await expect(page.locator('article.message.assistant', { hasText: 'compact composer' })).toBeVisible();
  expect(pageErrors).toEqual([]);
});

test('composer follows wrapped content on resize and caps overflow at 200px', async ({ page }, testInfo) => {
  const pageErrors: string[] = [];
  page.on('pageerror', error => pageErrors.push(error.message));
  await openWorkbench(page);
  const input = page.getByLabel('消息');
  await expect(input).toBeEditable({ timeout: 15_000 });
  await input.fill('请检查输入区域在窗口宽度变化后，是否能够根据内容自动折行并调整高度。'.repeat(3));
  const wideHeight = await input.evaluate(el => el.getBoundingClientRect().height);
  expect(wideHeight).toBeGreaterThan(36);
  expect(wideHeight).toBeLessThan(200);
  await page.setViewportSize({ width: 800, height: 900 });
  await expect.poll(() => input.evaluate(el => el.getBoundingClientRect().height)).toBeGreaterThan(wideHeight);
  await page.screenshot({ path: testInfo.outputPath('composer-narrow.png'), animations: 'disabled' });
  await page.setViewportSize({ width: 1440, height: 900 });
  await expect.poll(() => input.evaluate(el => el.getBoundingClientRect().height)).toBe(wideHeight);

  await input.fill(Array.from({ length: 20 }, (_, index) => `第 ${index + 1} 行：超出高度后在输入框内滚动。`).join('\n'));
  await expect(input).toHaveCSS('height', '200px');
  await expect(input).toHaveCSS('overflow-y', 'auto');
  expect(await input.evaluate(el => el.scrollHeight > el.clientHeight)).toBe(true);
  await input.press('ControlOrMeta+End');
  await expect.poll(() => input.evaluate(el => el.scrollTop)).toBeGreaterThan(0);
  await page.screenshot({ path: testInfo.outputPath('composer-capped.png'), animations: 'disabled' });
  await input.fill('');
  await expect(input).toHaveCSS('height', '36px');
  await page.setViewportSize({ width: 800, height: 900 });
  await expect(input).toHaveCSS('height', '36px');
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(input).toHaveCSS('height', '36px');
  expect(await input.evaluate(el => el.scrollHeight)).toBe(36);
  await page.screenshot({ path: testInfo.outputPath('composer-mobile.png'), animations: 'disabled' });
  expect(pageErrors).toEqual([]);
});
