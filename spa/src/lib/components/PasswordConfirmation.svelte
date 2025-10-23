<script>
  export let password = '';
  export let confirmPassword = '';
  export let disabled = false;

  // Reactive validation
  $: passwordsMatch = password && confirmPassword && password === confirmPassword;
  $: showValidation = password && confirmPassword;

  // Dispatch events for parent components
  import { createEventDispatcher } from 'svelte';
  const dispatch = createEventDispatcher();

  $: {
    if (showValidation) {
      dispatch('validation', {
        isValid: passwordsMatch,
        passwordsMatch,
      });
    }
  }
</script>

<div class="password-confirmation">
  <div class="row">
    <label for="confirmPassword">Confirm Password</label>
    <input
      id="confirmPassword"
      type="password"
      placeholder="Re-enter your password"
      bind:value={confirmPassword}
      {disabled}
      required
    />

      <div class="help-text" class:match={passwordsMatch}>
        {#if showValidation}
          {#if passwordsMatch}
            Passwords match
          {:else}
            Passwords do not match
          {/if}
        {/if}
      </div>
  </div>
</div>

<style>
  .password-confirmation {
    margin-top: 0.5rem;
  }

  .help-text {
    min-height: 1rem;
  }

  .help-text.match {
    color: #059669;
  }
</style>
