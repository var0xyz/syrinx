<script>
  export let currentStep;
  export let totalSteps;

  $: progress = (currentStep / totalSteps) * 100;
</script>

<div class="progress-container">
  <div class="progress-bar">
    <div class="progress-fill" style="width: {progress}%"></div>
  </div>
</div>

<style>
  .progress-container {
    margin: 1rem 0;
    padding: 1rem;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
  }

  .progress-bar {
    width: 100%;
    height: 8px;
    background: var(--input-bg);
    border-radius: 4px;
    overflow: hidden;
    margin-bottom: 0.5rem;
  }

  .progress-fill {
    height: 100%;
    background: linear-gradient(90deg, var(--primary), #4fc3f7);
    border-radius: 4px;
    transition: width 0.3s ease;
    position: relative;
  }

  .progress-fill::after {
    content: '';
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    background: linear-gradient(90deg, transparent, rgba(255, 255, 255, 0.2), transparent);
    animation: shimmer 2s infinite;
  }
  @keyframes shimmer {
    0% {
      transform: translateX(-100%);
    }
    100% {
      transform: translateX(100%);
    }
  }
</style>
