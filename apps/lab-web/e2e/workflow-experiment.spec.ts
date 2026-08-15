import { expect, test } from '@playwright/test';

test('sealed workflow experiment renders paired provenance and rejected near matches', async ({ page }) => {
  const errors: string[] = [];
  page.on('console', (message) => { if (message.type() === 'error') errors.push(message.text()); });
  page.on('pageerror', (error) => errors.push(error.message));
  await page.goto('/');
  await expect(page.getByTestId('workflow-experiment')).toBeVisible();
  await expect(page.getByText('25 → 23', { exact: true })).toBeVisible();
  await expect(page.getByText('0', { exact: true }).first()).toBeVisible();
  await expect(page.locator('.workflow-arrivals button')).toHaveCount(14);

  await page.locator('.workflow-arrivals button', { hasText: 'coalesced' }).click();
  await expect(page.locator('.workflow-task-metrics')).toContainText('physical 2 → 1');
  await expect(page.locator('.workflow-treatment[data-treatment="optimized"]')).toContainText('admitted · coalesced');
  await expect(page.locator('.provenance-chain')).toContainText('2 logical requests');

  await page.locator('.workflow-arrivals button', { hasText: 'authority mismatch' }).click();
  await expect(page.locator('.workflow-task-metrics')).toContainText('1 rejected');
  await expect(page.locator('.workflow-treatment[data-treatment="optimized"]')).toContainText('rejected · reused · authority_mismatch');
  await expect(page.getByText('no prompt, output body, Python source or private reasoning')).toBeVisible();
  expect(errors).toEqual([]);
});

test('workflow experiment remains usable at narrow viewport', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/');
  await expect(page.getByTestId('workflow-experiment')).toBeVisible();
  await expect(page.locator('.workflow-pair')).toHaveCSS('grid-template-columns', /[0-9.]+px/);
  await page.locator('.workflow-arrivals button', { hasText: 'retained reuse' }).click();
  await expect(page.locator('.workflow-task-metrics')).toContainText('physical 2 → 1');
  const width = await page.evaluate(() => ({ scroll: document.documentElement.scrollWidth, client: document.documentElement.clientWidth }));
  expect(width.scroll).toBeLessThanOrEqual(width.client + 1);
});
