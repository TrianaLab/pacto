<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.ts';
  import { decideViewState, snapshotKnowledge } from '../lib/knowledgeState.ts';
  import { knowledgeLabel, knowledgeTone, sourceHealthLabel } from '../lib/entityLabels.ts';
  import { fleetOverviewUrl, fleetSourcesUrl } from '../lib/router.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import EntityLink from '../components/EntityLink.svelte';
  import ProductEmptyState from '../components/ProductEmptyState.svelte';
  import ActiveFilterChips from '../components/ActiveFilterChips.svelte';

  // The product Sources list (requirement G): source discovery through
  // /api/fleet/entities?kinds=source via the SDK facade, with a search box and a
  // health filter (the backend sourceHealth query param), all kept in the URL. Rows
  // navigate to the rich source page.
  let { text = '', sourceHealth = '', offset = '', refreshTick = 0 } = $props();

  const PAGE_SIZE = 25;
  const HEALTH_OPTIONS = ['available', 'partial', 'stale', 'unavailable'];
  const pageOffset = $derived(Math.max(0, Math.trunc(Number(offset) || 0)));
  const anyFilter = $derived(!!(text || sourceHealth));

  let list = $state(null);
  let loading = $state(true);
  let error = $state(null);
  let lastKey = '';
  let textDraft = $state(text);
  $effect(() => { textDraft = text; });

  async function load() {
    loading = true; error = null;
    try {
      list = await api.fleetEntities({ kinds: ['source'], text: text || undefined, sourceHealth: sourceHealth || undefined, offset: pageOffset || undefined, limit: PAGE_SIZE });
    } catch (e) { error = e; } finally { loading = false; }
  }
  onMount(load);
  $effect(() => {
    const key = `${text}@@${sourceHealth}@@${pageOffset}@@${refreshTick}`;
    if (key !== lastKey) { lastKey = key; load(); }
  });

  const knowledge = $derived(snapshotKnowledge(list?.meta));
  const count = $derived(list?.entities?.length ?? 0);
  const state = $derived(decideViewState({ loading, error, itemCount: count, filtered: anyFilter, knowledge }));
  const total = $derived(list?.total ?? 0);
  const shownFrom = $derived(total === 0 ? 0 : (list?.offset ?? pageOffset) + 1);
  const shownTo = $derived((list?.offset ?? pageOffset) + count);
  const hasPrev = $derived((list?.offset ?? pageOffset) > 0);
  const hasNext = $derived(list?.nextOffset != null);
  const prevOffset = $derived(Math.max(0, (list?.offset ?? pageOffset) - PAGE_SIZE));

  function urlWith(patch, off = 0) {
    return fleetSourcesUrl({ text: patch.text ?? text, sourceHealth: patch.sourceHealth ?? sourceHealth, offset: off });
  }
  function submitSearch(e) { e.preventDefault(); location.hash = urlWith({ text: textDraft }); }
  function clearAll() { location.hash = fleetSourcesUrl(); }
  const chips = $derived([
    text ? { key: 'text', label: 'Search', value: text } : null,
    sourceHealth ? { key: 'sourceHealth', label: 'Health', value: sourceHealthLabel(sourceHealth) } : null,
  ].filter(Boolean));
  function removeChip(key) { location.hash = urlWith({ [key]: '' }); }
</script>

<div class="list-view">
  <Breadcrumbs trail={[{ label: 'Fleet', href: fleetOverviewUrl() }, { label: 'Sources' }]} />
  <div class="lv-head">
    <h1>Sources</h1>
    {#if list}<span class="lv-total">{total} source{total === 1 ? '' : 's'}</span>{/if}
  </div>

  <div class="lv-filters">
    <form class="lv-search" onsubmit={submitSearch} role="search">
      <input type="search" bind:value={textDraft} placeholder="Search sources..." aria-label="Search sources" />
      <button type="submit" class="lv-btn">Search</button>
    </form>
    <label class="lv-field">
      <span>Health</span>
      <select value={sourceHealth} aria-label="Filter by source health" onchange={(e) => (location.hash = urlWith({ sourceHealth: e.currentTarget.value }))}>
        <option value="">Any health</option>
        {#each HEALTH_OPTIONS as h}<option value={h}>{sourceHealthLabel(h)}</option>{/each}
      </select>
    </label>
  </div>
  <ActiveFilterChips {chips} onRemove={removeChip} onClear={clearAll} />

  {#if knowledge.incomplete && state.kind === 'ready'}
    <div class="lv-knowledge tone-{knowledgeTone(knowledge.level)}" role="status">{knowledgeLabel(knowledge.level)} — this list may be incomplete.</div>
  {/if}

  {#if state.kind !== 'ready'}
    <ProductEmptyState {state} noun="sources" onRetry={load} onClearFilters={anyFilter ? clearAll : null} />
  {:else}
    <ul class="lv-list" data-testid="source-list">
      {#each list.entities as ref (ref.key)}
        <li class="lv-item"><EntityLink {ref} showStatus={true} /></li>
      {/each}
    </ul>
    <nav class="lv-pager" aria-label="Source pages">
      <span class="lv-range">Showing {shownFrom}–{shownTo} of {total}</span>
      <div class="lv-pager-btns">
        {#if hasPrev}<a class="lv-page" href={urlWith({}, prevOffset)} data-testid="source-prev" rel="prev">Previous</a>{:else}<span class="lv-page disabled" aria-disabled="true">Previous</span>{/if}
        {#if hasNext}<a class="lv-page" href={urlWith({}, list.nextOffset)} data-testid="source-next" rel="next">Next</a>{:else}<span class="lv-page disabled" aria-disabled="true">Next</span>{/if}
      </div>
    </nav>
  {/if}
</div>

<style>
  .list-view { display: flex; flex-direction: column; gap: var(--sp-4); }
  .lv-head { display: flex; align-items: baseline; gap: var(--sp-3); }
  .lv-head h1 { margin: 0; }
  .lv-total { color: var(--c-text-3); }
  .lv-filters { display: flex; gap: var(--sp-3); flex-wrap: wrap; align-items: flex-end; }
  .lv-search { display: flex; gap: var(--sp-2); flex: 1; min-width: 220px; }
  .lv-search input { flex: 1; padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); color: var(--c-text); font: inherit; font-size: var(--text-sm); min-height: var(--touch-min); }
  .lv-field { display: flex; flex-direction: column; gap: 2px; font-size: var(--text-xs); color: var(--c-text-3); }
  .lv-field select { padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); color: var(--c-text); font: inherit; font-size: var(--text-sm); min-height: var(--touch-min); }
  .lv-btn { padding: var(--sp-2) var(--sp-4); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); color: var(--c-text); font: inherit; font-size: var(--text-sm); cursor: pointer; min-height: var(--touch-min); }
  .lv-btn:hover { border-color: var(--c-accent); }
  .lv-knowledge { padding: var(--sp-2) var(--sp-3); border-radius: var(--radius-sm); font-size: var(--text-sm); background: var(--c-warn-bg); border: 1px solid var(--c-warn-border); }
  .lv-knowledge.tone-err { background: var(--c-err-bg); border-color: color-mix(in srgb, var(--c-err) 30%, transparent); }
  .lv-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .lv-item { padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); }
  .lv-pager { display: flex; align-items: center; justify-content: space-between; gap: var(--sp-3); flex-wrap: wrap; margin-top: var(--sp-2); }
  .lv-range { color: var(--c-text-3); font-size: var(--text-sm); }
  .lv-pager-btns { display: flex; gap: var(--sp-2); }
  .lv-page { padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); font-size: var(--text-sm); color: var(--c-text); text-decoration: none; background: var(--c-surface); min-height: var(--touch-min); display: inline-flex; align-items: center; }
  .lv-page:hover { border-color: var(--c-accent); }
  .lv-page.disabled { color: var(--c-text-3); opacity: 0.5; pointer-events: none; }
</style>
