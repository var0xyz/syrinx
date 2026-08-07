<script>
  import { onMount, onDestroy } from 'svelte';
  import { apiService } from '$lib/services/api';

  export let username = '';
  /** Opaque form fields forwarded to POST /check-username (e.g. invite credentials). */
  export let extraFormFields = {};

  let status = 'idle'; // 'idle', 'checking', 'available', 'taken', 'error'
  let message = '';
  let checkTimeout = null;
  let currentCheck = null;
  let previousUsername = '';

  function resetStatus() {
    status = 'idle';
    message = '';
  }

  function debouncedUsernameCheck(username, fields) {
    if (checkTimeout !== null) {
      clearTimeout(checkTimeout);
    }

    if (currentCheck) {
      currentCheck.abort();
    }

    checkTimeout = setTimeout(() => {
      checkUsernameAvailability(username, fields);
    }, 200);
  }

  async function checkUsernameAvailability(username, fields) {
    if (username.length === 0) {
      resetStatus();
      return;
    }

    currentCheck = new AbortController();
    status = 'checking';
    message = 'Checking availability...';

    try {
      const result = await apiService.checkUsernameAvailability(
        username,
        fields,
        currentCheck.signal,
      );

      if (result.available) {
        status = 'available';
        message = 'Username is available';
      } else if (result.taken) {
        status = 'taken';
        message = result.message;
      } else {
        status = 'error';
        message = result.message;
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

  function handleUsernameChange() {
    if (username !== previousUsername) {
      previousUsername = username;

      if (username && username.length > 0) {
        if (status === 'error') {
          resetStatus();
        }
        debouncedUsernameCheck(username, extraFormFields);
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
