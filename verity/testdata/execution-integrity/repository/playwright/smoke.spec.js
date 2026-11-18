const { test, expect } = require('@playwright/test');

test('fixture browser smoke', async ({ page }) => {
  await page.setContent('<main><h1>Execution fixture</h1></main>');
  await expect(page.getByRole('heading', { name: 'Execution fixture' })).toBeVisible();
});
