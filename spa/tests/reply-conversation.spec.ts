import { test, expect } from '@playwright/test';

test.describe('Reply conversation flow', () => {
  test('should show a reply in the parent conversation section', async ({ page }) => {
    const usernameLength = Math.floor(Math.random() * 32) + 1;
    const username = Math.random().toString(36).substring(2, 2 + usernameLength);

    await page.goto('/signup');
    await expect(page.locator('h2')).toContainText('Sign up');
    await page.fill('#username', username);
    await page.fill('#email', `${username}@example.com`);
    await page.click('button.submit');
    await page.waitForURL('/welcome', { timeout: 30000 });

    await page.goto('/reeds');
    await expect(page.locator('.reeds-container')).toBeVisible();
    await page.click('.floating-write-btn');
    await expect(page.locator('.write-modal')).toBeVisible();

    const rootContent = 'Root reed for reply test';
    await page.locator('.write-modal textarea').fill(rootContent);
    await page.locator('.write-modal button[type="submit"]').click();
    await expect(page.locator('.write-modal')).not.toBeVisible({ timeout: 30000 });
    await expect(page.locator('.reed-item')).toContainText(rootContent);

    await page.locator('.reed-item').first().click();
    await expect(page.locator('.reed-detail')).toBeVisible();
    await expect(page.locator('.conversation-section')).not.toBeVisible();

    await page.getByRole('button', { name: 'Reply' }).click();
    await expect(page.locator('.write-modal')).toBeVisible();

    const replyContent = 'Direct reply content';
    await page.locator('.write-modal textarea').fill(replyContent);
    await page.locator('.write-modal button[type="submit"]').click();
    await expect(page.locator('.write-modal')).not.toBeVisible({ timeout: 30000 });
    await expect(page.locator('.reed-detail')).toBeVisible();
    await expect(page.locator('.reed-body')).toContainText(replyContent);

    await page.goBack();
    await expect(page.locator('.reed-detail')).toBeVisible();
    await expect(page.locator('.conversation-section')).toContainText(replyContent, { timeout: 30000 });

    await page.locator('.reply-row').first().click();
    await expect(page.locator('.reed-body')).toContainText(replyContent);
    await expect(page.locator('.quote-container')).toBeVisible();
  });
});
