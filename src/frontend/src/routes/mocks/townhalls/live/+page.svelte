<script>
  import { onDestroy, tick } from 'svelte';
  import { goto } from '$app/navigation';
  import Avatar from '$lib/components/Avatar.svelte';
  import { countCharacters } from '$lib/utils/text';
  import MockSideNav from '../MockSideNav.svelte';
  import MockToolbar from '../MockToolbar.svelte';
  import {
    townHalls,
    liveAttendees,
    otherListeners,
    liveChat,
    incomingChat,
    servers,
    people,
    nameOf,
    serverOf,
    HOME,
    CHAT_MAX,
  } from '../data.js';
  import '../townhalls.css';

  $: th = $townHalls.find((t) => t.id === 'th-live');

  const WREN = '7@syrinx.local';
  const HERON = '31@syrinx.local';
  const KESTREL = '12@syrinx.local';

  // Mock-only: which seat the viewer sits in. Moderator view is heron's.
  /** @type {'audience' | 'panelist' | 'moderator'} */
  let viewAs = 'audience';
  $: meID = viewAs === 'moderator' ? HERON : WREN;

  let joined = false;
  let attendees = liveAttendees.map((a) => ({ ...a }));
  /** @type {string[]} */
  let queue = attendees.filter((a) => a.hand).sort((a, b) => a.hand - b.hand).map((a) => a.userID);
  let floorID = '63@syrinx.local';
  let floorMicOn = true;
  let floorLeft = 83;
  let moderatorGone = false;
  let chatOn = true;
  /** @type {Set<string>} */
  let speaking = new Set();
  let messages = liveChat.map((m) => ({ ...m, kind: 'chat' }));
  let draft = '';
  /** @type {'people' | 'chat'} */
  let tab = 'people';
  let unseenChat = 0;
  /** @type {any} */
  let removing = null;
  let toast = '';
  let nowMs = Date.now();

  // Wren's own seat follows the view: audience or panelist.
  $: attendees = attendees.map((a) => (a.userID === WREN ? { ...a, role: viewAs === 'audience' ? 'audience' : 'panelist' } : a));
  $: if (viewAs !== 'audience') {
    queue = queue.filter((id) => id !== WREN);
    if (floorID === WREN) floorID = '';
  }

  $: me = attendees.find((a) => a.userID === meID);
  $: isModerator = viewAs === 'moderator';
  $: openMic = (a) => a.role !== 'audience';
  $: stage = attendees.filter((a) => a.role !== 'audience' || a.userID === floorID);
  $: myPosition = queue.indexOf(WREN) + 1;
  $: iHaveFloor = floorID === WREN;
  $: queueRunner = moderatorGone ? KESTREL : HERON;

  $: byServer = Object.keys(servers).map((sid) => ({
    server: servers[sid],
    shown: attendees.filter((a) => serverOf(a.userID) === sid),
    others: otherListeners[sid] ?? 0,
  }));
  $: total = byServer.reduce((n, g) => n + g.shown.length + g.others, 0);
  $: recorders = attendees.filter((a) => people[a.userID]?.admin);

  $: chatCount = countCharacters(draft);
  $: canSend = chatOn && draft.trim().length > 0 && chatCount <= CHAT_MAX;

  /** @param {string} iso */
  function clock(iso) {
    const s = Math.max(0, Math.floor((nowMs - new Date(iso).getTime()) / 1000));
    return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
  }

  /** @param {number} s */
  const mmss = (s) => `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;

  /** @param {string} text */
  function flash(text) {
    toast = text;
    setTimeout(() => (toast = ''), 2800);
  }

  /** @param {string} text */
  function system(text) {
    messages = [...messages, { id: `sys-${Date.now()}`, kind: 'system', content: text, at: new Date().toISOString() }];
  }

  /** @param {string} userID @param {Record<string, unknown>} patch */
  function patch(userID, patch) {
    attendees = attendees.map((a) => (a.userID === userID ? { ...a, ...patch } : a));
  }

  // Who can be heard: open mics not muted, and the floor holder with mic on.
  $: audible = attendees.filter(
    (a) => (openMic(a) && !a.muted) || (a.userID === floorID && floorMicOn && !(a.userID === WREN && !iHaveFloor))
  );

  let incomingIndex = 0;
  let tickCount = 0;
  const timer = setInterval(() => {
    nowMs = Date.now();
    tickCount++;
    if (floorID && floorLeft > 0 && th?.turnSeconds) {
      floorLeft--;
      if (floorLeft === 0) {
        system(`Time's up for ${nameOf(floorID)}. The floor is back with ${nameOf(queueRunner)}.`);
        floorID = '';
      }
    }
    if (tickCount % 2 === 0) {
      const pool = audible.filter((a) => a.userID !== WREN || iHaveFloor || viewAs === 'panelist');
      const pick = pool.length ? [pool[Math.floor(Math.random() * pool.length)].userID] : [];
      // The floor holder talks most of the time.
      if (floorID && floorMicOn && Math.random() < 0.6) pick[0] = floorID;
      if (pool.length > 1 && Math.random() < 0.2) pick.push(pool[Math.floor(Math.random() * pool.length)].userID);
      speaking = new Set(pick.filter((id) => id !== WREN));
    }
    if (tickCount % 9 === 0 && incomingIndex < incomingChat.length && chatOn) {
      const [from, content] = incomingChat[incomingIndex++];
      messages = [...messages, { id: `in-${incomingIndex}`, kind: 'chat', from, content, at: new Date().toISOString() }];
      if (tab !== 'chat') unseenChat++;
    }
  }, 1000);
  onDestroy(() => clearInterval(timer));

  function raiseHand() {
    if (myPosition) {
      queue = queue.filter((id) => id !== WREN);
      flash('Hand lowered.');
    } else {
      queue = [...queue, WREN];
      flash(`Hand raised. You're number ${queue.length}.`);
    }
  }

  /** @param {string} userID */
  function grant(userID) {
    if (floorID) system(`${nameOf(floorID)}'s turn ended.`);
    queue = queue.filter((id) => id !== userID);
    floorID = userID;
    floorMicOn = userID !== WREN && Math.random() < 0.9;
    floorLeft = th.turnSeconds;
    system(`${nameOf(queueRunner)} gave ${nameOf(userID)} the floor.`);
  }

  function revoke() {
    system(`${nameOf(queueRunner)} took the floor back from ${nameOf(floorID)}.`);
    floorID = '';
  }

  function done() {
    system(`${nameOf(floorID)} handed the floor back.`);
    floorID = '';
  }

  /** @param {number} i @param {number} dir */
  function move(i, dir) {
    const j = i + dir;
    if (j < 0 || j >= queue.length) return;
    const next = [...queue];
    [next[i], next[j]] = [next[j], next[i]];
    queue = next;
  }

  /** @param {any} a */
  function muteOther(a) {
    patch(a.userID, { muted: true });
    if (a.userID === floorID) floorMicOn = false;
    flash(`Muted ${nameOf(a.userID)}. Only they can turn their mic back on.`);
  }

  function toggleMyMic() {
    if (iHaveFloor) {
      floorMicOn = !floorMicOn;
    } else {
      patch(meID, { muted: !me.muted });
    }
  }

  function confirmRemove() {
    const id = removing.userID;
    attendees = attendees.filter((a) => a.userID !== id);
    queue = queue.filter((q) => q !== id);
    if (floorID === id) floorID = '';
    messages = messages.filter((m) => m.from !== id);
    system(`${nameOf(id)} was removed by the moderator.`);
    removing = null;
  }

  function toggleChat() {
    chatOn = !chatOn;
    system(chatOn ? `${nameOf(queueRunner)} turned the chat on.` : `${nameOf(queueRunner)} turned the chat off.`);
  }

  function toggleModeratorGone() {
    moderatorGone = !moderatorGone;
    patch(HERON, { reconnecting: moderatorGone });
    system(moderatorGone ? 'heron disconnected. kestrel, the host, runs the queue until they are back.' : 'heron is back and runs the queue again.');
  }

  function send() {
    if (!canSend) return;
    messages = [...messages, { id: `me-${Date.now()}`, kind: 'chat', from: meID, content: draft.trim(), at: new Date().toISOString() }];
    draft = '';
  }

  function leave() {
    void goto('/mocks/townhalls/list');
  }

  /** @param {HTMLElement} node @param {number} _count */
  function stickToBottom(node, _count) {
    const toEnd = () => tick().then(() => (node.scrollTop = node.scrollHeight));
    toEnd();
    return { update: toEnd };
  }

  /** @param {string} text */
  const withMentions = (text) => text.split(/(@\w+)/g);

  const roleLabel = { host: 'Host', moderator: 'Moderator', panelist: 'Panelist', audience: '' };
</script>

  <MockSideNav currentPage="townhalls" />
  <div class="th-container">
    <div class="th-content wide">
      <div class="mock-bar">
        <span class="th-note">Mockup. View as:</span>
        {#each ['audience', 'panelist', 'moderator'] as v}
          <button type="button" class="th-btn small" class:primary={viewAs === v} on:click={() => (viewAs = v)}>
            {v === 'moderator' ? 'moderator (heron)' : v}
          </button>
        {/each}
        <span class="sep"></span>
        <button type="button" class="th-btn small" on:click={toggleModeratorGone}>
          {moderatorGone ? 'Moderator reconnects' : 'Moderator disconnects'}
        </button>
        {#if viewAs === 'audience'}
          <button type="button" class="th-btn small" on:click={() => grant(WREN)} disabled={iHaveFloor}>Floor goes to you</button>
        {/if}
        <a href="/mocks/townhalls" class="th-note">All screens</a>
      </div>

      {#if th}
        <header class="th-card head">
          <div class="chips">
            <span class="th-chip live"><span class="th-dot"></span>LIVE</span>
            <span class="th-chip">⏱ {clock(th.startedAt)} / {th.maxMinutes} min</span>
            {#if th.recording}
              <span class="th-chip rec" title={recorders.map((r) => nameOf(r.userID)).join(', ')}>
                ● Recording on {recorders.length} admins' devices
              </span>
            {/if}
            <span class="th-chip primary">🌐 Open to connected servers</span>
            <span class="th-chip">👥 {total} / {th.capacity}</span>
            {#if th.turnSeconds}<span class="th-chip">Turns: {th.turnSeconds / 60} min</span>{/if}
          </div>
          <h1 class="topic">{th.topic}</h1>
          <p class="th-hint">
            Hosted by <strong>{nameOf(th.host)}</strong> · moderated by <strong>{nameOf(th.moderator)}</strong> ·
            organized by {nameOf(th.organizer)}
          </p>
        </header>

        {#if moderatorGone}
          <div class="banner warn">⚠ heron, the moderator, disconnected. <strong>kestrel</strong>, the host, runs the queue until they're back.</div>
        {/if}

        <div class="layout">
          <section class="main">
            <h2 class="th-section">On stage</h2>
            <div class="stage">
              {#each stage as a (a.userID)}
                {@const isFloor = a.userID === floorID}
                {@const talking = speaking.has(a.userID) && ((isFloor && floorMicOn) || (!isFloor && !a.muted))}
                <div class="tile" class:talking class:floor={isFloor} class:away={a.reconnecting}>
                  <div class="avatar-ring">
                    <Avatar userID={a.userID} username={nameOf(a.userID)} size="64px" />
                    {#if talking}<span class="waves"><i></i><i></i><i></i></span>{/if}
                  </div>
                  <div class="tile-name">
                    {nameOf(a.userID)}{#if a.userID === meID}<span class="you"> (you)</span>{/if}
                  </div>
                  <div class="tile-meta">
                    {#if isFloor}
                      <span class="th-chip ok">Has the floor</span>
                    {:else}
                      <span class="th-chip" class:primary={a.role === 'host' || a.role === 'moderator'}>
                        {roleLabel[a.role]}{a.guest ? ' · guest' : ''}
                      </span>
                    {/if}
                  </div>
                  <div class="tile-status">
                    <span class="th-server" class:peer={serverOf(a.userID) !== HOME}>@{serverOf(a.userID)}</span>
                    <span title={(isFloor ? !floorMicOn : a.muted) ? 'Muted' : 'Mic on'}>
                      {(isFloor ? !floorMicOn : a.muted) ? '🔇' : '🎙️'}
                    </span>
                    {#if people[a.userID]?.admin && th.recording}<span class="rec-dot" title="Recording on this device">●</span>{/if}
                  </div>
                  {#if a.reconnecting}<div class="th-hint">reconnecting…</div>{/if}
                  {#if isModerator && a.userID !== meID}
                    <div class="tile-actions">
                      <button type="button" class="th-btn small" on:click={() => muteOther(a)} disabled={isFloor ? !floorMicOn : a.muted}>Mute</button>
                      <button type="button" class="th-btn small danger" on:click={() => (removing = a)}>Remove</button>
                    </div>
                  {/if}
                </div>
              {/each}
            </div>

            {#if floorID}
              <div class="floor-bar">
                <div class="floor-text">
                  <strong>{nameOf(floorID)}</strong> has the floor
                  {#if th.turnSeconds}<span class="th-hint">· {mmss(floorLeft)} left</span>{/if}
                  {#if !floorMicOn}<span class="th-hint">· mic off, waiting for them to turn it on</span>{/if}
                </div>
                {#if th.turnSeconds}
                  <div class="progress"><span style="width: {(floorLeft / th.turnSeconds) * 100}%" class:low={floorLeft < 20}></span></div>
                {/if}
                {#if isModerator || (moderatorGone && viewAs !== 'audience')}
                  <button type="button" class="th-btn small danger" on:click={revoke}>Take the floor back</button>
                {/if}
              </div>
            {/if}

            <div class="th-card controls">
              {#if !joined}
                <p class="th-hint">You're not in yet.</p>
              {:else if iHaveFloor}
                <div class="my-floor">
                  <strong>You have the floor.</strong>
                  <span class="th-hint">{floorMicOn ? 'Everyone can hear you.' : 'Nobody can turn your mic on but you.'}</span>
                </div>
                <button type="button" class="th-btn" class:primary={!floorMicOn} on:click={toggleMyMic}>
                  {floorMicOn ? '🔇 Mute' : '🎙️ Turn on my mic'}
                </button>
                <button type="button" class="th-btn" on:click={done}>✓ Done</button>
                {#if th.turnSeconds}<span class="th-hint">{mmss(floorLeft)} left</span>{/if}
              {:else if viewAs === 'audience'}
                <button type="button" class="th-btn" class:primary={!myPosition} on:click={raiseHand}>
                  {myPosition ? '✋ Lower hand' : '✋ Raise hand'}
                </button>
                <span class="th-hint">
                  {#if myPosition}
                    You're <strong>number {myPosition}</strong> in line. {queueRunner === HERON ? 'heron' : 'kestrel'} will give you the floor.
                  {:else}
                    Listening. Your mic stays off: the server only forwards your audio once you have the floor.
                  {/if}
                </span>
              {:else}
                <button type="button" class="th-btn" class:primary={me?.muted} on:click={toggleMyMic}>
                  {me?.muted ? '🎙️ Unmute' : '🔇 Mute'}
                </button>
                <span class="th-hint">Your mic is always open as {viewAs}. Jump in whenever you want.</span>
              {/if}
              <span class="spacer"></span>
              {#if isModerator}
                <button type="button" class="th-btn danger" on:click={() => goto('/mocks/townhalls/archive')}>End town hall</button>
              {/if}
              <button type="button" class="th-btn" on:click={leave}>Leave</button>
            </div>

            <h2 class="th-section">
              Queue <span class="th-chip">{queue.length}</span>
              {#if moderatorGone}<span class="th-hint">· kestrel is running it</span>{/if}
            </h2>
            {#if queue.length === 0}
              <p class="th-hint">No hands raised.</p>
            {:else}
              <ol class="queue">
                {#each queue as id, i (id)}
                  {@const a = attendees.find((x) => x.userID === id)}
                  <li class:me={id === WREN}>
                    <span class="pos">{i + 1}</span>
                    <Avatar userID={id} username={nameOf(id)} size="28px" />
                    <span class="q-name">
                      {nameOf(id)}{#if id === WREN}<span class="you"> (you)</span>{/if}
                      <span class="th-server" class:peer={serverOf(id) !== HOME}>@{serverOf(id)}</span>
                      {#if a?.reconnecting}<span class="th-hint">· reconnecting, keeps their place</span>{/if}
                    </span>
                    {#if isModerator}
                      <span class="q-actions">
                        <button type="button" class="th-btn small" on:click={() => move(i, -1)} disabled={i === 0} aria-label="Move up">↑</button>
                        <button type="button" class="th-btn small" on:click={() => move(i, 1)} disabled={i === queue.length - 1} aria-label="Move down">↓</button>
                        <button type="button" class="th-btn small primary" on:click={() => grant(id)}>Give floor</button>
                      </span>
                    {:else}
                      <span class="hand">✋</span>
                    {/if}
                  </li>
                {/each}
              </ol>
            {/if}

            <p class="th-hint relay">
              🔊 Audio is relayed by {HOME} and never stored. The server forwards only open mics and whoever has the floor, so
              a modified client gains nothing by unmuting. Each peer gets <strong>one stream per speaker</strong> and passes it
              to its own users.
            </p>
          </section>

          <aside class="side th-card">
            <div class="tabs">
              <button type="button" class:active={tab === 'people'} on:click={() => (tab = 'people')}>👥 People <span class="th-chip">{total}</span></button>
              <button type="button" class:active={tab === 'chat'} on:click={() => ((tab = 'chat'), (unseenChat = 0))}>
                💬 Chat {#if unseenChat}<span class="th-chip live">{unseenChat}</span>{/if}
              </button>
            </div>

            {#if tab === 'people'}
              <div class="people">
                {#each byServer as g (g.server.id)}
                  <div class="server-group">
                    <div class="server-head">
                      <span class="server-name">{g.server.home ? '🏠' : '🌐'} {g.server.id}</span>
                      <span class="th-hint">
                        {g.shown.length + g.others}{g.server.home ? ' · home' : ' · via 1 stream per speaker'}
                      </span>
                    </div>
                    {#each g.shown as a (a.userID)}
                      {@const qpos = queue.indexOf(a.userID) + 1}
                      <div class="person" class:away={a.reconnecting}>
                        <Avatar userID={a.userID} username={nameOf(a.userID)} size="26px" />
                        <span class="p-name">
                          {nameOf(a.userID)}{#if a.userID === meID}<span class="you"> (you)</span>{/if}
                        </span>
                        {#if a.role !== 'audience'}<span class="th-chip primary">{roleLabel[a.role]}</span>{/if}
                        {#if a.userID === floorID}<span class="th-chip ok">floor</span>{/if}
                        {#if qpos}<span class="th-chip">✋ {qpos}</span>{/if}
                        {#if speaking.has(a.userID)}<span class="mini-wave">▮▮▮</span>{/if}
                        {#if isModerator && a.userID !== meID}
                          <button type="button" class="kick" title="Remove from town hall" on:click={() => (removing = a)}>✕</button>
                        {/if}
                      </div>
                    {/each}
                    {#if g.others}
                      <div class="th-hint more">+ {g.others} more listening</div>
                    {/if}
                  </div>
                {/each}
              </div>
            {:else}
              <div class="chat-head">
                <span class="th-hint">Signed by each author, checked before it's shown. {th.recording ? 'Published with the recording.' : 'Gone when it ends.'}</span>
                {#if isModerator}
                  <label class="switch"><input type="checkbox" checked={chatOn} on:change={toggleChat} /> On</label>
                {/if}
              </div>
              <div class="chat" use:stickToBottom={messages.length}>
                {#each messages as m (m.id)}
                  {#if m.kind === 'system'}
                    <div class="sys">{m.content}</div>
                  {:else}
                    <div class="msg" class:mine={m.from === meID}>
                      <Avatar userID={m.from} username={nameOf(m.from)} size="24px" />
                      <div>
                        <div class="msg-head">
                          <strong>{nameOf(m.from)}</strong>
                          <span class="th-server" class:peer={serverOf(m.from) !== HOME}>@{serverOf(m.from)}</span>
                          <span class="verified" title="Signature verified">✓</span>
                          <span class="th-hint">{new Date(m.at).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })}</span>
                        </div>
                        <div class="msg-body">
                          {#each withMentions(m.content) as part}
                            {#if part.startsWith('@')}<span class="mention">{part}</span>{:else}{part}{/if}
                          {/each}
                        </div>
                      </div>
                    </div>
                  {/if}
                {/each}
              </div>
              {#if chatOn}
                <form class="composer" on:submit|preventDefault={send}>
                  <input bind:value={draft} placeholder={joined ? 'Say something…' : 'Join to chat'} disabled={!joined} />
                  <span class="counter" class:over={chatCount > CHAT_MAX}>{CHAT_MAX - chatCount}</span>
                  <button type="submit" class="th-btn primary small" disabled={!canSend || !joined}>Send</button>
                </form>
              {:else}
                <p class="th-hint off">The moderator turned the chat off.</p>
              {/if}
            {/if}
          </aside>
        </div>
      {/if}
    </div>
    <MockToolbar currentPage="townhalls" />
  </div>

{#if !joined && th}
  <div class="th-modal-backdrop">
    <div class="th-modal" role="dialog" aria-modal="true">
      <h2>● This town hall is being recorded</h2>
      <p>“{th.topic}”</p>
      <ul class="consent">
        <li>If you speak, your voice is in the recording.</li>
        <li>Your chat messages are published with it.</li>
        <li>It's recorded on the admins' devices ({recorders.map((r) => nameOf(r.userID)).join(', ')}), never on the server.</li>
        <li>Everyone in the town hall sees that you're here.</li>
      </ul>
      <p class="th-hint">{total} of {th.capacity} seats taken.</p>
      <div class="th-modal-actions">
        <button type="button" class="th-btn" on:click={leave}>Not now</button>
        <button type="button" class="th-btn primary" on:click={() => (joined = true)}>Join and listen</button>
      </div>
    </div>
  </div>
{/if}

{#if removing}
  <div class="th-modal-backdrop" role="presentation" on:click|self={() => (removing = null)}>
    <div class="th-modal" role="dialog" aria-modal="true">
      <h2>Remove {nameOf(removing.userID)}?</h2>
      <p class="th-hint">
        The server stops forwarding their audio and chat at once, and they can't rejoin this town hall.
        {#if serverOf(removing.userID) !== HOME}{removing.userID.split('@')[1]} is told to stop passing it to them.{/if}
      </p>
      <div class="th-modal-actions">
        <button type="button" class="th-btn" on:click={() => (removing = null)}>Keep</button>
        <button type="button" class="th-btn danger" on:click={confirmRemove}>Remove</button>
      </div>
    </div>
  </div>
{/if}

{#if toast}<div class="th-toast">{toast}</div>{/if}

<style>
  .mock-bar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
    margin-bottom: 0.75rem;
    padding: 0.5rem;
    border: 1px dashed var(--border);
    border-radius: 8px;
  }

  .mock-bar .th-note {
    margin: 0;
  }

  .mock-bar a.th-note {
    margin-left: auto;
    color: var(--primary);
  }

  .sep {
    width: 1px;
    height: 1.25rem;
    background: var(--border);
  }

  .head .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 0.375rem;
  }

  .topic {
    margin: 0.5rem 0 0.25rem;
    font-size: 1.25rem;
  }

  .banner {
    margin-top: 0.75rem;
    padding: 0.5rem 0.75rem;
    border-radius: 8px;
    font-size: 0.85rem;
  }

  .banner.warn {
    border: 1px solid #d29922;
    background: rgba(210, 153, 34, 0.1);
  }

  .layout {
    display: grid;
    grid-template-columns: 1fr 340px;
    gap: 1rem;
    align-items: start;
  }

  @media (max-width: 960px) {
    .layout {
      grid-template-columns: 1fr;
    }
  }

  .stage {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(130px, 1fr));
    gap: 0.75rem;
  }

  .tile {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.25rem;
    padding: 0.75rem 0.5rem;
    border: 1px solid var(--border);
    border-radius: 12px;
    background: var(--surface);
    text-align: center;
    transition: border-color 0.2s ease;
  }

  .tile.floor {
    border-color: var(--success);
  }

  .tile.away {
    opacity: 0.5;
  }

  .avatar-ring {
    position: relative;
    border-radius: 50%;
    padding: 3px;
    border: 3px solid transparent;
    transition: border-color 0.15s ease, box-shadow 0.15s ease;
  }

  .tile.talking .avatar-ring {
    border-color: var(--success);
    box-shadow: 0 0 0 4px rgba(39, 174, 96, 0.25);
  }

  .waves {
    position: absolute;
    right: -6px;
    bottom: -2px;
    display: flex;
    align-items: flex-end;
    gap: 2px;
    padding: 3px 4px;
    border-radius: 6px;
    background: var(--success);
  }

  .waves i {
    width: 3px;
    height: 10px;
    background: #fff;
    border-radius: 2px;
    animation: wave 0.6s ease-in-out infinite;
  }

  .waves i:nth-child(2) {
    animation-delay: 0.15s;
  }

  .waves i:nth-child(3) {
    animation-delay: 0.3s;
  }

  @keyframes wave {
    50% {
      height: 3px;
    }
  }

  .tile-name {
    font-weight: 600;
    font-size: 0.9rem;
  }

  .you {
    color: var(--muted);
    font-weight: 400;
  }

  .tile-status {
    display: flex;
    align-items: center;
    gap: 0.375rem;
    font-size: 0.8rem;
  }

  .rec-dot {
    color: var(--error);
    font-size: 0.7rem;
  }

  .tile-actions {
    display: flex;
    gap: 0.25rem;
    margin-top: 0.25rem;
  }

  .floor-bar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem 0.75rem;
    margin-top: 0.75rem;
    padding: 0.5rem 0.75rem;
    border: 1px solid var(--success);
    border-radius: 8px;
    background: rgba(39, 174, 96, 0.08);
    font-size: 0.9rem;
  }

  .progress {
    flex: 1;
    min-width: 6rem;
    height: 0.375rem;
    border-radius: 999px;
    background: var(--border);
    overflow: hidden;
  }

  .progress span {
    display: block;
    height: 100%;
    background: var(--success);
    transition: width 1s linear;
  }

  .progress span.low {
    background: var(--error);
  }

  .controls {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem 0.75rem;
    margin-top: 0.75rem;
  }

  .my-floor {
    display: flex;
    flex-direction: column;
  }

  .spacer {
    flex: 1;
  }

  .queue {
    list-style: none;
    margin: 0;
    padding: 0;
    border: 1px solid var(--border);
    border-radius: 12px;
    background: var(--surface);
  }

  .queue li {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.5rem 0.75rem;
    border-bottom: 1px solid var(--border);
  }

  .queue li:last-child {
    border-bottom: none;
  }

  .queue li.me {
    background: rgba(88, 166, 255, 0.1);
  }

  .pos {
    width: 1.5rem;
    color: var(--muted);
    font-weight: 700;
    text-align: center;
  }

  .q-name {
    flex: 1;
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 0.375rem;
    min-width: 0;
  }

  .q-actions {
    display: flex;
    gap: 0.25rem;
  }

  .relay {
    margin-top: 1rem;
  }

  .side {
    display: flex;
    flex-direction: column;
    padding: 0;
    max-height: calc(100vh - 8rem);
    position: sticky;
    top: 4rem;
  }

  .tabs {
    display: flex;
    border-bottom: 1px solid var(--border);
  }

  .tabs button {
    flex: 1;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 0.375rem;
    padding: 0.75rem;
    border: none;
    border-bottom: 2px solid transparent;
    background: none;
    color: var(--muted);
    font: inherit;
    font-size: 0.85rem;
    font-weight: 600;
    cursor: pointer;
  }

  .tabs button.active {
    border-bottom-color: var(--primary);
    color: var(--fg);
  }

  .people {
    overflow-y: auto;
    padding: 0.5rem 0.75rem;
  }

  .server-group {
    margin-bottom: 0.75rem;
  }

  .server-head {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    padding: 0.25rem 0;
    border-bottom: 1px solid var(--border);
    margin-bottom: 0.25rem;
  }

  .server-name {
    font-size: 0.8rem;
    font-weight: 700;
  }

  .person {
    display: flex;
    align-items: center;
    gap: 0.375rem;
    padding: 0.25rem 0;
    font-size: 0.85rem;
  }

  .person.away {
    opacity: 0.5;
  }

  .p-name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .mini-wave {
    color: var(--success);
    font-size: 0.6rem;
    letter-spacing: -2px;
  }

  .kick {
    border: none;
    background: none;
    color: var(--muted);
    cursor: pointer;
  }

  .kick:hover {
    color: var(--error);
  }

  .more {
    padding-left: 2rem;
  }

  .chat-head {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.5rem 0.75rem;
    border-bottom: 1px solid var(--border);
  }

  .switch {
    display: inline-flex;
    align-items: center;
    gap: 0.25rem;
    font-size: 0.8rem;
    white-space: nowrap;
  }

  .chat {
    flex: 1;
    min-height: 16rem;
    overflow-y: auto;
    padding: 0.5rem 0.75rem;
  }

  .msg {
    display: flex;
    gap: 0.5rem;
    padding: 0.375rem 0;
    font-size: 0.85rem;
  }

  .msg.mine .msg-body {
    color: var(--primary);
  }

  .msg-head {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 0.25rem;
  }

  .verified {
    color: var(--success);
    font-size: 0.75rem;
  }

  .msg-body {
    word-break: break-word;
  }

  .mention {
    color: var(--primary);
  }

  .sys {
    margin: 0.375rem 0;
    color: var(--muted);
    font-size: 0.75rem;
    font-style: italic;
    text-align: center;
  }

  .composer {
    position: relative;
    display: flex;
    gap: 0.5rem;
    padding: 0.5rem 0.75rem;
    border-top: 1px solid var(--border);
  }

  .composer input {
    flex: 1;
    min-width: 0;
    padding: 0.5rem 2.5rem 0.5rem 0.625rem;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--input-bg);
    color: var(--fg);
    font: inherit;
    font-size: 0.85rem;
  }

  .counter {
    position: absolute;
    right: 5rem;
    top: 50%;
    transform: translateY(-50%);
    color: var(--muted);
    font-size: 0.7rem;
  }

  .counter.over {
    color: var(--error);
  }

  .off {
    padding: 0.75rem;
    border-top: 1px solid var(--border);
    text-align: center;
  }

  .consent {
    margin: 0.5rem 0;
    padding-left: 1.25rem;
    font-size: 0.9rem;
  }

  .consent li {
    margin: 0.25rem 0;
  }
</style>
