import { expect, test } from '@playwright/test';

test('switches runs and renders recorded multi-agent lanes', async ({ page }) => {
  await page.goto('/');
  const runSelector = page.getByTestId('run-select');
  const options = page.locator('[data-testid="run-option"][data-run-kind="recorded"]');
  await expect(options).toHaveCount(3);
  const secondValue = await options.nth(1).getAttribute('value');
  await runSelector.selectOption(secondValue as string);
  await expect(runSelector).toHaveValue(secondValue as string);
  await expect(page.locator('[data-agent-id="orchestrator"]')).toHaveCount(1);
  await expect(page.locator('[data-agent-id="researcher"]')).toHaveCount(1);
  await expect(page.locator('[data-agent-id="reviewer"]')).toHaveCount(1);
  await expect(page.locator('[data-agent-id="runtime"]')).toHaveCount(1);
  await expect(page.locator('[data-agent-id="runtime"]')).toContainText('Parent done + fan-out');
  await expect(page.locator('[data-agent-id="runtime"]')).toContainText('Join + Host checks');
  await expect(page.locator('[data-agent-id="runtime"]')).toContainText('Resume + finish');
  await expect(page.getByLabel('Execution phases')).toContainText('Fan-out');
  await expect(page.getByLabel('Execution phases')).toContainText('Join');
  await expect(page.locator('[data-agent-id="researcher"] .lane-label')).toContainText(/\d+–\d+ ms/);
  await page.getByRole('tab', { name: 'Trace tree' }).click();
  const tree = page.getByRole('tree', { name: 'Causal trace tree' });
  await expect(tree).toBeVisible();
  await expect(tree.getByText('researcher Python', { exact: true })).toBeVisible();
  await expect(tree.getByText('Run finished', { exact: true })).toBeVisible();
  await page.getByRole('tab', { name: 'Timeline' }).click();
});

test('links an agent span to Python and its workspace diff', async ({ page }) => {
  await page.goto('/');
  const researcher = page.getByRole('button', { name: 'researcher agent.execute' });
  await researcher.click();
  await expect(page.getByText('What happened')).toBeVisible();
  await expect(page.getByText('researcher ran its recorded Python task in a private workspace.')).toBeVisible();
  await expect(page.getByText('caused by orchestrator-python')).toBeVisible();
  const pythonPanel = page.getByRole('tabpanel', { name: 'Python' });
  await expect(pythonPanel).toContainText('researcher.py · lines 1–4 · researcher');
  await expect(pythonPanel.locator('.source-line-linked')).toHaveCount(4);
  await expect(pythonPanel).toContainText('RECORDED SOURCE LINK');
  await expect(pythonPanel).toContainText('Path("/workspace/researcher.txt")');
  const filesystem = page.getByRole('region', { name: 'Filesystem changes' });
  await expect(filesystem).toContainText('workspace-researcher');
  await expect(filesystem).toContainText('researcher.txt');
  await expect(filesystem).toContainText('added');
});
