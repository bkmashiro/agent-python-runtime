import { expect, test } from '@playwright/test';

test('opens the body-safe real-Guest experiment projection by default', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByText('REAL GUEST · PUBLIC EXPERIMENT')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Causal evidence' })).toBeVisible();
  await expect(page.getByTestId('event-count')).toHaveText('15 events');
  await expect(page.getByLabel('Trajectory session')).toHaveValue('experiment-full-public');
  await page.getByRole('button', { name: 'source decision' }).click();
  await page.getByRole('tab', { name: 'Raw event' }).click();
  await expect(page.getByRole('complementary', { name: 'Event inspector' })).toContainText('source_bound');
});

test('switches to the strict production projection without experiment records', async ({ page }) => {
  await page.goto('/');
  await page.getByLabel('Trajectory session').selectOption('production-rollback');
  await expect(page.getByText('PRODUCTION ROLLBACK PROJECTION', { exact: true })).toBeVisible();
  await expect(page.getByTestId('event-count')).toHaveText('6 events');
  await expect(page.getByRole('button', { name: 'subagent runtime' })).toHaveCount(0);
  await page.getByRole('button', { name: 'effect transition' }).click();
  await page.getByRole('tab', { name: 'Raw event' }).click();
  await expect(page.getByRole('complementary', { name: 'Event inspector' })).toContainText('rcpt_ef0ed8e340af2c53');
});

test('inspects fresh child and workspace identities in the public experiment view', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'subagent', exact: true }).click();
  await expect(page.getByTestId('filtered-count')).toHaveText('3 shown');
  await page.getByRole('button', { name: 'subagent workspace' }).click();
  await page.getByRole('tab', { name: 'Raw event' }).click();
  const inspector = page.getByRole('complementary', { name: 'Event inspector' });
  await expect(inspector).toContainText('base_root_sha256');
  await expect(inspector).toContainText('result_root_sha256');
  await expect(inspector).toContainText('selected');
});

test('causal evidence remains inspectable at narrow viewport', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Causal evidence' })).toBeVisible();
  await page.getByRole('button', { name: 'source decision' }).click();
  await expect(page.getByRole('complementary', { name: 'Event inspector' })).toContainText('sealed');
});
