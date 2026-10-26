<script>
  import { onDestroy, tick } from 'svelte';
  import Avatar from '$lib/components/Avatar.svelte';
  import MockSideNav from '../MockSideNav.svelte';
  import MockToolbar from '../MockToolbar.svelte';
  import { townHalls, transcript, archiveChat, recordings, backup, nameOf, serverOf, people, me, HOME } from '../data.js';
  import '../townhalls.css';

  $: th = $townHalls.find((t) => t.id === 'th-archived');
  $: total = th?.durationSeconds ?? 1;

  let position = 0;
  let playing = false;
  let speed = 1;
  /** @type {'transcript' | 'chat'} */
  let tab = 'transcript';
  let query = '';
  let follow = true;
  let kept = recordings.map((r) => r.kept);
  let toast = '';

  // Mock playback: no audio, just a clock that advances.
  const timer = setInterval(() => {
    if (!playing) return;
    position = Math.min(total, position + speed);
    if (position >= total) playing = false;
  }, 1000);
  onDestroy(() => clearInterval(timer));

  /** @param {number} s */
  const mmss = (s) => `${Math.floor(s / 60)}:${String(Math.floor(s % 60)).padStart(2, '0')}`;

  $: current = transcript.reduce((idx, seg, i) => (seg.at <= position ? i : idx), 0);
  $: segments = transcript.map((seg, i) => ({ ...seg, end: transcript[i + 1]?.at ?? total }));
  $: shown = query.trim()
    ? segments.filter((s) => s.text.toLowerCase().includes(query.trim().toLowerCase()) || nameOf(s.speaker).includes(query.trim().toLowerCase()))
    : segments;
  $: chatSoFar = archiveChat.filter((m) => m.at <= position);

  const speakers = [...new Set(transcript.map((s) => s.speaker))];
  const palette = ['#58a6ff', '#27ae60', '#d29922', '#bc8cff', '#ff7b72', '#39c5cf', '#f778ba', '#a5d6ff'];
  /** @param {string} id */
  const colorOf = (id) => palette[speakers.indexOf(id) % palette.length];

  /** @param {number} s */
  function seek(s) {
    position = s;
    follow = true;
  }

  /** @param {MouseEvent} e */
  function seekBar(e) {
    const el = /** @type {HTMLElement} */ (e.currentTarget);
    const rect = el.getBoundingClientRect();
    seek(Math.round(((e.clientX - rect.left) / rect.width) * total));
  }

  /** @param {number} delta */
  const skip = (delta) => seek(Math.max(0, Math.min(total, position + delta)));

  /** @param {HTMLElement} node @param {number} idx */
  function keepCurrentInView(node, idx) {
    const scroll = () =>
      tick().then(() => {
        if (!follow || query) return;
        node.querySelector('.seg.current')?.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
      });
    scroll();
    return { update: scroll };
  }

  /** @param {string} text */
  function flash(text) {
    toast = text;
    setTimeout(() => (toast = ''), 2600);
  }

  /** @param {number} i */
  function deleteOwn(i) {
    kept[i] = false;
    flash('Deleted from your device. The published recording is untouched.');
  }

  /** @param {string} text @param {string} q */
  function highlight(text, q) {
    if (!q.trim()) return [{ t: text, hit: false }];
    const re = new RegExp(`(${q.trim().replace(/[.*+?^${}()|[\]\\]/g, '\\$&')})`, 'gi');
    return text.split(re).map((t) => ({ t, hit: t.toLowerCase() === q.trim().toLowerCase() }));
  }

  /** @param {string} text */
  const withMentions = (text) => text.split(/(@\w+)/g);
</script>

<MockSideNav currentPage="townhalls" />
<div class="th-container">
  <div class="th-content">
    <p class="th-note">
      Town halls mockup: a past town hall. The chosen recording, published with its transcript and side chat. Playback is
      simulated. <a href="/mocks/townhalls">All screens</a>
    </p>

    {#if th}
      <a href="/mocks/townhalls/list" class="th-link back">← Town halls</a>

      <header class="head">
        <div class="chips">
          <span class="th-chip">Ended</span>
          <span class="th-chip ok" title="Signed by heron, countersigned by {HOME}">✓ Signature verified</span>
          <span class="th-chip">🌐 Was open to connected servers</span>
        </div>
        <h1 class="topic">{th.topic}</h1>
        <p class="th-hint">
          {new Date(th.startedAt).toLocaleString(undefined, { weekday: 'long', day: 'numeric', month: 'long', hour: '2-digit', minute: '2-digit' })}
          · {mmss(total)} · {th.attended} attended from {th.attendedServers} servers
        </p>
        <p class="th-hint">Hosted by <strong>{nameOf(th.host)}</strong> · moderated by <strong>{nameOf(th.moderator)}</strong></p>
      </header>

      <section class="th-card player">
        <div class="now">
          <Avatar userID={transcript[current].speaker} username={nameOf(transcript[current].speaker)} size="40px" />
          <div class="now-text">
            <span class="th-hint">Now speaking</span>
            <strong style="color: {colorOf(transcript[current].speaker)}">{nameOf(transcript[current].speaker)}</strong>
          </div>
          {#if playing}<span class="eq"><i></i><i></i><i></i><i></i></span>{/if}
        </div>

        <!-- Each band is one speaker's stretch; click anywhere to seek. -->
        <div class="timeline" role="slider" tabindex="0" aria-valuemin="0" aria-valuemax={total} aria-valuenow={position}
          on:click={seekBar}
          on:keydown={(e) => (e.key === 'ArrowRight' ? skip(15) : e.key === 'ArrowLeft' ? skip(-15) : null)}>
          {#each segments as seg}
            <span class="band" style="left: {(seg.at / total) * 100}%; width: {((seg.end - seg.at) / total) * 100}%; background: {colorOf(seg.speaker)}" title="{nameOf(seg.speaker)} · {mmss(seg.at)}"></span>
          {/each}
          {#each archiveChat as m}
            <span class="chat-tick" style="left: {(m.at / total) * 100}%" title="Chat: {nameOf(m.from)}"></span>
          {/each}
          <span class="played" style="width: {(position / total) * 100}%"></span>
          <span class="head-pin" style="left: {(position / total) * 100}%"></span>
        </div>

        <div class="transport">
          <span class="time">{mmss(position)}</span>
          <button type="button" class="th-btn small" on:click={() => skip(-15)} aria-label="Back 15 seconds">↺ 15</button>
          <button type="button" class="play" on:click={() => (playing = !playing)} aria-label={playing ? 'Pause' : 'Play'}>
            {playing ? '❚❚' : '▶'}
          </button>
          <button type="button" class="th-btn small" on:click={() => skip(15)} aria-label="Forward 15 seconds">15 ↻</button>
          <span class="time">{mmss(total)}</span>
          <span class="spacer"></span>
          <select bind:value={speed} aria-label="Speed">
            {#each [1, 1.25, 1.5, 2] as s}<option value={s}>{s}×</option>{/each}
          </select>
          <button type="button" class="th-btn small" on:click={() => flash('Saved: recording, transcript and chat (5.7 MB).')}>⬇ Download</button>
        </div>

        <div class="legend">
          {#each speakers as id}
            <span><i style="background: {colorOf(id)}"></i>{nameOf(id)}{#if serverOf(id) !== HOME}<span class="th-server peer"> @{serverOf(id)}</span>{/if}</span>
          {/each}
          <span><i class="tick"></i>chat message</span>
        </div>
      </section>

      <section class="th-card reader">
        <div class="tabs">
          <button type="button" class:active={tab === 'transcript'} on:click={() => (tab = 'transcript')}>📝 Transcript</button>
          <button type="button" class:active={tab === 'chat'} on:click={() => (tab = 'chat')}>
            💬 Chat <span class="th-chip">{chatSoFar.length}/{archiveChat.length}</span>
          </button>
        </div>

        {#if tab === 'transcript'}
          <div class="reader-tools">
            <input bind:value={query} placeholder="Search the transcript" />
            <label class="follow"><input type="checkbox" bind:checked={follow} /> Follow along</label>
          </div>
          <div class="segs" use:keepCurrentInView={current}>
            {#each shown as seg (seg.at)}
              <button type="button" class="seg" class:current={seg.at === transcript[current].at} class:past={seg.end <= position} on:click={() => seek(seg.at)}>
                <span class="seg-time">{mmss(seg.at)}</span>
                <span class="seg-body">
                  <strong style="color: {colorOf(seg.speaker)}">{nameOf(seg.speaker)}</strong>
                  {#if seg.speaker === th.host}<span class="th-chip primary">Host</span>{/if}
                  {#if seg.speaker === th.moderator}<span class="th-chip primary">Moderator</span>{/if}
                  {#if serverOf(seg.speaker) !== HOME}<span class="th-server peer">@{serverOf(seg.speaker)}</span>{/if}
                  <span class="seg-text">
                    {#each highlight(seg.text, query) as part}{#if part.hit}<mark>{part.t}</mark>{:else}{part.t}{/if}{/each}
                  </span>
                </span>
              </button>
            {:else}
              <p class="th-hint empty">Nothing matches “{query}”.</p>
            {/each}
          </div>
          <p class="th-hint foot">Transcribed on heron's device when the recording was published. It may have mistakes.</p>
        {:else}
          <div class="segs">
            {#if chatSoFar.length === 0}
              <p class="th-hint empty">The chat replays as you listen. Nothing yet at {mmss(position)}.</p>
            {/if}
            {#each chatSoFar as m (m.at)}
              <button type="button" class="seg chat-msg" on:click={() => seek(m.at)}>
                <span class="seg-time">{mmss(m.at)}</span>
                <Avatar userID={m.from} username={nameOf(m.from)} size="24px" />
                <span class="seg-body">
                  <strong>{nameOf(m.from)}</strong>
                  <span class="th-server" class:peer={serverOf(m.from) !== HOME}>@{serverOf(m.from)}</span>
                  <span class="verified" title="Signed by its author">✓</span>
                  <span class="seg-text">
                    {#each withMentions(m.content) as part}{#if part.startsWith('@')}<span class="mention">{part}</span>{:else}{part}{/if}{/each}
                  </span>
                </span>
              </button>
            {/each}
          </div>
          <p class="th-hint foot">Each message is signed by its author and was published with the recording, timestamped.</p>
        {/if}
      </section>

      <h2 class="th-section">About this recording</h2>
      <section class="th-card about">
        <dl>
          <dt>Recorded by</dt>
          <dd>heron's device, chosen by heron (organizer)</dd>
          <dt>Signed</dt>
          <dd>by heron, countersigned by {HOME} <span class="th-chip ok">✓ verified</span></dd>
          <dt>Format</dt>
          <dd>Mono Opus, 16 kbps · 5.7 MB</dd>
          <dt>Kept by</dt>
          <dd>Every admin ({backup.admins} devices)</dd>
          <dt>Backup</dt>
          <dd>
            {backup.chunks} chunks of 256 KB, {backup.copies} copies each, across {backup.holders} members here and on peers
            <span class="th-hint">· {backup.availability} available without admins</span>
          </dd>
          <dt>Your share</dt>
          <dd>
            <div class="share">
              <div class="share-bar"><span style="width: {(backup.mineMB / backup.capMB) * 100}%"></span></div>
              <span class="th-hint">{backup.mine} chunks · {backup.mineMB} of {backup.capMB} MB</span>
            </div>
          </dd>
        </dl>
        <p class="th-hint">
          The server never had the audio. When you play it, it comes from a member who holds it, and from an admin only if none is online.
        </p>
      </section>

      {#if me.admin}
        <h2 class="th-section">Recordings made <span class="th-hint">· admins only</span></h2>
        <section class="th-card recs">
          <p class="th-hint">Every admin present recorded on their own device. Each one reported what it covered, and the organizer picked one. The others are never deleted automatically.</p>
          {#each recordings as r, i}
            <div class="rec" class:chosen={r.chosen}>
              <Avatar userID={r.by} username={nameOf(r.by)} size="28px" />
              <div class="rec-main">
                <div class="rec-head">
                  <strong>{nameOf(r.by)}'s device</strong>
                  {#if r.chosen}<span class="th-chip ok">Published</span>{/if}
                  {#if !kept[i]}<span class="th-chip">Deleted by its admin</span>{/if}
                  <span class="th-hint">{r.sizeMB} MB</span>
                </div>
                <div class="coverage" title={r.gap ?? 'Complete'}>
                  {#each r.covered as [from, to]}
                    <span style="left: {from * 100}%; width: {(to - from) * 100}%"></span>
                  {/each}
                </div>
                <span class="th-hint">{r.gap ?? 'Complete, no gaps'}</span>
              </div>
              {#if r.by === me.userID && kept[i] && !r.chosen}
                <button type="button" class="th-btn small danger" on:click={() => deleteOwn(i)}>Delete mine</button>
              {/if}
            </div>
          {/each}
        </section>
      {/if}

      <h2 class="th-section">Who was there</h2>
      <section class="th-card attended">
        <div class="servers">
          <span class="th-chip">🏠 {HOME} · 39</span>
          <span class="th-chip">🌐 other.example · 11</span>
          <span class="th-chip">🌐 birdhouse.net · 8</span>
        </div>
        <div class="stack">
          {#each Object.keys(people).slice(0, 14) as id}
            <span class="stack-item"><Avatar userID={id} username={nameOf(id)} size="28px" /></span>
          {/each}
          <span class="th-hint more">+{th.attended - 14}</span>
        </div>
        <p class="th-hint">Everyone who attended or RSVP'd was notified when this was published.</p>
      </section>
    {/if}
  </div>
  <MockToolbar currentPage="townhalls" />
</div>

{#if toast}<div class="th-toast">{toast}</div>{/if}

<style>
  .back {
    display: inline-block;
    margin-bottom: 0.75rem;
    font-size: 0.85rem;
    text-decoration: none;
  }

  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 0.375rem;
  }

  .topic {
    margin: 0.5rem 0 0.25rem;
    font-size: 1.3rem;
  }

  .head p {
    margin: 0.125rem 0;
  }

  .player {
    margin-top: 1rem;
  }

  .now {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    margin-bottom: 0.75rem;
  }

  .now-text {
    display: flex;
    flex-direction: column;
  }

  .eq {
    display: flex;
    align-items: flex-end;
    gap: 3px;
    height: 1.25rem;
    margin-left: auto;
  }

  .eq i {
    width: 4px;
    height: 100%;
    border-radius: 2px;
    background: var(--success);
    animation: eq 0.8s ease-in-out infinite;
  }

  .eq i:nth-child(2) {
    animation-delay: 0.2s;
  }

  .eq i:nth-child(3) {
    animation-delay: 0.4s;
  }

  .eq i:nth-child(4) {
    animation-delay: 0.1s;
  }

  @keyframes eq {
    50% {
      height: 25%;
    }
  }

  .timeline {
    position: relative;
    height: 2rem;
    border-radius: 6px;
    background: var(--input-bg);
    overflow: hidden;
    cursor: pointer;
  }

  .band {
    position: absolute;
    top: 0.5rem;
    bottom: 0.5rem;
    opacity: 0.55;
  }

  .chat-tick {
    position: absolute;
    top: 0;
    width: 2px;
    height: 0.375rem;
    background: var(--fg);
    opacity: 0.6;
  }

  .played {
    position: absolute;
    inset: 0 auto 0 0;
    background: rgba(255, 255, 255, 0.08);
    pointer-events: none;
  }

  .head-pin {
    position: absolute;
    top: 0;
    bottom: 0;
    width: 2px;
    background: var(--fg);
    pointer-events: none;
  }

  .transport {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
    margin-top: 0.75rem;
  }

  .time {
    font-variant-numeric: tabular-nums;
    color: var(--muted);
    font-size: 0.8rem;
  }

  .play {
    width: 2.75rem;
    height: 2.75rem;
    border: none;
    border-radius: 50%;
    background: var(--primary);
    color: var(--button-text);
    font-size: 1rem;
    cursor: pointer;
  }

  .spacer {
    flex: 1;
  }

  select,
  .reader-tools input {
    padding: 0.25rem 0.5rem;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--input-bg);
    color: var(--fg);
    font: inherit;
    font-size: 0.8rem;
  }

  .legend {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem 0.75rem;
    margin-top: 0.75rem;
    font-size: 0.75rem;
    color: var(--muted);
  }

  .legend span {
    display: inline-flex;
    align-items: center;
    gap: 0.25rem;
  }

  .legend i {
    width: 0.625rem;
    height: 0.625rem;
    border-radius: 2px;
  }

  .legend i.tick {
    width: 2px;
    background: var(--fg);
  }

  .reader {
    margin-top: 1rem;
    padding: 0;
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

  .reader-tools {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 0.5rem 0.75rem;
    border-bottom: 1px solid var(--border);
  }

  .reader-tools input {
    flex: 1;
    min-width: 0;
    padding: 0.375rem 0.5rem;
  }

  .follow {
    display: inline-flex;
    align-items: center;
    gap: 0.25rem;
    font-size: 0.8rem;
    color: var(--muted);
    white-space: nowrap;
  }

  .segs {
    max-height: 24rem;
    overflow-y: auto;
    padding: 0.25rem;
  }

  .seg {
    display: flex;
    align-items: flex-start;
    gap: 0.75rem;
    width: 100%;
    padding: 0.5rem;
    border: none;
    border-left: 3px solid transparent;
    border-radius: 6px;
    background: none;
    color: var(--fg);
    font: inherit;
    font-size: 0.9rem;
    text-align: left;
    cursor: pointer;
  }

  .seg:hover {
    background: var(--input-bg);
  }

  .seg.past {
    color: var(--muted);
  }

  .seg.current {
    border-left-color: var(--primary);
    background: rgba(88, 166, 255, 0.1);
    color: var(--fg);
  }

  .seg-time {
    flex-shrink: 0;
    width: 2.75rem;
    padding-top: 0.125rem;
    color: var(--muted);
    font-size: 0.75rem;
    font-variant-numeric: tabular-nums;
  }

  .seg-body {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 0.25rem 0.375rem;
    min-width: 0;
  }

  .seg-text {
    flex-basis: 100%;
    line-height: 1.45;
  }

  .chat-msg .seg-text {
    flex-basis: 100%;
  }

  mark {
    border-radius: 2px;
    background: #d29922;
    color: #000;
  }

  .verified {
    color: var(--success);
    font-size: 0.75rem;
  }

  .mention {
    color: var(--primary);
  }

  .empty {
    padding: 1rem;
    text-align: center;
  }

  .foot {
    padding: 0.5rem 0.75rem;
    border-top: 1px solid var(--border);
  }

  .about dl {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 0.5rem 1rem;
    margin: 0 0 0.75rem;
    font-size: 0.875rem;
  }

  .about dt {
    color: var(--muted);
  }

  .about dd {
    margin: 0;
  }

  .share {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .share-bar {
    width: 8rem;
    height: 0.375rem;
    border-radius: 999px;
    background: var(--border);
    overflow: hidden;
  }

  .share-bar span {
    display: block;
    height: 100%;
    background: var(--primary);
  }

  .recs {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }

  .rec {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 0.5rem;
    border: 1px solid var(--border);
    border-radius: 8px;
  }

  .rec.chosen {
    border-color: var(--success);
  }

  .rec-main {
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    min-width: 0;
  }

  .rec-head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.375rem;
    font-size: 0.875rem;
  }

  .coverage {
    position: relative;
    height: 0.5rem;
    border-radius: 999px;
    background: rgba(255, 107, 107, 0.35);
    overflow: hidden;
  }

  .coverage span {
    position: absolute;
    top: 0;
    bottom: 0;
    background: var(--success);
  }

  .attended {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }

  .servers {
    display: flex;
    flex-wrap: wrap;
    gap: 0.375rem;
  }

  .stack {
    display: flex;
    align-items: center;
  }

  .stack-item {
    display: inline-flex;
    margin-right: -0.5rem;
    border: 2px solid var(--surface);
    border-radius: 50%;
  }

  .more {
    margin-left: 0.875rem;
  }
</style>
