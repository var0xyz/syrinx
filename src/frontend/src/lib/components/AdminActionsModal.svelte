<script lang="ts">
  import { MAX_LONG_TEXT_CHARS, countCharacters, longTextFits } from '$lib/utils/text';
  import { createEventDispatcher, onDestroy } from 'svelte';
  import Username from '$lib/components/Username.svelte';
  import PermissionPicker from '$lib/components/PermissionPicker.svelte';
  import { apiService } from '$lib/services/api';
  import { cryptoService } from '$lib/services/crypto';
  import { myPermissions, openAdminRequest, type AdminAction } from '$lib/services/admin';
  import { readRebindFile } from '$lib/services/rebind';
  import { isOnline } from '$lib/services/pwa';
  import { formatPermissionSet } from '$lib/utils/permissions';
  import type * as api from '$lib/types/api';

  export let open = false;
  export let userID: string;

  const dispatch = createEventDispatcher<{ close: void }>();

  // The page behind stays put while the modal is open; the modal scrolls.
  $: if (typeof document !== 'undefined') document.documentElement.style.overflow = open ? 'hidden' : '';
  onDestroy(() => {
    if (typeof document !== 'undefined') document.documentElement.style.overflow = '';
  });

  interface ActionItem {
    id: AdminAction;
    icon: string;
    label: string;
    hint: string;
    danger?: boolean;
  }

  let viewer: api.UserPermissions | null = null;
  let target: api.UserPermissions | null = null;
  let tree: api.InviteTreeNode[] = [];
  let loadError = '';
  let loadedFor = '';
  // Nothing renders until the server answers: admin actions are online only.
  let loading = true;

  let action: AdminAction | null = null;
  let checked = new Set<string>();
  let reason = '';
  let message = '';
  let granted = new Set<string>();
  let keyArmor = '';
  let keyFingerprint = '';
  let keyError = '';
  let submitting = false;
  let submitError = '';
  let done = false;

  $: if (open && userID && $isOnline && loadedFor !== userID) void load(userID);

  async function load(id: string): Promise<void> {
    loadedFor = id;
    loadError = '';
    loading = true;
    viewer = null;
    target = null;
    tree = [];
    try {
      const [v, t, nodes] = await Promise.all([
        myPermissions(),
        apiService.getUserPermissions(id),
        apiService.getInviteTree(id).then((r) => r.nodes),
      ]);
      if (id !== loadedFor) return;
      [viewer, target, tree] = [v, t, nodes];
    } catch (err) {
      if (id !== loadedFor) return;
      loadError = err instanceof Error ? err.message : 'Could not load this user.';
    }
    loading = false;
  }

  $: held = new Set(viewer?.permissions ?? []);
  $: viewerIsRoot = viewer?.role === 'root';
  $: self = tree.find((n) => n.userId === userID);
  $: suspended = !!self?.suspended;
  $: targetIsAdmin = target?.role === 'admin';

  $: actions = [
    ...(!targetIsAdmin && held.has('admin.create')
      ? [{ id: 'promote', icon: 'promote', label: 'Promote to admin', hint: 'Give admin rights and permissions' }]
      : []),
    ...(targetIsAdmin && (held.has('admin.create') || held.has('admin.demote'))
      ? [{ id: 'permissions', icon: 'admin', label: 'Change permissions', hint: 'Grant or take away permissions' }]
      : []),
    ...(targetIsAdmin && held.has('admin.demote')
      ? [{ id: 'demote', icon: 'demote', label: 'Demote to user', hint: 'Take away admin rights' }]
      : []),
    ...(held.has('user.rebind')
      ? [{ id: 'rebind', icon: 'key', label: 'Add a key', hint: 'For a member who lost their device and has no backup' }]
      : []),
    ...(held.has('message.unicast')
      ? [{ id: 'unicast', icon: 'mail', label: 'Send a private message', hint: 'Delivered to their mailbox only' }]
      : []),
    ...(held.has('user.suspend')
      ? [
          suspended
            ? { id: 'reinstate', icon: 'reinstate', label: 'Reinstate', hint: 'Lift the suspension' }
            : { id: 'suspend', icon: 'suspend', label: 'Suspend', hint: 'Block sign-in until reinstated; nothing is deleted' },
        ]
      : []),
    ...(held.has('user.kick')
      ? [{ id: 'kick', icon: 'kick', label: 'Kick', hint: 'Remove the account and all its content', danger: true }]
      : []),
  ] as ActionItem[];

  $: chosen = actions.find((a) => a.id === action);
  $: picksTargets = action === 'suspend' || action === 'kick';
  $: picksPermissions = action === 'promote' || action === 'permissions';
  $: needsReason = action !== 'unicast';
  $: reasonOk = reason.trim().length > 0 && longTextFits(reason);
  $: canSubmit =
    !submitting &&
    (action === 'unicast'
      ? message.trim().length > 0 && longTextFits(message)
      : reasonOk && (!picksTargets || checked.size > 0) && (action !== 'rebind' || !!keyArmor));

  /** Whether the viewer may pick node for the chosen action. */
  function selectable(n: api.InviteTreeNode): boolean {
    if (n.role === 'root' || n.removed) return false;
    if (action === 'suspend' && n.suspended) return false;
    return n.role !== 'admin' || viewerIsRoot;
  }

  function choose(id: AdminAction): void {
    action = id;
    checked = new Set([userID]);
    reason = '';
    message = '';
    keyArmor = '';
    keyFingerprint = '';
    keyError = '';
    submitError = '';
    granted = new Set(id === 'permissions' ? (target?.permissions ?? []) : []);
  }

  function toggle(id: string): void {
    const next = new Set(checked);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    checked = next;
  }

  function close(): void {
    action = null;
    done = false;
    loadedFor = '';
    dispatch('close');
  }

  async function onFile(e: Event): Promise<void> {
    keyArmor = '';
    keyFingerprint = '';
    keyError = '';
    const file = (e.currentTarget as HTMLInputElement).files?.[0];
    if (!file) return;
    try {
      const armor = await readRebindFile(file, userID);
      const fingerprint = await cryptoService.fingerprintOf(armor);
      keyArmor = armor;
      keyFingerprint = (fingerprint.toUpperCase().match(/.{1,4}/g) ?? []).join(' ');
    } catch (err) {
      keyError = err instanceof Error ? err.message : 'That isn’t a key file.';
    }
  }

  async function submit(): Promise<void> {
    if (!action || !canSubmit) return;
    submitting = true;
    submitError = '';
    try {
      const targets = picksTargets ? [...checked] : [userID];
      const body =
        action === 'unicast' ? message.trim() : action === 'rebind' ? keyArmor : picksPermissions ? formatPermissionSet(granted) : '';
      await openAdminRequest(action, targets, needsReason ? reason.trim() : '', body);
      done = true;
    } catch (err) {
      submitError = err instanceof Error ? err.message : 'The request was refused.';
    } finally {
      submitting = false;
    }
  }

  $: kickSummary =
    checked.size === 1
      ? 'Kick 1 account and all its content. This can’t be undone.'
      : `Kick ${checked.size} accounts and all their content. This can’t be undone.`;
</script>

{#if open}
  <div class="overlay" on:click={close} role="presentation"></div>
  <div class="modal" role="dialog" aria-modal="true" aria-labelledby="admin-actions-title">
    {#if done}
      <h3 id="admin-actions-title">Request opened</h3>
      <div class="actions">
        {#if action === 'unicast'}
          <a class="btn btn-secondary" href="/admin/message" on:click={close}>View in Message</a>
        {:else}
          <a class="btn btn-secondary" href="/admin/actions" on:click={close}>View in Actions</a>
        {/if}
        <button class="btn btn-primary" type="button" on:click={close}>Done</button>
      </div>
    {:else if !action}
      <h3 id="admin-actions-title">Admin actions</h3>
      <p class="who"><span class="name"><Username {userID} linked={false} color="var(--primary)" /></span></p>
      {#if loadError}
        <p class="warning" role="alert">{loadError}</p>
      {:else if loading && !$isOnline}
        <p class="warning" role="alert">You’re offline. Admin actions need a connection to the server.</p>
      {:else if loading}
        <p class="loading">Loading…</p>
      {:else}
        <ul class="action-list">
          <li>
            <a class="action-row" href="/admin/flags/{userID}" on:click={close}>
              <span class="action-icon" style="--icon: url('/icons/flag-16.svg')" aria-hidden="true"></span>
              <span class="action-text">
                <span class="action-label">Flags received</span>
                <span class="action-hint">Every flag their reeds received</span>
              </span>
            </a>
          </li>
          {#each actions as a (a.id)}
            <li>
              <button type="button" class="action-row" class:danger={a.danger} on:click={() => choose(a.id)}>
                <span class="action-icon" style="--icon: url('/icons/{a.icon}-16.svg')" aria-hidden="true"></span>
                <span class="action-text">
                  <span class="action-label">{a.label}</span>
                  <span class="action-hint">{a.hint}</span>
                </span>
              </button>
            </li>
          {/each}
        </ul>
        {#if actions.length === 0}
          <p class="who">You don’t hold a permission for any action on this user.</p>
        {/if}
      {/if}
      <div class="actions">
        <button class="btn btn-secondary" type="button" on:click={close}>Close</button>
      </div>
    {:else}
      <h3 id="admin-actions-title" class="form-title" class:danger={chosen?.danger}>
        <span class="action-icon" style="--icon: url('/icons/{chosen?.icon}-16.svg')" aria-hidden="true"></span>
        {chosen?.label}
      </h3>
      <p class="who"><span class="name"><Username {userID} linked={false} color="var(--primary)" /></span></p>

      {#if picksTargets}
        <ul class="tree">
          {#each tree as n (n.userId)}
            <li style="padding-left: {n.depth * 1.25}rem">
              <label class="tree-node" class:locked={!selectable(n)}>
                {#if selectable(n)}
                  <input type="checkbox" checked={checked.has(n.userId)} on:change={() => toggle(n.userId)} />
                {:else}
                  <span class="lock-icon" aria-label="Can't be selected"></span>
                {/if}
                <span class="name"><Username userID={n.userId} username={n.username || undefined} linked={false} color="var(--primary)" /></span>
                {#if n.role !== 'user'}<span class="node-role">{n.role}</span>{/if}
                {#if n.suspended}<span class="node-role">suspended</span>{/if}
                {#if n.removed}<span class="node-role">removed</span>{/if}
              </label>
            </li>
          {/each}
        </ul>
      {/if}

      {#if picksPermissions}
        <PermissionPicker available={viewer?.permissions ?? []} bind:selected={granted} />
      {/if}

      {#if action === 'rebind'}
        <label class="field">
          <span>Public key file</span>
          <input type="file" accept=".txt" on:change={onFile} />
        </label>
        {#if keyError}<p class="warning">{keyError}</p>{/if}
        {#if keyFingerprint}
          <div class="fingerprint">
            <span>Read this back to them</span>
            <code>{keyFingerprint}</code>
          </div>
        {/if}
      {/if}

      {#if action === 'unicast'}
        <label class="field">
          <span>Message</span>
          <textarea bind:value={message} rows="4"></textarea>
          <span class="character-counter">{countCharacters(message)}/{MAX_LONG_TEXT_CHARS} characters</span>
        </label>
      {:else}
        <label class="field">
          <span>Reason</span>
          <textarea bind:value={reason} rows="3"></textarea>
          <span class="character-counter">{countCharacters(reason)}/{MAX_LONG_TEXT_CHARS} characters</span>
        </label>
      {/if}

      {#if action === 'kick' && checked.size > 0}
        <p class="warning">{kickSummary}</p>
      {/if}
      {#if submitError}<p class="warning" role="alert">{submitError}</p>{/if}

      <div class="actions">
        <button class="btn btn-secondary" type="button" on:click={() => (action = null)}>Back</button>
        <button
          class="btn"
          class:btn-danger={action === 'kick'}
          class:btn-primary={action !== 'kick'}
          type="button"
          disabled={!canSubmit}
          on:click={submit}
        >
          {submitting ? 'Requesting…' : 'Request'}
        </button>
      </div>
    {/if}
  </div>
{/if}

<style>
  .overlay {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.5);
    z-index: 2000;
  }

  .modal {
    position: fixed;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    z-index: 2001;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 1.5rem;
    width: min(440px, calc(100vw - 2rem));
    max-height: calc(100dvh - 2rem);
    overflow-y: auto;
    overscroll-behavior: contain;
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
    text-align: left;
  }

  /* Overflow the modal instead of squeezing a clipped list inside it. */
  .modal > * {
    flex-shrink: 0;
  }

  h3 {
    margin: 0;
    font-size: 1.1rem;
    color: var(--fg);
  }

  .loading {
    margin: 0;
    font-size: 0.9rem;
    color: var(--muted);
  }

  .who {
    margin: -0.5rem 0 0;
    font-size: 0.85rem;
    color: var(--muted);
    word-break: break-all;
  }

  .action-list,
  .tree {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
  }

  .action-list {
    border: 1px solid var(--border);
    border-radius: 8px;
    overflow: hidden;
  }

  .action-list li + li {
    border-top: 1px solid var(--border);
  }

  .action-row {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    width: 100%;
    padding: 0.75rem 1rem;
    border: none;
    background: transparent;
    color: var(--fg);
    font-family: inherit;
    text-align: left;
    text-decoration: none;
    cursor: pointer;
  }

  .action-text {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    min-width: 0;
  }

  .action-icon {
    display: inline-block;
    width: 1.25rem;
    height: 1.25rem;
    flex-shrink: 0;
    background-color: currentColor;
    -webkit-mask: var(--icon) center / contain no-repeat;
    mask: var(--icon) center / contain no-repeat;
  }

  .action-row .action-icon {
    color: var(--muted);
  }

  .action-row.danger .action-icon,
  .form-title.danger {
    color: var(--error);
  }

  .form-title {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .action-row:hover {
    background: var(--input-bg);
  }

  .action-label {
    font-weight: 600;
    font-size: 0.9rem;
  }

  .action-row.danger .action-label {
    color: var(--error);
  }

  .action-hint {
    font-size: 0.8rem;
    color: var(--muted);
  }

  .tree {
    gap: 0.25rem;
    padding: 0.5rem 0.75rem;
    border: 1px solid var(--border);
    border-radius: 8px;
  }

  .tree-node {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.9rem;
    color: var(--fg);
    cursor: pointer;
  }

  .tree-node input {
    width: auto;
    margin: 0;
    padding: 0;
    flex-shrink: 0;
  }

  .tree-node.locked {
    color: var(--muted);
    cursor: default;
  }

  .name {
    color: var(--primary);
    font-weight: 600;
  }

  .tree-node.locked .name {
    color: var(--muted);
  }

  .lock-icon {
    display: inline-block;
    width: 1rem;
    height: 1rem;
    background-color: currentColor;
    -webkit-mask: url('/icons/lock-16.svg') center / contain no-repeat;
    mask: url('/icons/lock-16.svg') center / contain no-repeat;
  }

  .node-role {
    font-size: 0.75rem;
    font-weight: 600;
    padding: 0 0.5rem;
    border-radius: 999px;
    background: var(--input-bg);
    color: var(--muted);
  }

  .character-counter {
    text-align: right;
    font-size: 0.8rem;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    font-size: 0.85rem;
    color: var(--muted);
  }

  .field textarea {
    padding: 0.5rem;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--input-bg);
    color: var(--fg);
    font-family: inherit;
    font-size: 0.9rem;
    resize: vertical;
  }

  .fingerprint {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    font-size: 0.85rem;
    color: var(--muted);
  }

  .fingerprint code {
    color: var(--fg);
    overflow-wrap: anywhere;
  }

  .warning {
    margin: 0;
    font-size: 0.85rem;
    color: var(--error);
  }

  .actions {
    display: flex;
    gap: 0.75rem;
    justify-content: flex-end;
    margin-top: 0.25rem;
  }

  .btn {
    width: auto;
    padding: 0.5rem 1.25rem;
    border-radius: 8px;
    border: none;
    cursor: pointer;
    font-family: inherit;
    font-weight: 600;
    font-size: 0.9rem;
    text-decoration: none;
  }

  .btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .btn-danger {
    background: var(--error);
    color: #fff;
  }

  .btn-primary {
    background: var(--primary);
    color: var(--button-text);
  }

  .btn-secondary {
    background: var(--surface);
    color: var(--fg);
    border: 1px solid var(--border);
  }
</style>
