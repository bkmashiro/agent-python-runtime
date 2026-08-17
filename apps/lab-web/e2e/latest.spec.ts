import { expect, test } from '@playwright/test';

test('makes source-prefix overlap visually obvious', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByText('REAL GUEST · VERIFIED')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Start the READ before the model finishes' })).toBeVisible();
  await expect(page.getByText('1.923×')).toBeVisible();
  await expect(page.getByText("record = slow.lookup('alpha')")).toBeVisible();
  const timeline = page.getByRole('region', { name: 'Measured execution timeline' });
  await expect(timeline).toContainText('generate → execute');
  await expect(timeline).toContainText('stream while generating');
  await expect(timeline.locator('.segment.effect')).toHaveCount(2);
});

test('shows one physical Guest for two exact logical requests', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: /Two agents, one physical Guest/ }).click();
  await expect(page.getByRole('heading', { name: 'Two agents, one physical Guest' })).toBeVisible();
  await expect(page.getByText('same sealed physical ID')).toBeVisible();
  await expect(page.getByText('exact_shared')).toBeVisible();
  await expect(page.getByText('2', { exact: true }).first()).toBeVisible();
  await expect(page.getByText('1', { exact: true }).first()).toBeVisible();
});

test('shows source mismatch as an explicit safe control', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: /Different source stays fresh/ }).click();
  await expect(page.getByText('safe fallback observed')).toBeVisible();
  await expect(page.getByText('reject_source_mismatch')).toBeVisible();
  await expect(page.getByText("result = {'square': pow(inputs['value'], 2)}")).toBeVisible();
  await expect(page.getByText('independent IDs')).toBeVisible();
});

test('remains usable at narrow viewport', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/');
  await expect(page.getByRole('navigation', { name: 'Optimization demos' })).toBeVisible();
  await page.getByRole('button', { name: /Two agents, one physical Guest/ }).click();
  await expect(page.getByRole('region', { name: 'Measured execution timeline' })).toBeVisible();
  await expect(page.getByText('Authored Python', { exact: true })).toBeVisible();
});
