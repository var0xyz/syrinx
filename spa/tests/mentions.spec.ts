import { test, expect, type Page } from '@playwright/test';

function randomUsername(prefix: string): string {
  return `${prefix}${Math.random().toString(36).substring(2, 10)}`;
}

async function signup(page: Page, username: string) {
  await page.goto('/signup');
  await expect(page.locator('h2')).toContainText('Sign up');
  await page.fill('#username', username);
  await page.fill('#email', `${username}@example.com`);
  await page.click('button.submit');
  await expect(page.locator('.progress-bar')).toBeVisible();
  await page.waitForURL('/welcome', { timeout: 30000 });
}

test.describe('Mentions', () => {
  test('composer @ picker inserts a mention link that navigates in-app with no confirmation', async ({ browser }) => {
    const targetUsername = randomUsername('mentiontarget');
    const authorUsername = randomUsername('mentionauthor');

    // First browser context/page: create the mention target account so it
    // exists for the search API before the author composes.
    const targetContext = await browser.newContext();
    const targetPage = await targetContext.newPage();
    await signup(targetPage, targetUsername);
    await targetContext.close();

    // Second context: the author, who mentions the target.
    const authorContext = await browser.newContext();
    const page = await authorContext.newPage();
    await signup(page, authorUsername);

    await page.goto('/reeds');
    await expect(page.locator('.reeds-container')).toBeVisible();
    await page.click('.floating-write-btn');
    await expect(page.locator('.write-modal')).toBeVisible();

    // Search on the full random suffix, not a short generic prefix — the
    // DB accumulates usernames across test runs, and a short/common prefix
    // like "mentio" can match dozens of leftover users from earlier runs,
    // pushing the real target past the search endpoint's result limit.
    const textarea = page.locator('.write-modal textarea');
    await textarea.click();
    await textarea.type(`Hey @${targetUsername}`);

    // Mention popover should show the target user as a match.
    const popover = page.locator('#mention-picker-popover');
    await expect(popover).toBeVisible({ timeout: 5000 });
    await expect(popover).toContainText(targetUsername);

    // Confirm via click (covers the "click/tap confirms" path).
    await page.locator('.mention-result', { hasText: targetUsername }).click();
    await expect(popover).not.toBeVisible();

    const content = await textarea.inputValue();
    // The '@' trigger is compose-time only — it's replaced entirely, not
    // carried into the signed content.
    expect(content).toContain(`[${targetUsername}](web+syrinx://users/`);
    expect(content).not.toContain('@' + targetUsername);

    await page.locator('.write-modal button[type="submit"]').click();
    await expect(page.locator('.write-modal')).not.toBeVisible();

    // Publish navigates straight to the reed detail page
    // (/reed/{userID}/{reedID}) — that's where MarkdownParser renders
    // without `preview`, so the mention is a real clickable <a>, not the
    // inert <span> the feed list uses for preview cards.
    await page.waitForURL(/\/reed\//, { timeout: 15000 });
    const mentionLink = page.locator('.reed-content a.inline-link', { hasText: targetUsername }).first();
    await expect(mentionLink).toBeVisible({ timeout: 15000 });

    await mentionLink.click();
    await expect(page.locator('.external-link-modal, [class*="external"]')).toHaveCount(0);
    await page.waitForURL(/\/profile\//, { timeout: 5000 });
    await expect(page.url()).toContain('/profile/');

    await authorContext.close();
  });

  test('typed text with no confirmed mention stays plain text on Escape', async ({ page }) => {
    const authorUsername = randomUsername('mentionesc');
    await signup(page, authorUsername);

    await page.goto('/reeds');
    await page.click('.floating-write-btn');
    await expect(page.locator('.write-modal')).toBeVisible();

    const textarea = page.locator('.write-modal textarea');
    await textarea.click();
    await textarea.type('Talking about @nonexistentuserxyz');

    const popover = page.locator('#mention-picker-popover');
    // Either no matches show, or the popover is visible with "No matches" —
    // either way Escape must cancel and leave raw text untouched.
    await page.keyboard.press('Escape');
    await expect(popover).not.toBeVisible();

    const content = await textarea.inputValue();
    expect(content).toBe('Talking about @nonexistentuserxyz');
    expect(content).not.toContain('web+syrinx');
  });
});
