<script>
  import { thread } from './data.js';
</script>

<main class="mock-index">
  <h1>Threads mockup</h1>
  <p class="lede">Static, UI-only. Nothing here is signed, stored or published.</p>
  <ul>
    <li><a href="/mocks/threads/compose">Composer</a> — how a user turns a reed into a thread</li>
    <li><a href="/mocks/threads/feed">Feed</a> — how a thread shows up in a list</li>
    <li><a href="/mocks/threads/thread/{thread[0].id}">Thread page</a> — the whole thread, read-only</li>
    {#each thread as _, i}
      <li><a href="/mocks/threads/reed?n={i + 1}">Reed detail, {i + 1} of {thread.length}</a></li>
    {/each}
  </ul>
</main>

<style>
  .mock-index {
    max-width: 560px;
    margin: 3rem auto;
    padding: 0 1rem;
    color: var(--fg);
  }

  .lede {
    color: var(--muted);
  }

  li {
    margin: 0.5rem 0;
  }

  a {
    color: var(--primary);
  }
</style>
