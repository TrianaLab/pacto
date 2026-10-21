<script>
  // A delivery list the caller could only read in part says so: a namespace
  // they may not list is left out, not shown as empty.
  let { listing = null } = $props();
  const unreadable = $derived(listing?.unreadable ?? []);
</script>

{#if listing?.completeness === 'partial'}
  <p class="ln-partial t-body-2" role="status" data-testid="listing-partial">
    {#if unreadable.includes('*')}
      You may not list delivery objects across the cluster and the namespaces could not be known, so this may be incomplete.
    {:else}
      You may not read {unreadable.length === 1 ? 'the namespace' : 'the namespaces'} {unreadable.join(', ')}, so this may be incomplete.
    {/if}
  </p>
{/if}

<style>
  .ln-partial {
    margin: 0; padding: var(--sp-2) var(--sp-3); border-radius: var(--radius-sm);
    background: var(--c-warn-bg); color: var(--c-warn); border: 1px solid var(--c-warn-border);
  }
</style>
