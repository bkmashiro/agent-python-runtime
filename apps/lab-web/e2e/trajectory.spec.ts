import { expect, test } from '@playwright/test';

test('opens the human-first real Guest causal tree by default', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByText('REAL GUEST · PUBLIC')).toBeVisible();
  await expect(page.getByLabel('Evidence view')).toHaveValue('experiment-full-public');
  await expect(page.getByRole('heading', { name: /Trace the program/ })).toHaveCount(0);
  await expect(page.getByRole('navigation', { name: 'Session causal tree' })).toContainText('Child preparation');
  await expect(page.getByRole('heading', { name: 'tools.increment · source bound' })).toBeVisible();
  await page.getByRole('tab', { name: 'Code' }).click();
  await expect(page.getByText('Source body omitted from portable projection')).toBeVisible();
  await expect(page.getByText('L1:9–L1:27')).toBeVisible();
  await expect(page.getByText('not recorded', { exact: true })).toBeVisible();
});

test('switches to the strict production ledger without experiment groups', async ({ page }) => {
  await page.goto('/');
  await page.getByLabel('Evidence view').selectOption('production-rollback');
  await expect(page.getByText('PRODUCTION LEDGER')).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Session causal tree' })).toContainText('9 canonical records');
  await expect(page.getByRole('navigation', { name: 'Session causal tree' })).not.toContainText('Child preparation');
  await page.getByRole('button', { name: /Capability · Host effect/ }).click();
  await page.getByRole('tab', { name: 'Raw' }).click();
  await expect(page.getByRole('complementary', { name: 'Causal inspector' })).toContainText('rcpt_ef0ed8e340af2c53');
});

test('inspects child workspace identities without inventing file bodies', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: /Child preparation/ }).click();
  await page.getByRole('tab', { name: 'Workspace' }).click();
  const inspector = page.getByRole('complementary', { name: 'Causal inspector' });
  await expect(inspector).toContainText('base root sha256');
  await expect(inspector).toContainText('result root sha256');
  await expect(inspector).toContainText('selected');
  await expect(inspector).toContainText('Before/after file bodies are absent, not empty');
});

test('relations, timeline and evidence remain inspectable at narrow viewport', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/');
  await expect(page.getByRole('navigation', { name: 'Session causal tree' })).toBeVisible();
  await page.getByRole('button', { name: /tools.increment · source bound/ }).click();
  await page.getByRole('tab', { name: 'Timeline' }).click();
  await expect(page.getByText('Every mark is a recorded point; no duration is inferred.')).toBeVisible();
  await page.getByRole('tab', { name: 'Evidence' }).click();
  await expect(page.getByText('Canonical evidence identities')).toBeVisible();
});
