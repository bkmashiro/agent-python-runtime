import { expect, test } from '@playwright/test';

test('loads the multi-agent debugger without Host-recorder or legacy views', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByText('Load v4 JSON')).toBeVisible();
  await expect(page.getByText('3 development runs', { exact: true })).toBeVisible();
  await expect(page.getByText('Showing 3 public development runs', { exact: true })).toBeVisible();
  await expect(page.locator('[data-testid="run-option"]')).toHaveCount(3);
  await expect(page.getByRole('region', { name: 'Agent execution timeline' })).toBeVisible();
  await expect(page.getByRole('tab', { name: 'Python' })).toBeVisible();
  await expect(page.getByRole('tab', { name: 'Host recorder' })).toHaveCount(0);
  await expect(page.getByText('EXPERIMENTS', { exact: true })).toHaveCount(0);
  await expect(page.getByText('DIRECT RECORDS', { exact: true })).toHaveCount(0);
});
