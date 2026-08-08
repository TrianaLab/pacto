<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.ts';
  import { decideViewState, snapshotKnowledge } from '../lib/knowledgeState.ts';
  import { knowledgeLabel, knowledgeTone } from '../lib/entityLabels.ts';
  import { fleetOverviewUrl, fleetAttentionUrl } from '../lib/router.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import EntityLink from '../components/EntityLink.svelte';
  import StatusBadge from '../components/StatusBadge.svelte';
  import ProductEmptyState from '../components/ProductEmptyState.svelte';
  import ActiveFilterChips from '../components/ActiveFilterChips.svelte';

  // The dedicated attention list (requirement G/K/I). It consumes /api/fleet/attention,
  // optionally filtered by category (the overview tiles link here), and renders every
  // item as a navigable row. Category AND the page offset are kept in the URL so the
  // filtered, paged view is deep-linkable and back/forward-restorable (A2). Real
  // backend pagination (limit/offset/total/nextOffset) is used -- never client-side
  // slicing of a preloaded dataset.
  let { category = '', offset = '', refreshTick = 0 } = $props();

  const PAGE_SIZE = 25;
  const pageOffset = $derived(Math.max(0, Math.trunc(Number(offset) || 0)));

  let list = $state(null);
  let loading = $state(true);
  let error = $state(null);
  let lastKey = '';

  async function load() {
    loading = true;
    error = null;
    try {
      list = await api.fleetAttention({
        category: category || undefined,
        offset: pageOffset || undefined,
        limit: PAGE_SIZE,
      });
    } catch (e) {
      error = e;
    } finally {
      loading = false;
    }
  }

  onMount(load);
  // Reload when the category, the page offset (both from the URL) or the refresh tick
  // changes, so back/forward and deep links restore the exact page.
  $effect(() => {
    const key = `${category}@@${pageOffset}@@${refreshTick}`;
    if (key !== lastKey) {
      lastKey = key;
      load();
    }
  });

  const knowledge = $derived(snapshotKnowledge(list?.meta));
  const count = $derived(list?.items?.length ?? 0);
  const state = $derived(decideViewState({ loading, error, itemCount: count, filtered: !!category, knowledge }));
  const chips = $derived(category ? [{ key: 'category', label: 'Category', value: category }] : []);

  // Paging facts come from the backend page metadata, not from the item array.
  const total = $derived(list?.total ?? 0);
  const shownFrom = $derived(total === 0 ? 0 : (list?.offset ?? pageOffset) + 1);
  const shownTo = $derived((list?.offset ?? pageOffset) + count);
  const hasPrev = $derived((list?.offset ?? pageOffset) > 0);
  const hasNext = $derived(list?.nextOffset != null);
  const prevOffset = $derived(Math.max(0, (list?.offset ?? pageOffset) - PAGE_SIZE));
  const prevHref = $derived(fleetAttentionUrl({ category: category || undefined, offset: prevOffset }));
  const nextHref = $derived(fleetAttentionUrl({ category: category || undefined, offset: list?.nextOffset ?? undefined }));

  function clearCategory() {
    // Clearing the category also drops the offset -- a changed filter resets to page 1.
    location.hash = fleetAttentionUrl();
  }
</script>

<div class="attn-view">
  <Breadcrumbs trail={[{ label: 'Fleet', href: fleetOverviewUrl() }, { label: 'Attention' }]} />
  <div class="av-head">
    <h1>Needs attention</h1>
    {#if list}<span class="av-total">{list.total} item{list.total === 1 ? '' : 's'}</span>{/if}
  </div>

  <ActiveFilterChips {chips} onRemove={clearCategory} onClear={clearCategory} />

  {#if knowledge.incomplete && state.kind === 'ready'}
    <div class="av-knowledge tone-{knowledgeTone(knowledge.level)}" role="status">
      {knowledgeLabel(knowledge.level)} — this attention list may be incomplete.
    </div>
  {/if}

  {#if state.kind !== 'ready'}
    <ProductEmptyState {state} noun="attention items" onRetry={load} onClearFilters={category ? clearCategory : null} />
  {:else}
    <ul class="attn-list">
      {#each list.items as it}
        <li class="attn-item">
          <StatusBadge status={it.severity} />
          <span class="attn-cat">{it.category}</span>
          <EntityLink ref={it.entity} showStatus={false} />
          <span class="attn-summary">{it.summary || it.reason || it.label}</span>
          {#if it.nextStep}<span class="attn-next">{it.nextStep}</span>{/if}
        </li>
      {/each}
    </ul>

    <nav class="av-pager" aria-label="Attention pages">
      <span class="av-range">Showing {shownFrom}–{shownTo} of {total}</span>
      <div class="av-pager-btns">
        {#if hasPrev}
          <a class="av-page" href={prevHref} data-testid="attn-prev" rel="prev">Previous</a>
        {:else}
          <span class="av-page disabled" aria-disabled="true">Previous</span>
        {/if}
        {#if hasNext}
          <a class="av-page" href={nextHref} data-testid="attn-next" rel="next">Next</a>
        {:else}
          <span class="av-page disabled" aria-disabled="true">Next</span>
        {/if}
      </div>
    </nav>
  {/if}
</div>

<style>
  .attn-view { display: flex; flex-direction: column; gap: var(--sp-4); }
  .av-head { display: flex; align-items: baseline; gap: var(--sp-3); }
  .av-head h1 { margin: 0; }
  .av-total { color: var(--c-text-3); }
  .av-knowledge {
    padding: var(--sp-2) var(--sp-3); border-radius: var(--radius-sm); font-size: var(--text-sm);
    background: var(--c-warn-bg); border: 1px solid var(--c-warn-border);
  }
  .av-knowledge.tone-err { background: var(--c-err-bg); border-color: color-mix(in srgb, var(--c-err) 30%, transparent); }
  .attn-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .attn-item {
    display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap;
    padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    background: var(--c-surface);
  }
  .attn-cat {
    font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.03em;
    color: var(--c-text-3); background: var(--c-surface-inset); padding: 1px 6px; border-radius: var(--radius-xs);
  }
  .attn-summary { color: var(--c-text-2); font-size: var(--text-sm); }
  .attn-next { color: var(--c-text-3); font-size: var(--text-xs); margin-left: auto; }
  .av-pager {
    display: flex; align-items: center; justify-content: space-between; gap: var(--sp-3);
    flex-wrap: wrap; margin-top: var(--sp-2);
  }
  .av-range { color: var(--c-text-3); font-size: var(--text-sm); }
  .av-pager-btns { display: flex; gap: var(--sp-2); }
  .av-page {
    padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    font-size: var(--text-sm); color: var(--c-text); text-decoration: none; background: var(--c-surface);
    min-height: var(--touch-min); display: inline-flex; align-items: center;
  }
  .av-page:hover { border-color: var(--c-accent); text-decoration: none; }
  .av-page.disabled { color: var(--c-text-3); opacity: 0.5; pointer-events: none; }
</style>
