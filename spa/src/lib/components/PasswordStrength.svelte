<script>
  export let password = '';

  // Password strength variables
  let strength = 'weak';
  let strengthPercentage = 0;
  let strengthText = 'Password strength';

  // Reactive password strength calculation
  $: {
    if (password) {
      const result = checkPasswordStrength(password);
      strength = result.strength;
      strengthPercentage = (result.score / 5) * 100;

      const strengthMessages = {
        weak: 'Weak password',
        medium: 'Medium strength',
        strong: 'Strong password',
        excellent: 'Excellent password!'
      };
      strengthText = strengthMessages[strength];
    } else {
      strength = 'weak';
      strengthPercentage = 0;
      strengthText = 'Password strength';
    }
  }

  function checkPasswordStrength(password) {
    let score = 0;
    const checks = {
      length: password.length >= 16,
      lowercase: /[a-z]/.test(password),
      uppercase: /[A-Z]/.test(password),
      number: /\d/.test(password),
      special: /[!@#$%^&*(),.?":{}|<>]/.test(password)
    };

    score = Object.values(checks).filter(Boolean).length;

    return {
      score,
      checks,
      strength: score < 2 ? 'weak' : score < 4 ? 'medium' : score < 5 ? 'strong' : 'excellent'
    };
  }
</script>

<div class="password-strength">
  <div class="strength-meter">
    <div
      class="strength-bar"
      class:strength-weak={strength === 'weak'}
      class:strength-medium={strength === 'medium'}
      class:strength-strong={strength === 'strong'}
      class:strength-excellent={strength === 'excellent'}
      style="width: {strengthPercentage}%"
    ></div>
  </div>
  <div class="strength-text">{strengthText}</div>
</div>

<style>
  .password-strength {
    margin-top: 0.5rem;
  }

  .strength-meter {
    width: 100%;
    height: 4px;
    background: var(--border);
    border-radius: 2px;
    overflow: hidden;
    margin-bottom: 0.25rem;
  }

  .strength-bar {
    height: 100%;
    transition: width 0.3s ease, background 0.3s ease;
    border-radius: 2px;
  }

  .strength-weak {
    background: #ff6b6b;
  }

  .strength-medium {
    background: #ffa726;
  }

  .strength-strong {
    background: #66bb6a;
  }

  .strength-excellent {
    background: linear-gradient(90deg, #ff6b6b, #ffa726, #66bb6a, #42a5f5, #ab47bc, #ff6b6b);
    background-size: 200% 100%;
    animation: rainbow-slide 2s linear infinite;
  }

  @keyframes rainbow-slide {
    0% {
      background-position: 0% 50%;
    }
    100% {
      background-position: 200% 50%;
    }
  }

  .strength-text {
    font-size: 0.75rem;
    color: var(--muted);
  }
</style>
