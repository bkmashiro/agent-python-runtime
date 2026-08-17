import { expect, test } from '@playwright/test';

test('defaults to the public day-trip Inspector and shows the evidence map', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'A Saturday day trip under £100' })).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Travel trace groups' }).getByRole('button')).toHaveCount(5);
  for (const label of ['Public input', 'Candidate Agents', 'Fresh Guest execution', 'Main Agent decision', 'Final output']) {
    await expect(page.getByRole('button', { name: new RegExp(label) })).toBeVisible();
  }
  await expect(page.getByText('Main Agent final output', { exact: true }).first()).toBeVisible();
  await expect(page.getByText('£78', { exact: true }).first()).toBeVisible();
  await expect(page.getByText('Oxford', { exact: true }).last()).toBeVisible();
});

test('keeps the public envelope and all reviewed evidence expandable', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Task summary' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Public input envelope' })).toBeVisible();
  await expect(page.getByText(/Do not make network requests or access credentials/)).toBeVisible();

  await page.getByText('3 skill bodies', { exact: true }).click();
  await expect(page.getByText('# Budget checking skill', { exact: false })).toBeVisible();
  await page.getByText('Tool contracts', { exact: true }).click();
  await expect(page.getByText('travel.weather', { exact: true }).first()).toBeVisible();
  await page.getByText('Workspace snapshot', { exact: true }).click();
  await expect(page.getByText('pysolate.day-trip-api-fixture.v1', { exact: true })).toBeVisible();
  await expect(page.getByText('Private fields withheld', { exact: true })).toBeVisible();
});

test('renders both candidate outputs, three waits per Guest, selection, and final itinerary', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Brighton model output' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Oxford model output' })).toBeVisible();
  await expect(page.getByText('Generated Python', { exact: true })).toHaveCount(2);
  await expect(page.getByText('3 API waits/results', { exact: true })).toHaveCount(2);
  await expect(page.locator('.wait-list').getByText('travel.weather', { exact: true })).toHaveCount(2);
  await expect(page.getByText('Workspace SHA', { exact: true })).toHaveCount(2);
  await expect(page.getByText('Selected', { exact: true })).toBeVisible();
  await expect(page.getByText('Discarded', { exact: true })).toBeVisible();
  await expect(page.getByText(/Oxford selected/)).toBeVisible();
  await expect(page.getByText(/Total cost GBP 78/)).toBeVisible();
});

test('marks the top-level historical timeline as legacy', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Timeline' }).click();
  await expect(page.getByRole('heading', { name: 'Execution timeline' })).toBeVisible();
  await expect(page.getByText(/LEGACY · HISTORICAL RELEASE RECORDING/)).toBeVisible();
  await expect(page.getByText(/not the day-trip execution trace/)).toBeVisible();
});

test('remains readable at 390px without horizontal page overflow', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'A Saturday day trip under £100' })).toBeVisible();
  await expect(page.getByText('£78', { exact: true }).first()).toBeVisible();
  const widths = await page.evaluate(() => ({ viewport: window.innerWidth, document: document.documentElement.scrollWidth }));
  expect(widths.document).toBeLessThanOrEqual(widths.viewport);
});

test('still exposes the existing mechanisms surface separately', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Mechanisms' }).click();
  await expect(page.getByRole('heading', { name: 'Runtime mechanisms' })).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Mechanisms' }).getByRole('button')).toHaveCount(8);
  await expect(page.getByLabel('Pysolate Lab home').locator('svg')).toHaveCount(1);
});
