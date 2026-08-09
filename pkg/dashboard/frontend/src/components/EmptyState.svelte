<script>
  // `error` (string or true) switches to an error variant with an optional Retry
  // button — so a failed fetch never masquerades as a benign empty state.
  let { title = undefined, message = undefined, loading = false, error = null, onRetry = null } = $props();

  const errorText = $derived(message || (typeof error === 'string' ? error : ''));
</script>

<div class="state-box" class:is-error={error && !loading}>
  {#if loading}
    <div class="skeleton-table fade-in">
      {#each Array(4) as _}
        <div class="skeleton-row">
          <div class="skeleton skeleton-line" style="width:25%"></div>
          <div class="skeleton skeleton-line" style="width:10%"></div>
          <div class="skeleton skeleton-line" style="width:15%"></div>
        </div>
      {/each}
    </div>
    {#if message}
      <p style="margin-top:var(--sp-3); color:var(--c-text-3)">{message}</p>
    {/if}
  {:else if error}
    <h3>{title || 'Couldn’t load'}</h3>
    {#if errorText}<p>{errorText}</p>{/if}
    {#if onRetry}
      <button type="button" class="retry-btn" onclick={onRetry}>Retry</button>
    {/if}
  {:else if title || message}
    {#if title}<h3>{title}</h3>{/if}
    {#if message}<p>{message}</p>{/if}
  {/if}
</div>

<style>
  .state-box {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    padding: var(--sp-8) var(--sp-4);
    text-align: center;
    color: var(--c-text-3);
    gap: var(--sp-3);
    /* No opacity fade-in: an in-flight opacity animation transiently dips the muted
       title/message text below the AA contrast ratio (axe samples it mid-fade), and the
       fade added no real value on a resting empty/error state. */
  }
  .state-box h3 {
    color: var(--c-text-2);
  }
  .state-box.is-error h3 {
    color: var(--c-err);
  }
  .retry-btn {
    margin-top: var(--sp-2);
    background: none;
    border: 1px solid var(--c-border);
    border-radius: var(--radius-xs);
    color: var(--c-accent);
    font: inherit;
    padding: 6px 14px;
    min-height: var(--touch-min);
    cursor: pointer;
  }
  .retry-btn:hover { background: var(--c-surface-inset); }
  .state-box p {
    max-width: 400px;
    line-height: var(--line-height);
  }
  .skeleton-table {
    width: 100%;
    max-width: 600px;
  }
  .skeleton-row {
    display: flex;
    gap: var(--sp-3);
    margin-bottom: var(--sp-3);
  }
  .skeleton-row .skeleton-line {
    height: 18px;
    border-radius: var(--radius-xs);
  }
</style>
