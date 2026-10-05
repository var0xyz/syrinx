<script>
  import { people } from './data.js';
</script>

<main class="mock-index">
  <h1>Chats mockup</h1>
  <p class="lede">Static, UI-only. Nothing here is signed, encrypted, stored or sent.</p>
  <ul>
    <li><a href="/mocks/chats/profile?u=12@syrinx.local">Profile</a> — the Chat button next to Follow</li>
    <li><a href="/mocks/chats/profile?u=63@syrinx.local">Profile, no chat yet</a> — Chat opens an empty conversation</li>
    <li><a href="/mocks/chats/inbox">Chats list</a> — what the toolbar's Chats item opens</li>
    {#each Object.values(people) as person}
      <li><a href="/mocks/chats/with/{person.userID}">Chat with {person.username}</a></li>
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
