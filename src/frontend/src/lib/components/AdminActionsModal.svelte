<script lang="ts">
  import { createEventDispatcher } from 'svelte';

  // Mockup: the invite tree and every submission are local only.
  export let open = false;
  export let userID: string;
  export let username = '';
  export let role = 'user';
  export let viewerIsRoot = false;

  type Action = 'suspend' | 'reinstate' | 'kick' | 'promote' | 'demote' | 'rebind' | 'message';

  interface TreeNode {
    id: string;
    username: string;
    role: string;
    depth: number;
  }

  const dispatch = createEventDispatcher<{ close: void }>();

  const suspended = false;
  const isAdminTarget = role === 'admin';

  $: actions = [
    isAdminTarget
      ? { id: 'demote', icon: 'demote', label: 'Demote to user', hint: 'Take away admin rights' }
      : { id: 'promote', icon: 'promote', label: 'Promote to admin', hint: 'Give admin rights' },
    { id: 'rebind', icon: 'key', label: 'Add a key', hint: 'For a member who lost their device and has no backup' },
    { id: 'message', icon: 'mail', label: 'Send a private message', hint: 'Delivered to their mailbox only' },
    suspended
      ? { id: 'reinstate', icon: 'reinstate', label: 'Reinstate', hint: 'Lift the suspension' }
      : { id: 'suspend', icon: 'suspend', label: 'Suspend', hint: 'Block sign-in until reinstated; nothing is deleted' },
    { id: 'kick', icon: 'kick', label: 'Kick', hint: 'Remove the account and all its content', danger: true },
  ] as { id: Action; icon: string; label: string; hint: string; danger?: boolean }[];

  $: tree = [
    { id: 'inviter', username: 'marta', role: 'admin', depth: 0 },
    { id: userID, username, role, depth: 1 },
    { id: 'invitee-1', username: `${username}_friend`, role: 'user', depth: 2 },
    { id: 'invitee-2', username: 'quietfox', role: 'user', depth: 2 },
    { id: 'invitee-3', username: 'latecomer', role: 'user', depth: 3 },
  ] as TreeNode[];

  let action: Action | null = null;
  let checked = new Set<string>();
  let reason = '';
  let message = '';
  let keyFile: File | null = null;
  let done = false;

  $: picksTargets = action === 'suspend' || action === 'kick';
  $: chosen = actions.find((a) => a.id === action);
  $: canSubmit =
    action === 'message'
      ? message.trim().length > 0
      : reason.trim().length > 0 && (!picksTargets || checked.size > 0) && (action !== 'rebind' || !!keyFile);

  function selectable(n: TreeNode): boolean {
    if (n.role === 'root') return false;
    return n.role !== 'admin' || viewerIsRoot;
  }

  function choose(id: Action) {
    action = id;
    checked = new Set([userID]);
    reason = '';
    message = '';
    keyFile = null;
  }

  function toggle(id: string) {
    const next = new Set(checked);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    checked = next;
  }

  function close() {
    action = null;
    done = false;
    dispatch('close');
  }

  function onFile(e: Event) {
    keyFile = (e.currentTarget as HTMLInputElement).files?.[0] ?? null;
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
        <a class="btn btn-secondary" href="/admin/actions" on:click={close}>View in Actions</a>
        <button class="btn btn-primary" type="button" on:click={close}>Done</button>
      </div>
    {:else if !action}
      <h3 id="admin-actions-title">Admin actions</h3>
      <p class="who"><span class="name">{username}</span></p>
      <ul class="action-list">
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
      <div class="actions">
        <button class="btn btn-secondary" type="button" on:click={close}>Close</button>
      </div>
    {:else}
      <h3 id="admin-actions-title" class="form-title" class:danger={chosen?.danger}>
        <span class="action-icon" style="--icon: url('/icons/{chosen?.icon}-16.svg')" aria-hidden="true"></span>
        {chosen?.label}
      </h3>
      <p class="who"><span class="name">{username}</span></p>

      {#if picksTargets}
        <ul class="tree">
          {#each tree as n (n.id)}
            <li style="padding-left: {n.depth * 1.25}rem">
              <label class="tree-node" class:locked={!selectable(n)}>
                {#if selectable(n)}
                  <input type="checkbox" checked={checked.has(n.id)} on:change={() => toggle(n.id)} />
                {:else}
                  <span class="lock-icon" aria-label="Only root can select an admin"></span>
                {/if}
                <span class="name">{n.username}</span>
                {#if n.role !== 'user'}<span class="node-role">{n.role}</span>{/if}
              </label>
            </li>
          {/each}
        </ul>
      {/if}

      {#if action === 'rebind'}
        <label class="field">
          <span>Public key file</span>
          <input type="file" accept=".gz" on:change={onFile} />
        </label>
        {#if keyFile}
          <div class="fingerprint">
            <span>Read this back to them</span>
            <code>7F3A 91C2 0B44 E1D8 5A7C  3E90 12BF 64D1 A8C3 2F06</code>
          </div>
        {/if}
      {/if}

      {#if action === 'message'}
        <label class="field">
          <span>Message</span>
          <textarea bind:value={message} rows="5" maxlength="1000"></textarea>
        </label>
      {:else}
        <label class="field">
          <span>Reason</span>
          <textarea bind:value={reason} rows="3" maxlength="1000"></textarea>
        </label>
      {/if}

      {#if action === 'kick' && checked.size > 0}
        <p class="warning">{kickSummary}</p>
      {/if}

      <div class="actions">
        <button class="btn btn-secondary" type="button" on:click={() => (action = null)}>Back</button>
        <button
          class="btn"
          class:btn-danger={action === 'kick'}
          class:btn-primary={action !== 'kick'}
          type="button"
          disabled={!canSubmit}
          on:click={() => (done = true)}
        >
          Sign and request
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
    max-height: calc(100vh - 2rem);
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
    text-align: left;
  }

  h3 {
    margin: 0;
    font-size: 1.1rem;
    color: var(--fg);
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
