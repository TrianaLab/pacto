<script>
  // A delivery list the caller could only read in part says so: a namespace
  // they may not list is left out, not shown as empty. A list whose last
  // refresh failed says it may be out of date.
  let { listing = null, error = null, onRetry = () => {} } = $props();
  const unreadable = $derived(listing?.unreadable ?? []);
  // '*': the caller may not list across the cluster, so only the namespaces
  // Pacto could guess were read.
  const guessed = $derived(unreadable.includes('*'));
  const refused = $derived(unreadable.filter((ns) => ns !== '*'));
</script>

{#if listing && error}
  <p class="ln-stale t-body-2" role="alert" data-testid="listing-stale">The last refresh failed, so this may be out of date. <button type="button" class="btn btn-sm" onclick={onRetry}>Retry</button></p>
{/if}
{#if listing?.completeness === 'partial'}
  <p class="ln-partial t-body-2" role="status" data-testid="listing-partial">
    {#if guessed}
      You may not list delivery objects across the cluster, so this shows only the namespaces Pacto knows of and may be incomplete.
    {/if}
    {#if refused.length}
      You may not read {refused.length === 1 ? 'the namespace' : 'the namespaces'} {refused.join(', ')}, so this may be incomplete.
    {/if}
  </p>
{/if}

<style>
  .ln-stale { color: var(--c-err); margin: 0; }
  .ln-partial {
    margin: 0; padding: var(--sp-2) var(--sp-3); border-radius: var(--radius-sm);
    background: var(--c-warn-bg); color: var(--c-warn); border: 1px solid var(--c-warn-border);
  }
</style>
