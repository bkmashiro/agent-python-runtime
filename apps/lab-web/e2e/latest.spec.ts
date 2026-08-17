import { expect, test } from '@playwright/test';

test('shows eight mechanisms without research-interface noise', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Runtime mechanisms' })).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Mechanisms' }).getByRole('button')).toHaveCount(8);
  await expect(page.getByText('THREE SMALL PROGRAMS', { exact: false })).toHaveCount(0);
  await expect(page.getByText('REAL GUEST · VERIFIED', { exact: false })).toHaveCount(0);
  await expect(page.getByText('Claim boundary', { exact: false })).toHaveCount(0);
  await expect(page.getByText('Natural-cohort', { exact: false })).toHaveCount(0);
  await expect(page.getByText('0 / 36', { exact: false })).toHaveCount(0);
  await expect(page.locator('text=/sha256:[0-9a-f]{64}/')).toHaveCount(0);
});

test('makes source-prefix overlap visible', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Start the READ before generation finishes' })).toBeVisible();
  await expect(page.getByText('1.923×')).toBeVisible();
  const execution = page.getByRole('region', { name: 'Execution' });
  await expect(execution).toContainText('generate → execute');
  await expect(execution).toContainText('stream while generating');
  await expect(execution.getByText('finalize', { exact: true })).toHaveCount(2);
});

test('inspects additional optimization mechanisms', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: /Pre-dispatch one proven READ/ }).click();
  await expect(page.getByText('1018 ms')).toBeVisible();

  await page.getByRole('button', { name: /Compute once, reuse twice/ }).click();
  await expect(page.getByText('retained hit', { exact: true }).first()).toBeVisible();

  await page.getByRole('button', { name: /Share the baseline, keep writes private/ }).click();
  await expect(page.getByText('384 MiB', { exact: true }).first()).toBeVisible();

  await page.getByRole('button', { name: /Page out memory while waiting/ }).click();
  await expect(page.getByText('0 MiB', { exact: true }).first()).toBeVisible();

  await page.getByRole('button', { name: /Release the Guest, resume fresh/ }).click();
  await expect(page.getByText('fresh Guest', { exact: true })).toBeVisible();
});

test('shows source mismatch as the single control', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: /Different source stays fresh/ }).click();
  await expect(page.getByRole('heading', { name: 'Different source stays fresh' })).toBeVisible();
  await expect(page.getByText('Unsafe reuse')).toBeVisible();
  await expect(page.getByText('fresh physical B', { exact: true }).first()).toBeVisible();
});

test('keeps code mono, restores the icon and contains timeline geometry', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByLabel('Pysolate Lab home').locator('svg')).toHaveCount(1);
  const font = await page.locator('.code-row code').first().evaluate((element) => getComputedStyle(element).fontFamily);
  expect(font.toLowerCase()).toContain('ui-monospace');
  expect(font).not.toContain('DM Mono');

  const contained = await page.locator('.timeline-lane').evaluateAll((lanes) => lanes.every((lane) => {
    const track = lane.querySelector('.lane-track')!.getBoundingClientRect();
    return [...lane.querySelectorAll('.segment')].every((segment) => segment.getBoundingClientRect().right <= track.right + 0.5);
  }));
  expect(contained).toBe(true);

  await page.getByRole('button', { name: /Share the baseline, keep writes private/ }).click();
  const aligned = await page.locator('.state-flow').evaluate((flow) => {
    const nodes = [...flow.querySelectorAll('.state-node')].map((node) => node.getBoundingClientRect());
    const arrows = [...flow.querySelectorAll('.state-arrow')].map((arrow) => arrow.getBoundingClientRect());
    const narrow = window.innerWidth <= 620;
    return arrows.every((arrow, index) => {
      if (narrow) {
        const centerX = arrow.left + arrow.width / 2;
        const between = arrow.top + arrow.height / 2;
        return Math.abs(centerX - (nodes[index].left + nodes[index].width / 2)) < 1 && between >= nodes[index].bottom && between <= nodes[index + 1].top;
      }
      return Math.abs((arrow.top + arrow.height / 2) - (nodes[index].top + nodes[index].height / 2)) < 1;
    });
  });
  expect(aligned).toBe(true);
});

test('inspects one real workspace task across timeline, trace, source and workspace', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Task Inspector' }).click();
  await expect(page.getByRole('heading', { name: 'Summarize a development workspace' })).toBeVisible();
  await expect(page.getByRole('button', { name: /Research workspace shape/ })).toBeVisible();
  await page.getByRole('tab', { name: 'Trace tree' }).click();
  await expect(page.getByRole('button', { name: /Start parallel analyses/ })).toBeVisible();
  await page.getByRole('navigation', { name: 'Inspector tabs' }).getByRole('button', { name: 'Workspace', exact: true }).click();
  await expect(page.getByText('researcher.txt')).toBeVisible();
  await expect(page.getByText('reviewer.txt')).toBeVisible();
  await expect(page.getByText(/oracle .*passed/i)).toHaveCount(0);
});

test('remains usable at narrow viewport', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/');
  await expect(page.getByRole('navigation', { name: 'Mechanisms' })).toBeVisible();
  await page.getByRole('button', { name: /Share the baseline, keep writes private/ }).click();
  await expect(page.getByRole('heading', { name: 'Share the baseline, keep writes private' })).toBeVisible();
  await expect(page.getByText('Python', { exact: true })).toBeVisible();
});
