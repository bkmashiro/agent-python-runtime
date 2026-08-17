import { expect, test } from '@playwright/test';

test('shows eight mechanisms without research-interface noise', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Runtime mechanisms' })).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Mechanisms' }).getByRole('button')).toHaveCount(8);
  await expect(page.getByText('THREE SMALL PROGRAMS', { exact: false })).toHaveCount(0);
  await expect(page.getByText('REAL GUEST · VERIFIED', { exact: false })).toHaveCount(0);
  await expect(page.getByText('Claim boundary', { exact: false })).toHaveCount(0);
  await expect(page.getByText('Natural-cohort', { exact: false })).toHaveCount(0);
  await expect(page.getByText('0 / 36', { exact: false })).toHaveCount(0);
  await expect(page.locator('text=/sha256:[0-9a-f]{64}/')).toHaveCount(0);
});

test('makes source-prefix overlap visible', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Start the READ before generation finishes' })).toBeVisible();
  await expect(page.getByText('1.923×')).toBeVisible();
  const execution = page.getByRole('region', { name: 'Execution' });
  await expect(execution).toContainText('generate → execute');
  await expect(execution).toContainText('stream while generating');
});

test('inspects additional optimization mechanisms', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: /Pre-dispatch one proven READ/ }).click();
  await expect(page.getByText('1018 ms')).toBeVisible();

  await page.getByRole('button', { name: /Compute once, reuse twice/ }).click();
  await expect(page.getByText('retained hit', { exact: true }).first()).toBeVisible();

  await page.getByRole('button', { name: /Share the baseline, keep writes private/ }).click();
  await expect(page.getByText('384 MiB', { exact: true }).first()).toBeVisible();

  await page.getByRole('button', { name: /Page out memory while waiting/ }).click();
  await expect(page.getByText('0 MiB', { exact: true }).first()).toBeVisible();

  await page.getByRole('button', { name: /Release the Guest, resume fresh/ }).click();
  await expect(page.getByText('resume.fresh')).toBeVisible();
});

test('shows source mismatch as the single control', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: /Different source stays fresh/ }).click();
  await expect(page.getByRole('heading', { name: 'Different source stays fresh' })).toBeVisible();
  await expect(page.getByText('Unsafe reuse')).toBeVisible();
  await expect(page.getByText('fresh physical B', { exact: true }).first()).toBeVisible();
});

test('remains usable at narrow viewport', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/');
  await expect(page.getByRole('navigation', { name: 'Mechanisms' })).toBeVisible();
  await page.getByRole('button', { name: /Share the baseline, keep writes private/ }).click();
  await expect(page.getByRole('heading', { name: 'Share the baseline, keep writes private' })).toBeVisible();
  await expect(page.getByText('Python', { exact: true })).toBeVisible();
});
