<script>
  import { onMount } from 'svelte';
  import { authService } from '$lib/services/auth';

  let countdown = 5;
  let redirecting = true;

  onMount(async () => {
    try {
      await authService.logout();
    } catch (error) {
      console.error('Error during logout:', error);
    }
  });

</script>

<div class="container">
  <div class="logout-header">
    <h1>You've been logged out</h1>
    <p class="subtitle">Thank you for using Syrinx</p>
  </div>

  <div class="logout-content">
    <div class="logout-message">
      <h2>Session Ended</h2>
      <p>Your session has been successfully terminated. All your data remains secure and encrypted on your device.</p>
    </div>

    <div class="security-info">
      <h3>🔐 Your Data is Safe</h3>
      <ul>
        <li>Your private keys remain encrypted on your device</li>
        <li>No data was transmitted during logout</li>
        <li>Your account is secure and ready for next login</li>
      </ul>
    </div>
  </div>

  <div class="footer-info">
    <p>Want to log back in? <a href="/login">Sign in again</a></p>
  </div>
</div>

<style>
  .container {
    max-width: 600px;
    margin: 0 auto;
    padding: 1rem;
    min-height: calc(100vh - 4rem - 1px);
    display: flex;
    flex-direction: column;
    justify-content: center;
  }

  .logout-header {
    margin-bottom: 2rem;
  }

  .logout-header h1 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
    font-size: 1.8rem;
  }

  .subtitle {
    margin: 0;
    color: var(--muted);
    font-size: 1.1rem;
  }

  .logout-content {
    text-align: left;
  }

  .logout-message {
    margin-bottom: 2rem;
    padding: 1.5rem;
    background: var(--input-bg);
    border-radius: 8px;
    border-left: 4px solid var(--primary);
  }

  .logout-message h2 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
    font-size: 1.2rem;
  }

  .logout-message p {
    margin: 0;
    color: var(--muted);
    line-height: 1.5;
  }

  .security-info {
    margin-bottom: 2rem;
    padding: 1.5rem;
    background: rgba(76, 175, 80, 0.1);
    border: 1px solid rgba(76, 175, 80, 0.3);
    border-radius: 8px;
  }

  .security-info h3 {
    margin: 0 0 1rem 0;
    color: #4caf50;
    font-size: 1.1rem;
  }

  .security-info ul {
    margin: 0;
    padding-left: 1.5rem;
  }

  .security-info li {
    margin-bottom: 0.5rem;
    color: var(--fg);
    line-height: 1.4;
  }

  .footer-info {
    text-align: center;
    padding: 1rem;
  }

  .footer-info p {
    margin: 0;
    color: var(--muted);
  }

  .footer-info a {
    color: var(--primary);
    text-decoration: none;
  }

  .footer-info a:hover {
    text-decoration: underline;
  }

  @media (max-width: 768px) {
    .container {
      padding: 1.5rem;
    }

    .logout-header h1 {
      font-size: 1.5rem;
    }
  }
</style>
