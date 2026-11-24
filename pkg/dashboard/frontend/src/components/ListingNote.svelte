<script>
  // A delivery list the caller could only read in part says so: a namespace
  // they may not list is left out, not shown as empty. A list whose last
  // refresh failed says it may be out of date.
  import CappedList from './CappedList.svelte';
  import { clusterListingWords } from '../lib/clusters.ts';

  let { listing = null, error = null, onRetry = () => {} } = $props();
  const unreadable = $derived(listing?.unreadable ?? []);
  // '*': the caller may not list across the cluster, so only the namespaces
  // Pacto could guess were read.
  const guessed = $derived(unreadable.includes('*'));
  const refused = $derived(unreadable.filter((ns) => ns !== '*'));
  // An answer read from several clusters: what each other cluster could not read
  // and how many the reader may not see. The first is the dashboard's own, which
  // the namespace sentences above already speak for.
  const clusters = $derived(listing?.clusters ?? []);
  const hiddenClusters = $derived(listing?.hiddenClusters ?? 0);
  const clusterWords = $derived(clusters.slice(1).map(clusterListingWords).filter(Boolean));
  const shownClusters = $derived(clusters.filter((c) => !c.why).length);
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
      You may not read {refused.length === 1 ? 'the namespace' : 'the namespaces'}{' '}<CappedList items={refused} max={5} inline={true} label="Namespaces you may not read">{#snippet item(ns)}{ns}{/snippet}</CappedList>, so this may be incomplete.
    {/if}
    {#if clusterWords.length || hiddenClusters}
      <span data-testid="listing-clusters">Showing {shownClusters} of {clusters.length + hiddenClusters} clusters:{' '}<CappedList items={clusterWords} max={5} inline={true} label="Clusters read in part">{#snippet item(w)}{w}{/snippet}</CappedList>{#if hiddenClusters}{clusterWords.length ? '; ' : ''}{hiddenClusters} cluster{hiddenClusters === 1 ? '' : 's'} hidden{/if}.</span>
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
