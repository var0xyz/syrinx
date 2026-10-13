<script lang="ts">
  import { ADMIN_PERMISSIONS } from '$lib/utils/permissions';

  /** The permissions the viewer holds; only these can be granted. */
  export let available: string[] = [];
  export let selected = new Set<string>();

  $: offered = ADMIN_PERMISSIONS.filter((p) => available.includes(p));
  $: allSelected = offered.length > 0 && offered.every((p) => selected.has(p));

  function toggle(p: string): void {
    const next = new Set(selected);
    if (next.has(p)) next.delete(p);
    else next.add(p);
    selected = next;
  }

  function toggleAll(): void {
    selected = allSelected ? new Set() : new Set(offered);
  }
</script>

<fieldset class="picker">
  <legend>Permissions</legend>
  <label class="option all">
    <input type="checkbox" checked={allSelected} on:change={toggleAll} />
    <span>Select all</span>
  </label>
  {#each offered as p (p)}
    <label class="option">
      <input type="checkbox" checked={selected.has(p)} on:change={() => toggle(p)} />
      <code>{p}</code>
    </label>
  {/each}
</fieldset>

<style>
  .picker {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    margin: 0;
    padding: 0.5rem 0.75rem;
    border: 1px solid var(--border);
    border-radius: 8px;
  }

  legend {
    padding: 0 0.25rem;
    color: var(--muted);
    font-size: 0.85rem;
  }

  .option {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    color: var(--fg);
    font-size: 0.9rem;
    cursor: pointer;
  }

  .option input {
    width: auto;
    margin: 0;
    padding: 0;
    flex-shrink: 0;
  }

  .option.all {
    padding-bottom: 0.25rem;
    border-bottom: 1px solid var(--border);
    font-weight: 600;
  }
</style>
