import { test, expect } from '@playwright/test';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const repoRoot = fileURLToPath(new URL('../../../..', import.meta.url));
const visualDir = path.join(repoRoot, '.agent', 'visual');

test('captures desktop and narrow workspace screenshots', async ({ page }) => {
  await page.goto('/?view=debug');
  await expect(page.locator('.topbar-pipeline')).toBeVisible();
  await page.waitForTimeout(900);
  await page.screenshot({
    path: path.join(visualDir, 'token-tracer-desktop.png'),
  });

  await page.setViewportSize({ width: 760, height: 900 });
  await page.waitForTimeout(500);
  await page.screenshot({
    path: path.join(visualDir, 'token-tracer-narrow.png'),
  });
});

test('verifies the real three-project ledger and captures global views', async ({ page }) => {
  const url = process.env.PAW_GLOBAL_FIXTURE_URL;
  test.skip(!url, 'Run globalfixture and set PAW_GLOBAL_FIXTURE_URL for the real-process smoke.');
  await page.goto(url!);
  await expect(page.getByRole('heading', { name: '消耗概览', exact: true })).toBeVisible();
  await expect(page.locator('.total-anchor strong')).toHaveText('3,840');
  await expect(page.locator('.group-row')).toHaveCount(3);
  await page.screenshot({ path: path.join(visualDir, 'global-tracer-desktop.png') });
  await page.getByRole('button', { name: /paw-core.*1,280/ }).click();
  await expect(page.locator('.request-row')).toHaveCount(2);
  await expect(page.locator('.total-anchor strong')).toHaveText('1,280');
  await expect(page.locator('.summary-side > div').first()).toHaveText('1项目');
  await page.locator('.request-row').filter({ hasText: '750' }).click();
  const detail = page.getByRole('complementary', { name: '请求明细' });
  await expect(detail).toContainText('工具结果');
  await expect(detail).toContainText('工具参数');
  await expect(detail).toContainText('≈8');
  await page.screenshot({ path: path.join(visualDir, 'global-tracer-request.png'), animations: 'disabled' });
  await page.getByRole('button', { name: '关闭请求明细' }).click();
  await page.getByRole('button', { name: '概览', exact: true }).click();
  await page.getByRole('button', { name: '深色', exact: true }).click();
  await expect(page.locator('html')).toHaveAttribute('data-tracer-theme', 'dark');
  await page.screenshot({ path: path.join(visualDir, 'global-tracer-dark.png') });
  await page.setViewportSize({ width: 760, height: 900 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.getByRole('button', { name: '工具', exact: true }).click();
  await expect(page.locator('.tool-row').filter({ hasText: /^Read/ })).toContainText('24');
  await page.screenshot({ path: path.join(visualDir, 'global-tracer-narrow.png') });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole('button', { name: '请求', exact: true }).click();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.locator('.request-row').first().click();
  await expect(detail).toBeVisible();
  await expect(detail).toContainText('输入原子分解');
  await page.screenshot({ path: path.join(visualDir, 'global-tracer-mobile.png'), animations: 'disabled' });
});
