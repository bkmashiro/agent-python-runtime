import { expect, test } from '@playwright/test';

test('loads debugger surface without legacy experiments summary view', async ({ page }) => {
  await page.goto('/');

  await expect(page.getByText('Load v2 JSON')).toBeVisible();
  await expect(page.getByText('3 all-on runs', { exact: true })).toBeVisible();
  await expect(page.getByText('Showing 3 all-on benchmark runs · 54 evidence rows retained', { exact: true })).toBeVisible();
  await expect(page.locator('[data-testid="run-option"]')).toHaveCount(3);
  const codePanel = page.getByRole('tabpanel', { name: 'Code' });
  await expect(codePanel).toContainText(/complete runScenarioAllExecution function · \d+ lines/);
  await expect(codePanel).toContainText('func runScenarioAllExecution');
  await expect(page.getByText('EXPERIMENTS', { exact: true })).toHaveCount(0);
  await expect(page.getByText('DIRECT RECORDS', { exact: true })).toHaveCount(0);
  await expect(page.getByText('Experiment runs', { exact: true })).toHaveCount(0);
});
