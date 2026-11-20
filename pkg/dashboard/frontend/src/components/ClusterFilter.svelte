<script>
  import { clusterWord, clusterCaptionParts } from '../lib/clusters.ts';

  // The Cluster filter of the Services and Targets lists (514-S2): a select over
  // meta.clusters and one caption line naming every cluster the answer cannot show and
  // why. It renders nothing with fewer than two clusters, so a single-cluster install
  // and the demo see no change unless the URL names a cluster. The value is the cluster UID
  // the API filters by.
  // truncated is meta.clustersTruncated: the answer lists only the least healthy clusters.
  let { clusters = [], value = '', truncated = false, onchange } = $props();

  // A cluster set in the URL keeps the filter shown, even when the answer no longer lists it.
  const many = $derived((clusters?.length ?? 0) >= 2 || !!value);
  // A cluster with no id (a source whose cluster could not be told) cannot be filtered by.
  const listed = $derived((clusters ?? []).filter((c) => c.id).map((c) => ({ value: c.id, label: c.access ? `${c.name || c.id} (${clusterWord(c)})` : (c.name || c.id) })));
  // The select shows the filter that is applied: an id the answer does not list gets its own option.
  const options = $derived([
    { value: '', label: 'Any cluster' },
    ...listed,
    ...(value && !listed.some((o) => o.value === value) ? [{ value, label: `${value} (not in this answer)` }] : []),
  ]);
  const caption = $derived(many ? clusterCaptionParts(clusters ?? [], value, truncated) : null);
</script>

{#if many}
  <label class="cf-field">
    <span>Cluster</span>
    <!-- A native select, not Select.svelte: meta.clusters holds at most 50, and this
         filter sits on the Services list, whose first load stays small. -->
    <select {value} aria-label="Filter by cluster" onchange={(e) => onchange(e.currentTarget.value)}>
      {#each options as o (o.value)}<option value={o.value}>{o.label}</option>{/each}
    </select>
  </label>
  <!-- One line under the filter bar: order and a full row put it after every field. -->
  <p class="cf-caption t-meta" data-testid="cluster-caption">{caption.head}{#if caption.hidden.length}{' '}({#each caption.hidden as h, i (i)}{i ? ', ' : ''}<bdi title={h.name}>{h.name}</bdi>: {h.word}{/each}){/if}</p>
{/if}

<style>
  .cf-field { display: flex; flex-direction: column; gap: 2px; flex: 1 1 8rem; min-width: 8rem; max-width: 20rem; font-size: var(--text-xs); color: var(--c-text-3); }
  .cf-field select { width: 100%; min-width: 0; text-overflow: ellipsis; }
  .cf-caption { order: 1; flex: 1 1 100%; min-width: 0; margin: 0; overflow-wrap: anywhere; }
  /* A long cluster name keeps one line and ends in an ellipsis; its title holds it whole. */
  .cf-caption bdi { display: inline-block; max-width: 24ch; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; vertical-align: bottom; }
</style>
