import { expect, test } from '@playwright/test';

test('opens directly on the mechanism debugger with dense run context', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Generate and pre-dispatch' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Campaign' })).toHaveCount(0);
  await expect(page.getByText('£118.40')).toBeVisible();
  await expect(page.getByText('£78.00')).toBeVisible();
  await expect(page.getByRole('complementary', { name: 'Execution event inspector' })).toBeVisible();
});

test('groups the complete provider and runtime trace and reuses one inspector', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Timeline' }).click();
  await expect(page.getByRole('heading', { name: 'Model source generation' })).toBeVisible();
  await expect(page.getByText('brighton', { exact: true }).first()).toBeVisible();
  await page.getByRole('button', { name: /model.output/ }).first().click();
  await page.getByRole('button', { name: 'Input / output' }).click();
  await expect(page.getByRole('complementary', { name: 'Execution event inspector' })).toContainText('deepseek-v4-flash');
  await page.getByRole('button', { name: 'source', exact: true }).click();
  await expect(page.getByRole('complementary', { name: 'Execution event inspector' })).toContainText('travel.weather("brighton")');
  await page.getByRole('button', { name: 'raw' }).click();
  await expect(page.getByRole('complementary', { name: 'Execution event inspector' })).toContainText('reasoning_content');
});

test('uses projector-owned event groups without silently truncating records', async ({ page }) => {
  await page.goto('/');
  const groupButton = page.getByRole('button', { name: /Generate and pre-dispatch/ });
  const count = Number((await groupButton.locator('small').innerText()).split(' ')[0]);
  const rows = page.locator('.debug-event-tree .event-actor-group button');
  await expect(rows).toHaveCount(count);
  await page.getByRole('button', { name: /Seal, then execute fresh/ }).click();
  await expect(page.getByRole('heading', { name: 'Seal, then execute fresh' })).toBeVisible();
});

test('switching groups resets the selected raw event to that group', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Timeline' }).click();
  await page.getByRole('button', { name: /Select, seal, and resume/ }).click();
  const first = page.locator('.debug-event-tree .event-actor-group button').first();
  await expect(first).toHaveClass(/active/);
  await expect(page.getByRole('complementary', { name: 'Execution event inspector' })).not.toContainText('actor-brighton');
});

test('remains usable at 390px without horizontal page overflow', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Generate and pre-dispatch' })).toBeVisible();
  const widths = await page.evaluate(() => ({ viewport: window.innerWidth, document: document.documentElement.scrollWidth }));
  expect(widths.document).toBeLessThanOrEqual(widths.viewport);
  await expect(page.getByRole('complementary', { name: 'Execution event inspector' })).toBeVisible();
});
