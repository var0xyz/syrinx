<script>
  import { onMount, onDestroy } from 'svelte';

  export let username = '';

  let status = 'idle'; // 'idle', 'checking', 'available', 'taken', 'error'
  let message = '';
  let checkTimeout = null;
  let currentCheck = null;
  let previousUsername = '';

  function resetStatus() {
    status = 'idle';
    message = '';
  }

  function debouncedUsernameCheck(username) {
    // Clear existing timeout
    if (checkTimeout !== null) {
      clearTimeout(checkTimeout);
    }

    // Cancel previous request if still pending
    if (currentCheck) {
      currentCheck.abort();
    }

    // Set new timeout
    checkTimeout = setTimeout(() => {
      checkUsernameAvailability(username);
    }, 200);
  }

  async function checkUsernameAvailability(username) {
    if (username.length === 0) {
      resetStatus();
      return;
    }

    // Create new abort controller for this request
    currentCheck = new AbortController();
    status = 'checking';
    message = 'Checking availability...';

    try {
      const formData = new URLSearchParams();
      formData.append('username', username);

      const response = await fetch('/api/check-username', {
        method: 'POST',
        body: formData,
        headers: {
          'Accept': 'application/json',
          'Content-Type': 'application/x-www-form-urlencoded'
        },
        credentials: 'include',
        signal: currentCheck.signal
      });

      if (response.ok) {
        status = 'available';
        message = 'Username is available';
      } else {
        const errorData = await response.json();
        status = 'taken';
        message = 'Username is taken';
      }
    } catch (error) {
      if (error instanceof Error && error.name !== 'AbortError') {
        console.error('Username check error:', error);
        status = 'error';
        message = 'Error checking username';
      }
    } finally {
      currentCheck = null;
    }
  }

  // Watch for username changes manually
  function handleUsernameChange() {
    if (username !== previousUsername) {
      previousUsername = username;

      if (username && username.length > 0) {
        // Reset error state when username changes
        if (status === 'error') {
          resetStatus();
        }
        debouncedUsernameCheck(username);
      } else {
        if (checkTimeout !== null) {
          clearTimeout(checkTimeout);
        }
        if (currentCheck) {
          currentCheck.abort();
          currentCheck = null;
        }
        resetStatus();
      }
    }
  }

  // Use a simple interval to check for changes instead of reactive statements
  let intervalId;

  onMount(() => {
    intervalId = setInterval(handleUsernameChange, 100);
  });

  onDestroy(() => {
    if (intervalId) {
      clearInterval(intervalId);
    }
    if (checkTimeout !== null) {
      clearTimeout(checkTimeout);
    }
    if (currentCheck) {
      currentCheck.abort();
    }
  });
</script>

<div class="username-status" class:status-checking={status === 'checking'} class:status-available={status === 'available'} class:status-taken={status === 'taken'} class:status-error={status === 'error'}>
  {message}
</div>

<style>
  .username-status {
    font-size: 0.75rem;
    margin-top: 0.25rem;
    transition: color 0.3s ease;
  }

  .username-status.status-checking {
    color: var(--muted);
  }

  .username-status.status-available {
    color: #66bb6a;
  }

  .username-status.status-taken {
    color: var(--error);
  }

  .username-status.status-error {
    color: var(--error);
  }
</style>
