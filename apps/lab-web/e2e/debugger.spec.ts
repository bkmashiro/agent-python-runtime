import { expect, test } from '@playwright/test';

test('debugger exposes dense workflow, code, IO, atoms, and filesystem', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByText('AGENT WORKFLOW TRACE')).toBeVisible();
  await expect(page.getByText('TOOL CALL').first()).toBeVisible();
  await expect(page.getByText('PYSOLATE ABI').first()).toBeVisible();
  await expect(page.getByText('WASI').first()).toBeVisible();
  await expect(page.locator('.trace-panel')).not.toContainText('SOURCE BOUND');
  await expect(page.locator('.trace-panel')).not.toContainText('VERIFIED RUN');
  await expect(page.getByText('sources.demo_catalog()').first()).toBeVisible();
  await expect(page.getByText('sources.benchmark_manifest()').first()).toBeVisible();
  await page.getByText('sources.demo_catalog()').first().click();
  await expect(page.getByText('Agent-generated Guest program')).toBeVisible();
  await page.getByRole('tab', { name: 'Input / output' }).click();
  await expect(page.getByText('INPUT / ARGUMENTS')).toBeVisible();
  await expect(page.getByText('OUTPUT / RESULT')).toBeVisible();
  await page.getByText('Path.write_text()').first().click();
  await expect(page.getByText('ranking.json').first()).toBeVisible();
});
