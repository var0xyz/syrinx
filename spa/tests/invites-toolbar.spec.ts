import { test, expect } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');

test.describe('Invites management UI', () => {
  test('toolbar lists Invites between Feed and Profile', () => {
    const src = readFileSync(
      join(root, 'src/lib/components/BottomToolbar.svelte'),
      'utf8'
    );
    const feed = src.indexOf('href="/feeds"');
    const invites = src.indexOf('href="/invites"');
    const profile = src.indexOf('href="/profile"');
    expect(feed).toBeGreaterThan(-1);
    expect(invites).toBeGreaterThan(feed);
    expect(profile).toBeGreaterThan(invites);
    expect(src).toContain("currentPage === 'invites'");
  });

  test('invites route exists and profile card shows invitedBy', () => {
    const page = readFileSync(join(root, 'src/routes/invites/+page.svelte'), 'utf8');
    expect(page).toContain('createSignedInvite');
    expect(page).toContain('inviteShareURL');
    expect(page).toContain('No invites yet');
    expect(page).toContain('floating-create-btn');
    expect(page).toContain('Signups are closed');

    const card = readFileSync(
      join(root, 'src/lib/components/UserProfileCard.svelte'),
      'utf8'
    );
    expect(card).toContain('user?.invitedBy');
    expect(card).toContain('Invited by');
  });

  test('/invites hits auth gate', async ({ page }) => {
    await page.goto('/invites');
    await expect(page.getByRole('heading', { name: 'Sign up' })).toBeVisible({
      timeout: 10000,
    });
  });
});
