import { readFileSync } from 'node:fs';
import { test, expect, type Browser, type Page } from '@playwright/test';

declare const process: any;

// Needs a fresh server with a minted root: its public key, root's identity
// export and the export's passphrase.
const serverKeyFile = process.env.SYRINX_SERVER_KEY_FILE;
const rootBundle = process.env.SYRINX_ROOT_BUNDLE;
const rootPassphrase = process.env.SYRINX_ROOT_PASSPHRASE;

test.skip(!serverKeyFile || !rootBundle || !rootPassphrase, 'needs SYRINX_SERVER_KEY_FILE, SYRINX_ROOT_BUNDLE and SYRINX_ROOT_PASSPHRASE');
if (process.env.SYRINX_E2E_BASE_URL) test.use({ baseURL: process.env.SYRINX_E2E_BASE_URL });
test.describe.configure({ mode: 'serial' });

function uniqueName(prefix: string): string {
  return `${prefix}${Math.random().toString(36).slice(2, 8)}`;
}

/** A fresh page that already trusts the server's key. */
async function newPage(browser: Browser): Promise<Page> {
  const page = await (await browser.newContext()).newPage();
  if (process.env.SYRINX_E2E_CONSOLE) {
    page.on('console', (m) => m.type() === 'error' && console.log(`[browser] ${m.text()}`));
  }
  await page.goto('/');
  const gate = page.getByText('Server public key');
  if (await gate.waitFor({ timeout: 10000 }).then(() => true, () => false)) {
    await page.locator('textarea').fill(readFileSync(serverKeyFile, 'utf8'));
    await page.getByRole('button', { name: 'Continue' }).click();
    await expect(gate).toHaveCount(0, { timeout: 15000 });
  }
  return page;
}

async function userID(page: Page): Promise<string> {
  return (await page.evaluate(() => localStorage.getItem('userId'))) ?? '';
}

async function importRoot(page: Page): Promise<void> {
  await page.goto('/import');
  await page.getByRole('tab', { name: 'I only have my keys' }).click();
  await page.setInputFiles('#backup-file', rootBundle);
  await page.fill('#import-password', rootPassphrase);
  await page.locator('.actions .btn-primary').click();
  await page.waitForURL('**/reeds', { timeout: 60000 });
}

async function signup(page: Page, url: string, username: string): Promise<void> {
  await page.goto(url);
  const preamble = page.getByRole('button', { name: 'I understand, continue to sign up' });
  if (await preamble.waitFor({ timeout: 10000 }).then(() => true, () => false)) await preamble.click();
  await page.fill('#username', username);
  await page.click('button.submit');
  await page.waitForURL('**/backup', { timeout: 60000 });
  // The mandatory key backup isn't what this test is about.
  await page.evaluate(() => localStorage.setItem('lastKeyBackupAt', String(Date.now())));
}

/** Creates an invite from /invites and returns its link. An admin invite
 * grants every permission the inviter holds. */
async function createInvite(page: Page, role: 'user' | 'admin' | null): Promise<string> {
  await page.goto('/invites');
  if (role === 'admin') await expect(page.locator('.desktop-create .btn')).toBeEnabled();
  await page.locator('.desktop-create .btn').click();
  // The role choice only shows to someone who may grant admin.
  const roleModal = page.locator('.role-modal');
  const asked = await roleModal.waitFor({ timeout: role === 'admin' ? 15000 : 3000 }).then(() => true, () => false);
  if (asked) {
    await page.locator('.role-option', { hasText: role === 'admin' ? 'Admin' : 'User' }).click();
    await page.locator('.role-modal .btn.primary').click();
    if (role === 'admin') {
      await page.locator('.picker .option.all input').check();
      await page.locator('.role-modal .btn.primary').click();
    }
  }
  const link = await page.locator('code.share-url').textContent({ timeout: 30000 });
  await page.getByRole('button', { name: 'Done' }).click();
  return link ?? '';
}

async function publish(page: Page, content: string): Promise<void> {
  await page.goto('/reeds');
  await page.getByRole('button', { name: 'New Reed' }).click();
  await page.locator('.write-modal textarea').fill(content);
  await page.locator('.write-modal button[type="submit"]').click();
  await expect(page.locator('.write-modal')).not.toBeVisible({ timeout: 30000 });
}

/** How many reeds by authorID the page holds locally. */
async function heldReedsBy(page: Page, authorID: string): Promise<number> {
  return page.evaluate(async (author) => {
    const db: IDBDatabase = await new Promise((resolve, reject) => {
      const req = indexedDB.open('Syrinx');
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => reject(req.error);
    });
    const all: any[] = await new Promise((resolve, reject) => {
      const req = db.transaction(['reeds'], 'readonly').objectStore('reeds').getAll();
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => reject(req.error);
    });
    return all.filter((r) => r.userID === author).length;
  }, authorID);
}

test('a kick of a user and two invitees, opened by one admin and approved by root', async ({ browser }) => {
  test.setTimeout(5 * 60 * 1000);
  const kickReason = `Scam links in every pipe ${uniqueName('')}`;

  const root = await newPage(browser);
  await importRoot(root);

  const admin = await newPage(browser);
  await signup(admin, await createInvite(root, 'admin'), uniqueName('admin'));

  const target = await newPage(browser);
  const targetName = uniqueName('target');
  await signup(target, await createInvite(admin, 'user'), targetName);

  const invitees: Page[] = [];
  const inviteeNames: string[] = [];
  for (let i = 0; i < 2; i++) {
    const page = await newPage(browser);
    const name = uniqueName('invitee');
    await signup(page, await createInvite(target, null), name);
    invitees.push(page);
    inviteeNames.push(name);
  }

  const follower = await newPage(browser);
  await signup(follower, '/signup', uniqueName('follower'));
  const targetID = await userID(target);
  const reedText = `Buy coins ${uniqueName('')}`;
  await follower.goto(`/profile/${targetID}`);
  await follower.locator('.profile-actions .action-btn', { hasText: 'Follow' }).click();
  await publish(target, reedText);
  await expect.poll(() => heldReedsBy(follower, targetID), { timeout: 60000 }).toBeGreaterThan(0);

  // A member doesn't see the admin section.
  await follower.goto('/reeds');
  await expect(follower.locator('nav', { hasText: 'Invites' }).first()).toBeVisible();
  await expect(follower.getByRole('link', { name: 'Admin' })).toHaveCount(0);

  // The admin opens a kick of the target and both invitees.
  await admin.goto(`/profile/${targetID}`);
  await admin.getByRole('button', { name: 'Admin actions' }).click();
  await admin.locator('.action-row', { hasText: 'Kick' }).click();
  for (const name of inviteeNames) {
    await admin.locator('.tree-node', { hasText: name }).locator('input').check();
  }
  await admin.locator('.field textarea').fill(kickReason);
  await expect(admin.locator('.warning')).toContainText('Kick 3 accounts');
  await admin.getByRole('button', { name: 'Sign and request' }).click();
  await expect(admin.getByText('Request opened')).toBeVisible({ timeout: 30000 });

  // The requester can only withdraw; root can approve.
  await admin.goto('/admin/actions');
  const own = admin.locator('.request', { hasText: kickReason });
  await expect(own.getByRole('button', { name: 'Withdraw' })).toBeVisible();
  await expect(own.getByRole('button', { name: 'Approve' })).toHaveCount(0);

  await root.goto('/admin/actions');
  const pending = root.locator('.request', { hasText: kickReason });
  await expect(pending.getByRole('button', { name: 'Approve' })).toBeEnabled({ timeout: 30000 });
  await pending.getByRole('button', { name: 'Approve' }).click();
  await root.getByRole('button', { name: 'Sign and approve' }).click();
  await expect(root.locator('.request', { hasText: kickReason }).locator('.state-badge')).toHaveText(
    'Executed',
    { timeout: 30000 }
  );

  // The ledger records both steps, and the follower drops the kicked reeds.
  await root.goto('/ledger');
  await expect(root.locator('.entry', { hasText: `Kick. Reason: ${kickReason}` })).toBeVisible({ timeout: 30000 });
  await expect(root.locator('.entry', { hasText: '] Approved' }).first()).toBeVisible();
  await expect.poll(() => heldReedsBy(follower, targetID), { timeout: 60000 }).toBe(0);
});

test('a lost device gets a new key from two admins and finishes recovery', async ({ browser }, testInfo) => {
  test.setTimeout(5 * 60 * 1000);
  const rebindReason = `Lost their phone, checked in person ${uniqueName('')}`;

  const root = await newPage(browser);
  await importRoot(root);
  const admin = await newPage(browser);
  await signup(admin, await createInvite(root, 'admin'), uniqueName('admin'));

  const member = await newPage(browser);
  const memberName = uniqueName('member');
  await signup(member, '/signup', memberName);
  const memberID = await userID(member);

  // The new device mints a key and downloads it, without signing in.
  const device = await newPage(browser);
  await device.goto('/lost-device');
  await device.fill('.field input', memberID);
  await device.getByRole('button', { name: 'Create a new key' }).click();
  const [download] = await Promise.all([
    device.waitForEvent('download'),
    device.getByRole('button', { name: /^Download / }).click(),
  ]);
  expect(download.suggestedFilename()).toBe(`${memberID}.pub.txt.gz`);
  const keyFile = testInfo.outputPath(download.suggestedFilename());
  await download.saveAs(keyFile);

  // Not bound yet: finishing has to wait for the admins.
  await device.getByRole('button', { name: 'Finish recovery' }).click();
  await expect(device.getByText('Not approved yet')).toBeVisible({ timeout: 30000 });

  await admin.goto(`/profile/${memberID}`);
  await admin.getByRole('button', { name: 'Admin actions' }).click();
  await admin.locator('.action-row', { hasText: 'Add a key' }).click();
  await admin.setInputFiles('input[type="file"]', keyFile);
  await expect(admin.locator('.fingerprint code')).not.toBeEmpty();
  await admin.locator('.field textarea').fill(rebindReason);
  await admin.getByRole('button', { name: 'Sign and request' }).click();
  await expect(admin.getByText('Request opened')).toBeVisible({ timeout: 30000 });

  await root.goto('/admin/actions');
  const pending = root.locator('.request', { hasText: rebindReason });
  await pending.getByRole('button', { name: 'Approve' }).click();
  await root.getByRole('button', { name: 'Sign and approve' }).click();
  await expect(pending.locator('.state-badge')).toHaveText('Executed', { timeout: 30000 });

  // The device rotates away from the restricted key and is signed in.
  await device.getByRole('button', { name: 'Finish recovery' }).click();
  await device.waitForURL('**/backup', { timeout: 60000 });
  expect(await userID(device)).toBe(memberID);
  await device.evaluate(() => localStorage.setItem('lastKeyBackupAt', String(Date.now())));
  await device.goto('/reeds');
  await expect(device.getByText(memberName).first()).toBeVisible({ timeout: 30000 });
});

test('flags, a suspension and a broadcast', async ({ browser }) => {
  test.setTimeout(5 * 60 * 1000);
  const suspendReason = `Rude in every thread ${uniqueName('')}`;
  const broadcastText = `Maintenance tonight ${uniqueName('')}`;
  const flagNote = `Scam link ${uniqueName('')}`;

  const root = await newPage(browser);
  await importRoot(root);
  const admin = await newPage(browser);
  await signup(admin, await createInvite(root, 'admin'), uniqueName('admin'));

  const author = await newPage(browser);
  await signup(author, '/signup', uniqueName('author'));
  const authorID = await userID(author);
  const reedText = `Free coins ${uniqueName('')}`;
  await publish(author, reedText);

  // A member flags the author's reed; the menu then says it's done.
  const reader = await newPage(browser);
  await signup(reader, '/signup', uniqueName('reader'));
  await reader.goto(`/profile/${authorID}`);
  const reed = reader.locator('.reed-item', { hasText: reedText });
  await expect(reed).toBeVisible({ timeout: 60000 });
  await reed.getByRole('button', { name: 'More options' }).click();
  await reader.getByRole('menuitem', { name: 'Flag' }).click();
  await reader.locator('.modal textarea').fill(flagNote);
  await reader.locator('.modal').getByRole('button', { name: 'Flag', exact: true }).click();
  await expect(reader.getByText('Flagged. The admins will take a look.')).toBeVisible({ timeout: 30000 });
  await reader.getByRole('button', { name: 'Done' }).click();
  await reed.getByRole('button', { name: 'More options' }).click();
  await expect(reader.getByRole('menuitem', { name: 'Already flagged' })).toBeDisabled();
  await reader.keyboard.press('Escape');

  // The admin sees the flag with its note and dismisses it.
  await admin.goto('/admin/actions');
  await admin.getByRole('radio', { name: 'Flagged' }).or(admin.getByText('Flagged', { exact: true })).first().click();
  const flagged = admin.locator('.flag', { hasText: flagNote });
  await expect(flagged).toBeVisible({ timeout: 30000 });
  await flagged.getByRole('button', { name: 'Dismiss' }).click();
  await expect(flagged).toHaveCount(0);

  // A suspension, once root approves, locks the author out.
  await admin.goto(`/profile/${authorID}`);
  await admin.getByRole('button', { name: 'Admin actions' }).click();
  await admin.locator('.action-row', { hasText: 'Suspend' }).click();
  await admin.locator('.field textarea').fill(suspendReason);
  await admin.getByRole('button', { name: 'Sign and request' }).click();
  await expect(admin.getByText('Request opened')).toBeVisible({ timeout: 30000 });
  await root.goto('/admin/actions');
  const suspension = root.locator('.request', { hasText: suspendReason });
  await suspension.getByRole('button', { name: 'Approve' }).click();
  await root.getByRole('button', { name: 'Sign and approve' }).click();
  await expect(suspension.locator('.state-badge').first()).toHaveText('Executed', { timeout: 30000 });
  await author.goto('/invites');
  await expect(author.getByText('Your account is suspended')).toBeVisible({ timeout: 30000 });
  await expect(author.getByText(suspendReason)).toBeVisible();

  // A broadcast, once root approves, reaches members' mailboxes.
  await admin.goto('/admin/broadcast');
  await admin.locator('.field textarea').fill(broadcastText);
  await admin.getByRole('button', { name: 'Send for approval' }).click();
  await expect(admin.locator('.sent', { hasText: broadcastText })).toBeVisible({ timeout: 30000 });
  await root.goto('/admin/actions');
  const broadcast = root.locator('.request', { hasText: broadcastText });
  await broadcast.getByRole('button', { name: 'Approve' }).click();
  await root.getByRole('button', { name: 'Sign and approve' }).click();
  await expect(broadcast.locator('.state-badge').first()).toHaveText('Executed', { timeout: 30000 });
  await reader.goto('/reeds');
  await reader.getByRole('button', { name: 'Mailbox' }).click();
  await expect(reader.getByText(broadcastText)).toBeVisible({ timeout: 30000 });
});
