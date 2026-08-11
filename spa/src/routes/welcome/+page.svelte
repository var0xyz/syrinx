<div class="container">
  <div class="card">
    <div class="welcome-header">
      <h1>🎉 Welcome to Syrinx!</h1>
      <p class="subtitle">Your secure messaging account has been successfully created</p>
    </div>


    <div class="success-steps">
      <h2>What just happened:</h2>

      <div class="step">
        <div class="step-icon">👤</div>
        <div class="step-content">
          <h3>User Account Created</h3>
          <p>Your account has been successfully created and is ready to use.</p>
        </div>
      </div>

      <div class="step">
        <div class="step-icon">🔐</div>
        <div class="step-content">
          <h3>Encryption Keys Generated</h3>
          <p>A unique pair of encryption keys was generated specifically for your account. These keys ensure that only you can read your messages.</p>
        </div>
      </div>

      <div class="step">
        <div class="step-icon">☁️</div>
        <div class="step-content">
          <h3>Public Key Uploaded</h3>
          <p>Your public key has been securely uploaded to our servers. This allows other users to send you encrypted messages.</p>
        </div>
      </div>

      <div class="step success-step">
        <div class="step-icon">✅</div>
        <div class="step-content">
          <h3>App Ready to Use!</h3>
          <p>Your Syrinx account has been fully configured and is ready for secure messaging. You can now start posting and connecting with others!</p>
        </div>
      </div>
    </div>

  </div>
    <div class="action-buttons">
      <a href="/reeds" class="btn btn-primary">
        Start Posting!
      </a>
      <a href="/account" class="btn btn-secondary">
        View Profile
      </a>
    </div>
</div>

<style>
  .welcome-header {
    text-align: center;
    margin-bottom: 2rem;
  }

  .subtitle {
    color: var(--muted);
    font-size: 1.1rem;
    margin: 0;
  }

  .success-steps {
    margin: 2rem 0;
  }

  .success-steps h2 {
    color: var(--primary);
    margin-bottom: 1.5rem;
  }

  .step {
    display: flex;
    align-items: flex-start;
    margin-bottom: 1.5rem;
    padding: 1rem;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
  }

  .step-icon {
    font-size: 2rem;
    margin-right: 1rem;
    flex-shrink: 0;
  }

  .step-content h3 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
  }

  .step-content p {
    margin: 0;
    color: var(--muted);
    line-height: 1.5;
  }

  .success-step {
    background: rgba(34, 197, 94, 0.1);
    border: 1px solid rgba(34, 197, 94, 0.3);
  }

  .success-step .step-content h3 {
    color: #22c55e;
  }

  .action-buttons {
    display: flex;
    gap: 1rem;
    margin: 2rem 0;
    flex-wrap: wrap;
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
  }

  .btn-primary {
    background: var(--primary);
    color: var(--button-text);
  }

  .btn-primary:hover {
    opacity: 0.9;
    transform: translateY(-1px);
  }

  .btn-secondary {
    background: var(--surface);
    color: var(--fg);
    border: 1px solid var(--border);
  }

  .btn-secondary:hover {
    background: var(--border);
  }

  .btn:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }



  @keyframes gradient-shift {
    0% {
      background-position: 0% 50%;
    }
    50% {
      background-position: 100% 50%;
    }
    100% {
      background-position: 0% 50%;
    }
  }

  @media (max-width: 640px) {
    .action-buttons {
      flex-direction: column;
    }

    .btn {
      text-align: center;
      justify-content: center;
    }
  }
</style>
