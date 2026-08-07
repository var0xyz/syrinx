import { test, expect } from '@playwright/test';

async function mockServerInfo(
  page,
  signupMode: 'open' | 'invite' | 'closed',
  recoveryMode = false
) {
  await page.route('**/api/server/info', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        id: 'testsrv',
        name: 'Test',
        recoveryMode,
        signupMode,
        maxInvitesPerUser: 3,
      }),
    });
  });
}

test.describe('Signup mode gating', () => {
  test('open mode shows Sign Up on home', async ({ page }) => {
    await mockServerInfo(page, 'open');
    await page.goto('/');
    await expect(page.locator('a.btn', { hasText: 'Sign Up' })).toBeVisible();
    await expect(page.locator('a.btn', { hasText: 'Already a user' })).toBeVisible();
  });

  test('invite mode hides Sign Up on home', async ({ page }) => {
    await mockServerInfo(page, 'invite');
    await page.goto('/');
    await expect(page.locator('a.btn', { hasText: 'Already a user' })).toBeVisible();
    await expect(page.locator('a.btn', { hasText: 'Sign Up' })).toHaveCount(0);
  });

  test('closed mode hides Sign Up and blocks /signup form', async ({ page }) => {
    await mockServerInfo(page, 'closed');
    await page.goto('/');
    await expect(page.locator('a.btn', { hasText: 'Sign Up' })).toHaveCount(0);

    await page.goto('/signup');
    await expect(page.locator('text=This server is not accepting new signups.')).toBeVisible();
    await expect(page.locator('form')).toHaveCount(0);
  });

  test('invite mode blocks /signup without an invite link', async ({ page }) => {
    await mockServerInfo(page, 'invite');
    await page.goto('/signup');
    await expect(
      page.locator('text=You need a valid invite link to join this server.')
    ).toBeVisible();
    await expect(page.locator('form')).toHaveCount(0);
  });

  test('recovery mode hides Sign Up on home even when open', async ({ page }) => {
    await mockServerInfo(page, 'open', true);
    await page.goto('/');
    await expect(page.locator('a.btn', { hasText: 'Already a user' })).toBeVisible();
    await expect(page.locator('a.btn', { hasText: 'Sign Up' })).toHaveCount(0);
  });

  test('recovery mode blocks /preamble even when open', async ({ page }) => {
    await mockServerInfo(page, 'open', true);
    await page.goto('/preamble');
    await expect(page.locator('text=This server is rebuilding')).toBeVisible();
    await expect(page.locator('a', { hasText: 'I Understand, Continue to Sign Up' })).toHaveCount(0);
  });

  test('recovery mode blocks /signup even with a valid invite link', async ({ page }) => {
    await mockServerInfo(page, 'open', true);
    await page.route('**/api/invites/check**', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ valid: true }),
      });
    });
    await page.goto('/signup?iid=abcdefghijkl&uid=inviter1#test-secret-abc');
    await expect(page.locator('text=This server is rebuilding')).toBeVisible();
    await expect(page.locator('form')).toHaveCount(0);
  });

  test('invite query is preserved for signup payload wiring', async ({ page }) => {
    await mockServerInfo(page, 'invite');
    await page.route('**/api/invites/check**', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ valid: true }),
      });
    });
    await page.goto('/signup?iid=abcdefghijkl&uid=inviter1#test-secret-abc');
    await expect(page.locator('text=Signing up with an invite link.')).toBeVisible();
    await expect(page.locator('#username')).toBeVisible();
  });
});
