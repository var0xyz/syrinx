<script lang="ts">
  import { goto } from '$app/navigation';
</script>

<div class="container">
  <div class="card">
    <h1>Account Deleted</h1>
    <p class="subtitle">Thank you for using Syrinx</p>

    <div class="content">
      <div class="deleted-section">
        <h2>What was deleted:</h2>

        <div class="item">
          <h3>User profile</h3>
          <p>Your account and profile information have been permanently removed from the server.</p>
        </div>

        <div class="item">
          <h3>All local data</h3>
          <p>All local data including keys, reeds, and other stored information has been deleted.</p>
        </div>
      </div>

      <div class="retained-section">
        <h2>What was retained:</h2>
        <div class="item">
          <h3>Your public keys</h3>
          <p>Your public keys have been retained to prevent impersonation and maintain cryptographic integrity.</p>
        </div>
      </div>
    </div>

    <div class="action-buttons">
      <a href="/" class="btn btn-primary">Return to home</a>
    </div>
  </div>
</div>

<style>
  .container {
    max-width: 800px;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 1rem;
  }

  .card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 2rem;
    text-align: left;
  }

  .card h1 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
    font-size: 2rem;
    text-align: center;
  }

  .subtitle {
    margin: 0 0 1.5rem 0;
    color: var(--muted);
    font-size: 1.1rem;
    text-align: center;
  }

  .content {
    margin-bottom: 2rem;
  }

  .deleted-section,
  .retained-section {
    margin-bottom: 2rem;
  }

  .retained-section h2,
  .deleted-section h2 {
    font-size: 1.3rem;
    margin-bottom: 1rem;
  }

  .deleted-section h2 {
    color: #f59e0b;
  }

  .retained-section h2 {
    color: #10b981;
  }

  .retained-section .item,
  .deleted-section .item {
    margin-bottom: 1.5rem;
    padding: 1rem;
    border-radius: 8px;
  }

  .deleted-section .item {
    background: rgba(245, 158, 11, 0.1);
    border-left: 4px solid #f59e0b;
  }

  .retained-section .item {
    background: rgba(16, 185, 129, 0.1);
    border-left: 4px solid #10b981;
  }

  .deleted-section .item h3,
  .retained-section .item h3 {
    margin: 0 0 0.5rem;
  }

  .item p {
    margin: 0;
    line-height: 1.5;
  }

  .action-buttons {
    border-top: 1px solid var(--border);
    padding-top: 2rem;
    text-align: center;
    display: flex;
    gap: 1rem;
    justify-content: center;
  }

  .btn {
    display: inline-flex;
    align-items: center;
    padding: 0.75rem 1.5rem;
    border-radius: 8px;
    text-decoration: none;
    font-weight: 600;
    transition: all 0.2s ease;
    border: none;
    cursor: pointer;
    font-size: 0.95rem;
  }

  .btn-primary {
    background: var(--primary);
    color: var(--button-text);
  }

  .btn-primary:hover {
    opacity: 0.9;
    transform: translateY(-1px);
  }

  @media (max-width: 640px) {
    .container {
      padding: 0.5rem;
    }

    .card {
      padding: 1.5rem;
    }

    .card h1 {
      font-size: 1.5rem;
    }

    .action-buttons {
      flex-direction: column;
    }

    .btn {
      text-align: center;
      justify-content: center;
    }

    .deleted-section .item,
    .retained-section .item {
      padding: 0.75rem;
    }
  }
</style>

