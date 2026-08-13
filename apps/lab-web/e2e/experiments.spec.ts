import { expect, test } from '@playwright/test';

test('experiments view renders only directly recorded rows', async ({ page }) => {
  await page.goto('/');
  await page.getByText('Experiments', { exact: true }).click();
  await expect(page.getByText('DIRECT RECORDS ONLY')).toBeVisible();
  await expect(page.getByText('54 RECORDED ROWS')).toBeVisible();
  await expect(page.getByText('gpt-5.3-codex-spark')).toBeVisible();
  await expect(page.getByRole('table').getByRole('row')).toHaveCount(55);
  await expect(page.getByText('rejected').first()).toBeVisible();
  await expect(page.getByText('expected_base_conflict').first()).toBeVisible();
});
