<script>
  // A refresh that failed OVER content we can still show.
  //
  // decideViewState deliberately ranks data on hand above a request that failed, so the
  // rows survive a poll that could not reach the backend. That is only honest if the
  // failure is stated: without this line a frozen page reads exactly like a current one,
  // and the user makes decisions on numbers that stopped moving minutes ago.
  //
  // It is one component rather than a copy per view because "you are reading the last
  // answer we received" must say the same thing everywhere it is true.
  let { noun = 'page', onRetry = null } = $props();
</script>

<p class="stale-notice" role="status" data-testid="stale-refresh">
  This {noun} could not be refreshed, so you are reading the last answer we received.
  {#if onRetry}<button type="button" class="stale-retry" onclick={onRetry}>Try again</button>{/if}
</p>

<style>
  .stale-notice {
    margin: 0; display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap;
    padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-left: 3px solid var(--c-warn);
    border-radius: var(--radius-sm); background: var(--c-surface);
    font-size: var(--text-sm); color: var(--c-text-2);
  }
  .stale-retry {
    font: inherit; color: var(--c-accent); background: none; border: none; padding: 0;
    text-decoration: underline; cursor: pointer;
  }
</style>
