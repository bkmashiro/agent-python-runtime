import { expect, test } from '@playwright/test';

test('opens directly on the mechanism debugger with dense run context', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Code + tool calls' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Campaign' })).toHaveCount(0);
  await expect(page.getByText('£118.40')).toBeVisible();
  await expect(page.getByText('£78.00')).toBeVisible();
  await expect(page.getByRole('complementary', { name: 'Execution event inspector' })).toBeVisible();
});

test('groups the complete provider and runtime trace and reuses one inspector', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Timeline' }).click();
  await page.getByRole('button', { name: /^Model generation/ }).click();
  await expect(page.getByRole('heading', { name: 'Model generation' })).toBeVisible();
  await expect(page.getByText('brighton', { exact: true }).first()).toBeVisible();
  await page.getByRole('button', { name: /model.output/ }).first().click();
  await page.getByRole('button', { name: 'Input / output' }).click();
  await expect(page.getByRole('complementary', { name: 'Execution event inspector' })).toContainText('deepseek-v4-flash');
  await page.getByRole('button', { name: 'Python', exact: true }).click();
  await expect(page.getByRole('complementary', { name: 'Execution event inspector' })).toContainText('travel.weather("brighton")');
  await page.getByRole('button', { name: 'Raw event' }).click();
  await expect(page.getByRole('complementary', { name: 'Execution event inspector' })).toContainText('reasoning_content');
});

test('uses projector-owned event groups without silently truncating records', async ({ page }) => {
  await page.goto('/');
  const groupButton = page.getByRole('button', { name: /^Code \+ tool calls/ });
  const count = Number((await groupButton.locator('small').innerText()).split(' ')[0]);
  const rows = page.locator('.debug-event-tree .event-row-button');
  await expect(rows).toHaveCount(count);
  await groupButton.click();
  await page.getByRole('button', { name: /Seal, then execute fresh/ }).click();
  await expect(page.getByRole('heading', { name: 'Seal, then execute fresh' })).toBeVisible();
});

test('combines filter chips and provides an explicit tool-call view', async ({ page }) => {
  await page.goto('/');
  const code = page.getByRole('button', { name: /^Code \+ tool calls/ });
  const tools = page.getByRole('button', { name: /^Tool calls/ });
  await tools.click();
  await expect(page.getByRole('heading', { name: 'Combined evidence filters' })).toBeVisible();
  await expect(code).toHaveAttribute('aria-pressed', 'true');
  await expect(tools).toHaveAttribute('aria-pressed', 'true');
  await code.click();
  await expect(page.getByRole('heading', { name: 'Tool calls' })).toBeVisible();
  await expect(page.getByRole('button', { name: /source\.statement\.complete/ })).toHaveCount(0);
  await expect(page.getByRole('button', { name: /travel\.weather/ }).first()).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Evidence filters' })).not.toContainText('00');
});

test('shows the incremental source prefix at every statement completion', async ({ page }) => {
  await page.goto('/');
  const statements = page.getByRole('button', { name: /source\.statement\.complete · oxford/ });
  await statements.nth(0).click();
  await page.getByRole('button', { name: 'Python' }).click();
  const inspector = page.getByRole('complementary', { name: 'Execution event inspector' });
  await expect(inspector).toContainText('prefix 1/6');
  await expect(inspector).toContainText('weather = travel.weather("oxford")');
  await expect(inspector).not.toContainText('rail = travel.rail("oxford"');
  await expect(inspector.locator('.source-line-delta')).toHaveCount(1);
  await statements.nth(1).click();
  await expect(inspector).toContainText('prefix 2/6');
  await expect(inspector).toContainText('rail = travel.rail("oxford", travellers=2)');
  await expect(inspector.locator('.source-line-delta')).toHaveCount(1);
});

test('binds semantic qualification to the triggering source prefix', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: /semantic\.qualified · host · travel\.weather/ }).first().click();
  await page.getByRole('button', { name: 'Python' }).click();
  const inspector = page.getByRole('complementary', { name: 'Execution event inspector' });
  await expect(inspector).toContainText('qualifying source prefix 1/6');
  await expect(inspector).toContainText('weather = travel.weather("oxford")');
  await expect(inspector).not.toContainText('rail = travel.rail("oxford"');
});

test('renders the complete role-lane timeline and retains the evidence workbench', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Timeline' }).click();
  const map = page.getByRole('region', { name: 'Semantic causal lane timeline' });
  await expect(page.getByRole('heading', { name: 'Fan-out, spans, and convergence' })).toBeVisible();
  await expect(map.locator('.lane-axis')).toHaveCount(4);
  await expect(map.locator('.lane-span')).toHaveCount(13);
  await expect(map.locator('.span-request')).toHaveCount(6);
  await expect(map.locator('.span-request rect')).toHaveCount(6);
  await expect(map.locator('.span-request circle')).toHaveCount(12);
  await expect(map.locator('.lane-node')).toHaveCount(0);
  await expect(map.locator('.lane-transition')).toHaveCount(18);
  await expect(map.locator('.lane-reuse')).toHaveCount(6);
  const layout = await page.evaluate(() => ({ viewport: innerWidth, document: document.documentElement.scrollWidth, laneScroll: (document.querySelector('.lane-map-scroll') as HTMLElement)?.scrollLeft ?? 0 }));
  expect(layout.document).toBeLessThanOrEqual(layout.viewport);
  if (layout.viewport <= 620) expect(layout.laneScroll).toBeGreaterThan(0);
  await map.locator('.span-request').first().press('Enter');
  await expect(map.locator('.lane-selection')).toContainText('physical request start → finish');
  await expect(page.getByRole('navigation', { name: 'Evidence filters' })).toBeVisible();
  await expect(page.getByRole('complementary', { name: 'Execution event inspector' })).toBeVisible();
});

test('supports collapsible trace branches and Chrome-style filtering without stale inspection', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'By role' }).click();
  const oxford = page.getByRole('button', { name: /oxford \d+/ });
  await oxford.click();
  await expect(oxford).toHaveAttribute('aria-expanded', 'false');
  await expect(oxford.locator('xpath=..').locator('.event-row-button')).toHaveCount(0);
  await page.getByRole('textbox', { name: 'Filter trace events' }).fill('request.start');
  await expect(page.locator('.trace-count')).toHaveText('7/56');
  await expect(page.getByRole('complementary', { name: 'Execution event inspector' })).toContainText('request.start');
  await page.getByRole('button', { name: 'By time' }).click();
  await expect(page.locator('.actor-tree-row')).toHaveCount(0);
  await expect(page.locator('.event-row-button')).toHaveCount(7);
});

test('switching groups resets the selected raw event to that group', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Timeline' }).click();
  await page.getByRole('button', { name: /Select, seal, and resume/ }).click();
  const first = page.locator('.debug-event-tree .event-row-button').first();
  await expect(first).toHaveClass(/active/);
  await expect(page.getByRole('complementary', { name: 'Execution event inspector' })).not.toContainText('actor-brighton');
});

test('remains usable at 390px without horizontal page overflow', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Code + tool calls' })).toBeVisible();
  const widths = await page.evaluate(() => ({ viewport: window.innerWidth, document: document.documentElement.scrollWidth }));
  expect(widths.document).toBeLessThanOrEqual(widths.viewport);
  await expect(page.getByRole('complementary', { name: 'Execution event inspector' })).toBeVisible();
});
