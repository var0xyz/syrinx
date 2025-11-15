import { test, expect } from '@playwright/test';

test.describe('Signup and Reed Publishing Flow', () => {
  test('should signup user and publish a reed with emojis, newlines, and hashtags', async ({ page }) => {
    // Generate random username (1-32 characters)
    const usernameLength = Math.floor(Math.random() * 32) + 1;
    const username = Math.random().toString(36).substring(2, 2 + usernameLength);

    // Navigate to signup page
    await page.goto('/signup');

    // Wait for the page to load
    await expect(page.locator('h2')).toContainText('Sign up');

    // Fill in the username field
    await page.fill('#username', username);

    // Optionally fill in email (random email)
    const email = `${username}@example.com`;
    await page.fill('#email', email);

    // Submit the form
    await page.click('button.submit');

    // Wait for the progress bar to appear (indicates signup process started)
    await expect(page.locator('.progress-bar')).toBeVisible();

    // Wait for redirect to welcome page (signup successful)
    await page.waitForURL('/welcome', { timeout: 30000 });

    // Navigate to reeds page
    await page.goto('/reeds');

    // Wait for the page to load and verify we're on the reeds page
    await expect(page.locator('.reeds-container')).toBeVisible();

    // Click the floating write button to open the write section
    await page.click('.floating-write-btn');

    // Wait for the write section to appear
    await expect(page.locator('.write-section')).toBeVisible();

    // Fill in the reed content with emojis, newlines, and hashtags
    const reedContent = `Hello world! 🌟

This is a test reed with:
- Multiple lines
- Emojis 🎉 💡
- And #hashtags #testing

Testing special characters: @#$%^&*()_+-=[]{}|;:,.<>?`;

    await page.fill('#content', reedContent);

    // Verify the content was entered correctly
    await expect(page.locator('#content')).toHaveValue(reedContent);

    // Click the Publish button
    await page.click('button[type="submit"]');

    // Wait for the reed to be published (button should show "Publishing..." then disappear)
    await expect(page.locator('button[type="submit"]')).toHaveText('Publishing...');

    // Wait for the write section to close (indicates successful publish)
    await expect(page.locator('.write-section')).not.toBeVisible();

    // Verify the reed appears in the list
    await expect(page.locator('.reed-item')).toContainText(reedContent.split('\n')[0]); // First line of content

    // Verify the reed contains our username
    await expect(page.locator('.reed-item')).toContainText(username);

    // Verify the reed contains hashtags
    await expect(page.locator('.reed-item')).toContainText('#hashtags');
    await expect(page.locator('.reed-item')).toContainText('#testing');

    // Verify the reed contains emojis
    await expect(page.locator('.reed-item')).toContainText('🌟');
    await expect(page.locator('.reed-item')).toContainText('🎉');
    await expect(page.locator('.reed-item')).toContainText('💡');
  });
});
