import { expect, test } from '@playwright/test';

test('debugger inspects all recorded experiment runs and scrolls', async ({ page }) => {
  await page.goto('/');
  await page.getByText('Experiment runs', { exact: true }).click();
  await expect(page.getByText('54 recorded rows')).toBeVisible();
  await expect(page.getByText('DIRECT RECORDS ONLY')).toHaveCount(0);
  await expect(page.getByText('SELECTED EXPERIMENT')).toBeVisible();
  await expect(page.getByText('Expected result')).toBeVisible();
  await expect(page.getByText('public fixture · inspectable').first()).toBeVisible();
  await expect(page.getByRole('table').getByRole('row')).toHaveCount(55);
  const surface = page.locator('.experiments-view');
  await surface.evaluate((element) => element.scrollTo(0, element.scrollHeight));
  await expect.poll(() => surface.evaluate((element) => element.scrollTop)).toBeGreaterThan(0);
  await expect(page.getByRole('table').getByRole('row').last()).toBeVisible();
});
