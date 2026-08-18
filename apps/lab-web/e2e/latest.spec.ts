import { expect, test } from '@playwright/test';

test('defaults to the unified campaign and closes the real candidate decision', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'One London day-trip campaign' })).toBeVisible();
  await expect(page.getByText('£118.40')).toBeVisible();
  await expect(page.getByText('£78.00').first()).toBeVisible();
  await expect(page.getByText('3 matched pairs')).toBeVisible();
  await expect(page.getByText(/These are phases of this run/)).toBeVisible();
});

test('shows mechanisms as phases of one campaign with typed records', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Mechanisms' }).click();
  const phases = page.getByRole('navigation', { name: 'Unified campaign phases' });
  await expect(phases.getByRole('button')).toHaveCount(6);
  await phases.getByRole('button', { name: /Select, seal, and resume/ }).click();
  await expect(page.getByRole('heading', { name: 'Select, seal, and resume' })).toBeVisible();
  await expect(page.getByText('TYPED EVENT EVIDENCE')).toBeVisible();
  await expect(page.getByText('capsule.export', { exact: true })).toBeVisible();
});

test('exposes the actual public typed timeline and raw event', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Timeline' }).click();
  await expect(page.getByRole('heading', { name: 'Execution timeline' })).toBeVisible();
  await expect(page.getByText(/no reconstructed provider messages/i)).toBeVisible();
  await expect(page.getByText('PUBLIC RAW EVENT')).toBeVisible();
  await page.getByRole('button', { name: 'oxford', exact: true }).click();
  await expect(page.locator('.event-list .event-row').first()).toBeVisible();
});

test('remains readable at 390px without horizontal page overflow', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'One London day-trip campaign' })).toBeVisible();
  await expect(page.getByText('£78.00').first()).toBeVisible();
  const widths = await page.evaluate(() => ({ viewport: window.innerWidth, document: document.documentElement.scrollWidth }));
  expect(widths.document).toBeLessThanOrEqual(widths.viewport);
});

test('uses only local assets and loads one pinned public projection', async ({ page }) => {
  const requests: string[] = [];
  page.on('request', (request) => requests.push(request.url()));
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'One London day-trip campaign' })).toBeVisible();
  expect(new Set(requests.filter((url) => url.includes('/lab-data/')))).toEqual(new Set(['http://127.0.0.1:4187/lab-data/unified-campaign.json']));
  expect(requests.some((url) => /fonts\.googleapis|fonts\.gstatic|unpkg|jsdelivr/.test(url))).toBe(false);
  await expect(page.getByLabel('Pysolate Lab home').locator('svg')).toHaveCount(1);
});
