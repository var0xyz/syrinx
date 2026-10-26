<script>
  import { get } from 'svelte/store';
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import Avatar from '$lib/components/Avatar.svelte';
  import { countCharacters } from '$lib/utils/text';
  import MockSideNav from '../MockSideNav.svelte';
  import MockToolbar from '../MockToolbar.svelte';
  import { townHalls, addTownHall, patchTownHall, people, nameOf, serverOf, me, HOME, TOPIC_MAX } from '../data.js';
  import '../townhalls.css';

  const admins = Object.values(people).filter((p) => p.admin);
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;

  const editID = $page.url.searchParams.get('edit');
  const existing = editID ? get(townHalls).find((t) => t.id === editID) : null;

  /** @param {Date} d */
  const localInput = (d) => new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
  const nextWeek = new Date(Date.now() + 7 * 24 * 3600 * 1000);
  nextWeek.setHours(19, 0, 0, 0);

  let topic = existing?.topic ?? '';
  let description = existing?.description ?? '';
  let startsAt = localInput(existing ? new Date(existing.startsAt) : nextWeek);
  let host = existing?.host ?? me.userID;
  let moderator = existing?.moderator ?? '12@syrinx.local';
  /** @type {string[]} */
  let panelists = existing?.panelists ?? admins.map((a) => a.userID);
  let guestQuery = '';
  let recording = existing?.recording ?? true;
  let chat = existing?.chat ?? true;
  let capped = !!existing?.capacity;
  let capacity = existing?.capacity || 100;
  let scope = existing?.scope ?? 'community';
  let maxMinutes = existing?.maxMinutes ?? 60;
  let turnLimit = existing ? existing.turnSeconds > 0 : true;
  let turnSeconds = existing?.turnSeconds || 120;
  let submitted = false;

  $: topicCount = countCharacters(topic);
  // A capped town hall must be recorded, so those who couldn't get in can catch up.
  $: if (capped) recording = true;
  $: guests = panelists.filter((id) => !people[id]?.admin);
  $: guestMatches = guestQuery.trim()
    ? Object.values(people).filter(
        (p) => !p.admin && !panelists.includes(p.userID) && p.username.includes(guestQuery.trim().toLowerCase())
      )
    : [];
  $: sameHostModerator = host === moderator;
  $: valid = topicCount > 0 && topicCount <= TOPIC_MAX && startsAt;

  /** @param {string} id */
  function togglePanelist(id) {
    panelists = panelists.includes(id) ? panelists.filter((p) => p !== id) : [...panelists, id];
  }

  /** @param {string} id */
  function addGuest(id) {
    panelists = [...panelists, id];
    guestQuery = '';
  }

  function submit() {
    if (!valid) return;
    const record = {
      topic: topic.trim(),
      description: description.trim(),
      startsAt: new Date(startsAt).toISOString(),
      host,
      moderator,
      panelists,
      recording,
      chat,
      capacity: capped ? capacity : 0,
      scope,
      maxMinutes,
      turnSeconds: turnLimit ? turnSeconds : 0,
    };
    if (existing) patchTownHall(existing.id, record);
    else addTownHall({ id: `th-${Date.now()}`, state: 'scheduled', organizer: me.userID, rsvps: 0, rsvp: false, ...record });
    submitted = true;
    setTimeout(() => goto('/mocks/townhalls/list'), 1800);
  }
</script>

  <MockSideNav currentPage="townhalls" />
  <div class="th-container">
    <div class="th-content">
      <p class="th-note">
        Town halls mockup: {existing ? 'editing' : 'scheduling'}, admins only. Submitting signs a town hall record and the
        server countersigns it. <a href="/mocks/townhalls">All screens</a>
      </p>

      <h1 class="th-title">{existing ? 'Edit town hall' : 'Schedule a town hall'}</h1>

      <form class="th-card form" on:submit|preventDefault={submit}>
        <label class="field">
          <span class="label">Topic</span>
          <input bind:value={topic} placeholder="What's it about?" />
          <span class="counter" class:over={topicCount > TOPIC_MAX}>{TOPIC_MAX - topicCount}</span>
        </label>

        <label class="field">
          <span class="label">Description <span class="opt">optional</span></span>
          <textarea rows="3" bind:value={description} placeholder="What to expect, what to bring"></textarea>
        </label>

        <div class="grid2">
          <label class="field">
            <span class="label">Starts</span>
            <input type="datetime-local" bind:value={startsAt} />
            <span class="th-hint">Your time ({tz}). Everyone sees it in their own.</span>
          </label>
          <label class="field">
            <span class="label">Ends after at most</span>
            <select bind:value={maxMinutes}>
              {#each [30, 45, 60, 90, 120] as m}<option value={m}>{m} minutes</option>{/each}
            </select>
            <span class="th-hint">The host or moderator can end it sooner.</span>
          </label>
        </div>

        <h2 class="group">Roles</h2>
        <div class="grid2">
          <label class="field">
            <span class="label">Host</span>
            <select bind:value={host}>
              {#each admins as a}<option value={a.userID}>{a.username}</option>{/each}
            </select>
            <span class="th-hint">Introduces the topic and answers. Mic always open.</span>
          </label>
          <label class="field">
            <span class="label">Moderator</span>
            <select bind:value={moderator}>
              {#each admins as a}<option value={a.userID}>{a.username}</option>{/each}
            </select>
            <span class="th-hint">Runs the queue and the floor. If they drop, the host takes over.</span>
          </label>
        </div>
        {#if sameHostModerator}
          <p class="th-hint warn">One person hosting and moderating works, but they'll be answering and running the queue at once.</p>
        {/if}

        <div class="field">
          <span class="label">Panelists <span class="opt">mic always open, can jump in any time</span></span>
          <div class="people">
            {#each admins.filter((a) => a.userID !== host && a.userID !== moderator) as a}
              <label class="person">
                <input type="checkbox" checked={panelists.includes(a.userID)} on:change={() => togglePanelist(a.userID)} />
                <Avatar userID={a.userID} username={a.username} size="22px" />
                {a.username} <span class="th-chip">admin</span>
              </label>
            {/each}
            {#each guests as id}
              <label class="person">
                <input type="checkbox" checked on:change={() => togglePanelist(id)} />
                <Avatar userID={id} username={nameOf(id)} size="22px" />
                {nameOf(id)}
                <span class="th-chip primary">guest</span>
                {#if serverOf(id) !== HOME}<span class="th-server peer">@{serverOf(id)}</span>{/if}
              </label>
            {/each}
          </div>
          <input class="guest-search" bind:value={guestQuery} placeholder="Add a guest panelist: they don't become an admin" />
          {#if guestMatches.length > 0}
            <ul class="matches">
              {#each guestMatches.slice(0, 5) as p}
                <li>
                  <button type="button" on:click={() => addGuest(p.userID)}>
                    <Avatar userID={p.userID} username={p.username} size="20px" />
                    {p.username}
                    <span class="th-server" class:peer={serverOf(p.userID) !== HOME}>@{serverOf(p.userID)}</span>
                  </button>
                </li>
              {/each}
            </ul>
          {/if}
        </div>

        <h2 class="group">Floor</h2>
        <label class="toggle">
          <input type="checkbox" bind:checked={turnLimit} />
          <span>Limit each turn from the audience to</span>
          <select bind:value={turnSeconds} disabled={!turnLimit}>
            {#each [60, 90, 120, 180, 300] as s}<option value={s}>{s / 60} min</option>{/each}
          </select>
        </label>
        <label class="toggle">
          <input type="checkbox" bind:checked={chat} />
          <span>Side chat <span class="th-hint">· the moderator can turn it on and off during the call</span></span>
        </label>

        <h2 class="group">Who can attend</h2>
        <label class="radio">
          <input type="radio" bind:group={scope} value="community" />
          <span><strong>🏠 Community only</strong><br /><span class="th-hint">Members of {HOME}.</span></span>
        </label>
        <label class="radio">
          <input type="radio" bind:group={scope} value="peers" />
          <span>
            <strong>🌐 Open to every connected server</strong><br />
            <span class="th-hint">
              Each peer notifies its own users. They can raise a hand, speak and chat. Users we've banned can't join.
            </span>
          </span>
        </label>
        <label class="toggle">
          <input type="checkbox" bind:checked={capped} />
          <span>Limit attendance to</span>
          <input type="number" min="2" class="num" bind:value={capacity} disabled={!capped} />
          <span>people</span>
        </label>

        <h2 class="group">Recording</h2>
        <label class="toggle">
          <input type="checkbox" bind:checked={recording} disabled={capped} />
          <span>Record this town hall</span>
        </label>
        {#if capped}
          <p class="th-hint">A town hall with limited seats is always recorded, so those who can't get in can listen later.</p>
        {/if}
        {#if recording}
          <div class="rec-info">
            <p>● Everyone is told before they join.</p>
            <p>Every admin present records on their own device. The server never keeps audio.</p>
            <p>Afterwards you or the moderator pick one recording. It's published with a transcript and the side chat, signed.</p>
          </div>
        {/if}

        <div class="submit">
          <a href="/mocks/townhalls/list" class="th-btn">Cancel</a>
          <button type="submit" class="th-btn primary" disabled={!valid}>
            {existing ? 'Save changes' : 'Schedule and notify everyone'}
          </button>
        </div>
      </form>
    </div>
    <MockToolbar currentPage="townhalls" />
  </div>

{#if submitted}
  <div class="th-toast">
    ✓ Signed by you, countersigned by {HOME}.
    {existing ? 'Everyone who RSVP’d is told about the change.' : scope === 'peers' ? 'Everyone here and on 3 peers is notified.' : 'Everyone is notified.'}
  </div>
{/if}

<style>
  .form {
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  .field {
    position: relative;
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
  }

  .label {
    font-size: 0.85rem;
    font-weight: 600;
  }

  .opt {
    color: var(--muted);
    font-weight: 400;
  }

  input:not([type='checkbox']):not([type='radio']),
  textarea,
  select {
    padding: 0.5rem 0.625rem;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--input-bg);
    color: var(--fg);
    font: inherit;
    font-size: 0.9rem;
  }

  textarea {
    resize: vertical;
  }

  .counter {
    position: absolute;
    right: 0.625rem;
    top: 2rem;
    color: var(--muted);
    font-size: 0.75rem;
  }

  .counter.over {
    color: var(--error);
  }

  .grid2 {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 1rem;
  }

  @media (max-width: 560px) {
    .grid2 {
      grid-template-columns: 1fr;
    }
  }

  .group {
    margin: 0.5rem 0 -0.25rem;
    padding-top: 0.75rem;
    border-top: 1px solid var(--border);
    font-size: 0.85rem;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--muted);
  }

  .warn {
    margin-top: -0.5rem;
    color: #d29922;
  }

  .people {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
  }

  .person {
    display: inline-flex;
    align-items: center;
    gap: 0.375rem;
    padding: 0.25rem 0.5rem;
    border: 1px solid var(--border);
    border-radius: 999px;
    font-size: 0.85rem;
  }

  .guest-search {
    margin-top: 0.25rem;
  }

  .matches {
    list-style: none;
    margin: 0;
    padding: 0.25rem;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--input-bg);
  }

  .matches button {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    width: 100%;
    padding: 0.375rem 0.5rem;
    border: none;
    border-radius: 6px;
    background: none;
    color: var(--fg);
    font: inherit;
    cursor: pointer;
  }

  .matches button:hover {
    background: var(--surface);
  }

  .toggle,
  .radio {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 0.5rem;
    font-size: 0.9rem;
  }

  .radio {
    align-items: flex-start;
  }

  .num {
    width: 5rem;
  }

  .rec-info {
    padding: 0.75rem;
    border: 1px solid var(--error);
    border-radius: 8px;
    background: rgba(255, 107, 107, 0.06);
    font-size: 0.85rem;
  }

  .rec-info p {
    margin: 0.25rem 0;
  }

  .rec-info p:first-child {
    color: var(--error);
    font-weight: 600;
  }

  .submit {
    display: flex;
    justify-content: flex-end;
    gap: 0.5rem;
    padding-top: 0.75rem;
    border-top: 1px solid var(--border);
  }
</style>
