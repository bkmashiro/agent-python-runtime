import { expect, test } from '@playwright/test';

test('switches between recorded runs and updates trace content', async ({ page }) => {
  await page.goto('/');

  const runSelector = page.getByTestId('run-select');
  const recordedOption = page.locator('[data-testid="run-option"][data-run-kind="recorded"]');
  await expect(recordedOption).toHaveCount(3);

  const firstValue = await recordedOption.nth(0).getAttribute('value');
  const secondValue = await recordedOption.nth(1).getAttribute('value');
  expect(firstValue).not.toBeNull();
  expect(secondValue).not.toBeNull();

  await runSelector.selectOption(firstValue as string);
  await expect(runSelector).toHaveValue(firstValue as string);

  await runSelector.selectOption(secondValue as string);
  await expect(runSelector).toHaveValue(secondValue as string);
  await expect(page.locator('[data-testid="trace-node"][data-node-kind="group"]')).toHaveCount(11);
  await expect(page.locator('[data-testid="trace-node"][data-node-kind="event"]')).toHaveCount(0);
});

test('reports all recorded runs with trace payload and supports trace scroll', async ({ page }) => {
  await page.goto('/');

  const recordedRuns = page.locator('[data-testid="run-option"][data-run-kind="recorded"]');
  const runCount = await recordedRuns.count();
  await expect(runCount).toBeGreaterThan(0);

  for (let index = 0; index < runCount; index += 1) {
    const option = recordedRuns.nth(index);
    const nodeCount = Number(await option.getAttribute('data-node-count'));
    expect(nodeCount).toBeGreaterThanOrEqual(2);
  }

  const longest = await recordedRuns.evaluateAll((options) => options.reduce((best, option) => Number(option.getAttribute('data-node-count')) > best.count ? { value: (option as HTMLOptionElement).value, count: Number(option.getAttribute('data-node-count')) } : best, { value: '', count: -1 }));
  expect(longest.count).toBeGreaterThan(10);
  await page.getByTestId('run-select').selectOption(longest.value);
  await page.getByRole('button', { name: 'Expand all' }).click();

  const traceScroller = page.locator('.trace-scroll');
  await expect(traceScroller).toBeVisible();
  const dimensions = await traceScroller.evaluate((element) => ({
    scrollHeight: element.scrollHeight,
    clientHeight: element.clientHeight,
  }));
  expect(dimensions.scrollHeight).toBeGreaterThan(dimensions.clientHeight);

  const before = await traceScroller.evaluate((element) => element.scrollTop);
  await traceScroller.evaluate((element) => element.scrollTo(0, element.scrollHeight));
  const after = await traceScroller.evaluate((element) => element.scrollTop);
  expect(after).toBeGreaterThan(before);
});
