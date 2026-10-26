<script>
  import { onDestroy } from 'svelte';
  import Avatar from '$lib/components/Avatar.svelte';
  import MockSideNav from '../MockSideNav.svelte';
  import MockToolbar from '../MockToolbar.svelte';
  import { townHalls, patchTownHall, nameOf, liveAttendees, otherListeners, me } from '../data.js';
  import '../townhalls.css';

  let nowMs = Date.now();
  const timer = setInterval(() => (nowMs = Date.now()), 1000);
  onDestroy(() => clearInterval(timer));

  $: live = $townHalls.filter((t) => t.state === 'live');
  $: upcoming = $townHalls.filter((t) => t.state === 'scheduled').sort((a, b) => a.startsAt.localeCompare(b.startsAt));
  $: past = $townHalls.filter((t) => t.state === 'ended' || t.state === 'cancelled');

  $: liveCount = liveAttendees.length + Object.values(otherListeners).reduce((a, b) => a + b, 0);
  const liveSpeakers = liveAttendees.filter((a) => a.role !== 'audience' || a.floor).slice(0, 5);

  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
  /** @param {string} iso */
  const when = (iso) =>
    new Date(iso).toLocaleString(undefined, { weekday: 'long', day: 'numeric', month: 'long', hour: '2-digit', minute: '2-digit' });

  /** @param {string} iso */
  function countdown(iso) {
    const s = Math.max(0, Math.floor((new Date(iso).getTime() - nowMs) / 1000));
    const d = Math.floor(s / 86400);
    const h = Math.floor((s % 86400) / 3600);
    const m = Math.floor((s % 3600) / 60);
    return d > 0 ? `in ${d}d ${h}h ${m}m` : `in ${h}h ${m}m ${s % 60}s`;
  }

  /** @param {string} iso */
  function elapsed(iso) {
    const s = Math.floor((nowMs - new Date(iso).getTime()) / 1000);
    return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
  }

  /** @param {number} s */
  const duration = (s) => `${Math.floor(s / 60)} min`;

  let toast = '';
  /** @param {string} text */
  function flash(text) {
    toast = text;
    setTimeout(() => (toast = ''), 2600);
  }

  /** @param {any} th */
  function toggleRsvp(th) {
    patchTownHall(th.id, { rsvp: !th.rsvp, rsvps: th.rsvps + (th.rsvp ? -1 : 1) });
    flash(th.rsvp ? 'RSVP removed. No reminders.' : "RSVP'd. You'll get a reminder 15 minutes before.");
  }

  /** @type {any} */
  let cancelling = null;
  function confirmCancel() {
    patchTownHall(cancelling.id, { state: 'cancelled' });
    flash('Cancelled. Everyone who was notified is told.');
    cancelling = null;
  }
</script>

  <MockSideNav currentPage="townhalls" />
  <div class="th-container">
    <div class="th-content">
      <p class="th-note">
        Town halls mockup: list. The toolbar's 🎙️ item, with a live dot while one is on.
        <a href="/mocks/townhalls">All screens</a>
      </p>

      <div class="head">
        <h1 class="th-title">Town halls</h1>
        {#if me.admin}
          <a href="/mocks/townhalls/schedule" class="th-btn primary">＋ Schedule</a>
        {/if}
      </div>

      {#if live.length > 0}
        <h2 class="th-section"><span class="th-chip live"><span class="th-dot"></span>LIVE</span> Now</h2>
        {#each live as th (th.id)}
          <a href="/mocks/townhalls/live" class="th-card live-card">
            <div class="row">
              <span class="th-chip live"><span class="th-dot"></span>LIVE {elapsed(th.startedAt)}</span>
              {#if th.recording}<span class="th-chip rec">● Recording</span>{/if}
              {#if th.scope === 'peers'}<span class="th-chip primary">🌐 Open to connected servers</span>{/if}
            </div>
            <h3 class="topic">{th.topic}</h3>
            <p class="th-hint">Hosted by <strong>{nameOf(th.host)}</strong> · moderated by <strong>{nameOf(th.moderator)}</strong></p>
            <div class="live-foot">
              <div class="stack">
                {#each liveSpeakers as a (a.userID)}
                  <span class="stack-item"><Avatar userID={a.userID} username={nameOf(a.userID)} size="28px" /></span>
                {/each}
              </div>
              <span class="th-hint">{liveCount} listening from 4 servers{th.capacity ? ` · ${liveCount}/${th.capacity}` : ''}</span>
              <span class="th-btn primary small join">Join →</span>
            </div>
          </a>
        {/each}
      {/if}

      <h2 class="th-section">Upcoming</h2>
      {#if upcoming.length === 0}
        <p class="th-hint">Nothing scheduled.</p>
      {/if}
      {#each upcoming as th (th.id)}
        <div class="th-card">
          <div class="row">
            <span class="th-chip">🗓 {countdown(th.startsAt)}</span>
            {#if th.recording}
              <span class="th-chip rec">● Will be recorded</span>
            {:else}
              <span class="th-chip">Not recorded</span>
            {/if}
            <span class="th-chip">{th.scope === 'peers' ? '🌐 Open to connected servers' : '🏠 Community only'}</span>
            {#if th.capacity}<span class="th-chip">👥 {th.capacity} seats</span>{/if}
          </div>
          <h3 class="topic">{th.topic}</h3>
          <p class="when">{when(th.startsAt)} <span class="th-hint">({tz})</span></p>
          <p class="desc">{th.description}</p>
          <dl class="roles">
            <dt>Host</dt>
            <dd><Avatar userID={th.host} username={nameOf(th.host)} size="20px" /> {nameOf(th.host)}</dd>
            <dt>Moderator</dt>
            <dd><Avatar userID={th.moderator} username={nameOf(th.moderator)} size="20px" /> {nameOf(th.moderator)}</dd>
            <dt>Panel</dt>
            <dd>{th.panelists.map(nameOf).join(', ')}</dd>
          </dl>
          <div class="actions">
            <button type="button" class="th-btn" class:primary={!th.rsvp} on:click={() => toggleRsvp(th)}>
              {th.rsvp ? '✓ Going' : 'RSVP'}
            </button>
            <span class="th-hint">{th.rsvps} going · RSVP to get a reminder. It doesn't reserve a seat.</span>
            {#if me.admin}
              <span class="admin-actions">
                <a href="/mocks/townhalls/schedule?edit={th.id}" class="th-btn small">Edit</a>
                <button type="button" class="th-btn small danger" on:click={() => (cancelling = th)}>Cancel</button>
              </span>
            {/if}
          </div>
          {#if me.admin}
            <p class="th-hint admin-hint">Only an admin can start it, any time from 15 minutes before. Organized by {nameOf(th.organizer)}.</p>
          {/if}
        </div>
      {/each}

      <h2 class="th-section">Past</h2>
      {#each past as th (th.id)}
        {#if th.state === 'cancelled'}
          <div class="th-card past cancelled">
            <span class="th-chip">Cancelled</span>
            <h3 class="topic">{th.topic}</h3>
          </div>
        {:else}
          <a href="/mocks/townhalls/archive" class="th-card past">
            <div class="play">▶</div>
            <div class="past-main">
              <h3 class="topic">{th.topic}</h3>
              <p class="th-hint">
                {new Date(th.startedAt).toLocaleDateString(undefined, { day: 'numeric', month: 'long' })} ·
                {duration(th.durationSeconds)} · {th.attended} attended from {th.attendedServers} servers
              </p>
              <div class="row">
                <span class="th-chip ok">✓ Signed recording</span>
                <span class="th-chip">📝 Transcript</span>
                <span class="th-chip">💬 Chat replay</span>
              </div>
            </div>
          </a>
        {/if}
      {/each}
    </div>
    <MockToolbar currentPage="townhalls" />
  </div>

{#if cancelling}
  <div class="th-modal-backdrop" role="presentation" on:click|self={() => (cancelling = null)}>
    <div class="th-modal" role="dialog" aria-modal="true">
      <h2>Cancel this town hall?</h2>
      <p class="th-hint">“{cancelling.topic}”. Everyone was notified when it was scheduled, so they're told it's cancelled. {cancelling.rsvps} people RSVP'd.</p>
      <div class="th-modal-actions">
        <button type="button" class="th-btn" on:click={() => (cancelling = null)}>Keep it</button>
        <button type="button" class="th-btn danger" on:click={confirmCancel}>Cancel town hall</button>
      </div>
    </div>
  </div>
{/if}

{#if toast}<div class="th-toast">{toast}</div>{/if}

<style>
  .head {
    display: flex;
    justify-content: space-between;
    align-items: center;
  }

  .head .th-title {
    margin: 0;
  }

  .th-card {
    display: block;
    margin-bottom: 0.75rem;
    color: var(--fg);
    text-decoration: none;
  }

  a.th-card:hover {
    border-color: var(--primary);
  }

  .live-card {
    border-color: var(--error);
    background: linear-gradient(135deg, rgba(255, 107, 107, 0.1), var(--surface) 60%);
  }

  .row {
    display: flex;
    flex-wrap: wrap;
    gap: 0.375rem;
  }

  .topic {
    margin: 0.5rem 0 0.25rem;
    font-size: 1.05rem;
  }

  .when {
    margin: 0 0 0.5rem;
    font-weight: 600;
  }

  .desc {
    margin: 0 0 0.75rem;
    color: var(--muted);
    font-size: 0.9rem;
  }

  .live-foot {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    margin-top: 0.75rem;
  }

  .stack {
    display: flex;
  }

  .stack-item {
    margin-right: -0.5rem;
    border: 2px solid var(--surface);
    border-radius: 50%;
    display: inline-flex;
  }

  .join {
    margin-left: auto;
  }

  .roles {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 0.25rem 0.75rem;
    margin: 0 0 0.75rem;
    font-size: 0.85rem;
  }

  .roles dt {
    color: var(--muted);
  }

  .roles dd {
    display: flex;
    align-items: center;
    gap: 0.375rem;
    margin: 0;
  }

  .actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem 0.75rem;
  }

  .admin-actions {
    display: flex;
    gap: 0.5rem;
    margin-left: auto;
  }

  .admin-hint {
    margin-top: 0.75rem;
    padding-top: 0.5rem;
    border-top: 1px dashed var(--border);
  }

  .past {
    display: flex;
    gap: 1rem;
    align-items: center;
  }

  .past .topic {
    margin-top: 0;
  }

  .past-main {
    display: flex;
    flex-direction: column;
    gap: 0.375rem;
    min-width: 0;
  }

  .cancelled {
    flex-direction: column;
    align-items: flex-start;
    opacity: 0.6;
  }

  .cancelled .topic {
    text-decoration: line-through;
  }

  .play {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 3rem;
    height: 3rem;
    border-radius: 50%;
    background: var(--primary);
    color: var(--button-text);
    font-size: 1.1rem;
  }
</style>
