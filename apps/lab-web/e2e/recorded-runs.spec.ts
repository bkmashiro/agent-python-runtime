import { expect, test } from '@playwright/test';

test('loads debugger surface without legacy experiments summary view', async ({ page }) => {
  await page.goto('/');

  await expect(page.getByText('Load v3 JSON')).toBeVisible();
  await expect(page.getByText('3 development runs', { exact: true })).toBeVisible();
  await expect(page.getByText('Showing 3 public development runs', { exact: true })).toBeVisible();
  await expect(page.locator('[data-testid="run-option"]')).toHaveCount(3);
  const guestCodePanel = page.getByRole('tabpanel', { name: 'Guest Python' });
  await expect(guestCodePanel).toContainText('Complete Python executed by the selected Guest run');
  await expect(guestCodePanel).toContainText('RECORDED DEVELOPMENT SOURCE · PUBLIC');
  await expect(guestCodePanel).toContainText('items =');

  await page.getByRole('tab', { name: 'Host recorder' }).click();
  const hostCodePanel = page.getByRole('tabpanel', { name: 'Host recorder' });
  await expect(hostCodePanel).toContainText(/complete runScenarioAllExecution function · \d+ lines/);
  await expect(hostCodePanel).toContainText('HOST RECORDER · NOT GUEST PYTHON');
  await expect(page.getByText('EXPERIMENTS', { exact: true })).toHaveCount(0);
  await expect(page.getByText('DIRECT RECORDS', { exact: true })).toHaveCount(0);
  await expect(page.getByText('Experiment runs', { exact: true })).toHaveCount(0);
});
