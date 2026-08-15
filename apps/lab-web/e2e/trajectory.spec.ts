import { expect, test } from '@playwright/test';

test('opens the reset balanced-order real-Guest experiment by default', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByText('REAL GUEST EXPERIMENT')).toBeVisible();
  await expect(page.getByTestId('event-count')).toHaveText('243 events');
  await expect(page.getByLabel('Trajectory session')).toHaveValue('workflow-experiment-v1');
  await page.getByRole('button', { name: 'Raw assistant chunk' }).first().click();
  await expect(page.getByRole('complementary', { name: 'Event inspector' })).toContainText('reasoning-delta');
  await page.getByRole('button', { name: /workflowbench.execute_pair tool call/i }).first().click();
  await expect(page.getByRole('complementary', { name: 'Event inspector' })).toContainText('Source events');
  await expect(page.getByRole('region', { name: 'Linked execution' })).toContainText('runtime.event');
  await page.getByRole('button', { name: 'runtime', exact: true }).click();
  await expect(page.getByTestId('filtered-count')).toHaveText('76 shown');
});

test('inspects exact model context and every trajectory source', async ({ page }) => {
  await page.goto('/');
  await page.getByLabel('Trajectory session').selectOption('scripted-development');
  await expect(page.getByRole('heading', { name: 'Trajectory', exact: true })).toBeVisible();
  await expect(page.getByText('SCRIPTED DEVELOPMENT FIXTURE')).toBeVisible();
  await expect(page.getByTestId('event-count')).toHaveText(/28 events/);

  await page.getByRole('button', { name: /model request.*0002/i }).click();
  const context = page.getByRole('region', { name: 'Exact model context' });
  await expect(context.getByText('You are a development agent')).toBeVisible();
  await expect(context.getByText('Inspect the project status')).toBeVisible();
  await expect(context.getByText('workspace.status').first()).toBeVisible();
  await expect(context.getByText('The fixture includes system')).toBeVisible();

  await page.getByRole('button', { name: 'runtime', exact: true }).click();
  await expect(page.getByTestId('filtered-count')).toHaveText('2 shown');
  await expect(page.getByRole('complementary', { name: 'Event inspector' }).getByText('physical-workspace-status-0001').first()).toBeVisible();
});

test('follows a tool call through Runtime, workspace and result records', async ({ page }) => {
  await page.goto('/');
  await page.getByLabel('Trajectory session').selectOption('scripted-development');
  await page.getByRole('button', { name: /workspace.status tool call/i }).click();
  await expect(page.getByRole('region', { name: 'Linked execution' })).toContainText('runtime.event');
  await expect(page.getByRole('region', { name: 'Linked execution' })).toContainText('workspace.change');
  await expect(page.getByRole('region', { name: 'Linked execution' })).toContainText('tool.result');
  const linked = page.getByRole('region', { name: 'Linked execution' });
  await expect(linked.getByText('logical-workspace-status-0001').first()).toBeVisible();
  await expect(linked.getByText('physical-workspace-status-0001').first()).toBeVisible();
  await page.getByRole('tab', { name: 'Raw event' }).click();
  await expect(page.getByText('"tool_call_id"')).toBeVisible();
});

test('trajectory remains inspectable at narrow viewport', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/');
  await page.getByLabel('Trajectory session').selectOption('scripted-development');
  await expect(page.getByRole('heading', { name: 'Trajectory', exact: true })).toBeVisible();
  await page.getByRole('button', { name: /assistant output/i }).last().click();
  await expect(page.getByRole('complementary', { name: 'Event inspector' }).getByText('append-only trajectory contract is wired')).toBeVisible();
});
