<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { decideViewState, snapshotKnowledge } from '../lib/knowledgeState.ts';
  import { knowledgeLabel, knowledgeTone } from '../lib/entityLabels.ts';
  import { fleetOverviewUrl, fleetOwnersUrl } from '../lib/router.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import EntityLink from '../components/EntityLink.svelte';
  import ProductEmptyState from '../components/ProductEmptyState.svelte';
  import ActiveFilterChips from '../components/ActiveFilterChips.svelte';

  // The product Owners list (requirement G): owner discovery through
  // /api/fleet/entities?kinds=owner via the SDK facade, with a search box and stable
  // backend pagination kept in the URL. Rows navigate to the rich owner page.
  let { text = '', offset = '', refreshTick = 0 } = $props();

  const PAGE_SIZE = 25;
  const pageOffset = $derived(Math.max(0, Math.trunc(Number(offset) || 0)));
  const anyFilter = $derived(!!text);

  let textDraft = $state(text);
  $effect(() => { textDraft = text; });

  // One reusable, race-safe loader (requirement E): dedupes the initial load and
  // guards against a stale response overwriting a newer route/filter/refresh.
  const loader = createProductLoader(() => api.fleetEntities({ kinds: ['owner'], text: text || undefined, offset: pageOffset || undefined, limit: PAGE_SIZE }));
  $effect(() => { loader.sync(`${text}@@${pageOffset}@@${refreshTick}`); });
  onDestroy(() => loader.destroy());
  function load() { loader.refresh(); }

  const list = $derived(loader.data);
  const loading = $derived(loader.loading);
  const error = $derived(loader.error);
  const knowledge = $derived(snapshotKnowledge(list?.meta));
  const count = $derived(list?.entities?.length ?? 0);
  const state = $derived(decideViewState({ loading, error, itemCount: count, filtered: anyFilter, knowledge }));
  const total = $derived(list?.total ?? 0);
  const shownFrom = $derived(total === 0 ? 0 : (list?.offset ?? pageOffset) + 1);
  const shownTo = $derived((list?.offset ?? pageOffset) + count);
  const hasPrev = $derived((list?.offset ?? pageOffset) > 0);
  const hasNext = $derived(list?.nextOffset != null);
  const prevOffset = $derived(Math.max(0, (list?.offset ?? pageOffset) - PAGE_SIZE));

  function submitSearch(e) { e.preventDefault(); location.hash = fleetOwnersUrl({ text: textDraft }); }
  function clearAll() { location.hash = fleetOwnersUrl(); }
  const chips = $derived(text ? [{ key: 'text', label: 'Search', value: text }] : []);
</script>

<div class="list-view">
  <Breadcrumbs trail={[{ label: 'Overview', href: fleetOverviewUrl() }, { label: 'Owners' }]} />
  <div class="lv-head">
    <h1>Owners</h1>
    {#if list}<span class="lv-total">{total} owner{total === 1 ? '' : 's'}</span>{/if}
  </div>

  <form class="lv-search" onsubmit={submitSearch} role="search">
    <input type="search" bind:value={textDraft} placeholder="Search owners..." aria-label="Search owners" />
    <button type="submit" class="lv-btn">Search</button>
  </form>
  <ActiveFilterChips {chips} onRemove={clearAll} onClear={clearAll} />

  {#if knowledge.incomplete && (state.kind === 'ready' || state.kind === 'filtered-empty')}
    <div class="lv-knowledge tone-{knowledgeTone(knowledge.level)}" role="status">{knowledgeLabel(knowledge.level)} — this list may be incomplete.</div>
  {/if}

  {#if state.kind !== 'ready'}
    <ProductEmptyState {state} noun="owners" onRetry={load} onClearFilters={anyFilter ? clearAll : null} />
  {:else}
    <ul class="lv-list" data-testid="owner-list">
      {#each list.entities as ref (ref.key)}
        <li class="lv-item"><EntityLink {ref} showStatus={false} /></li>
      {/each}
    </ul>
    <nav class="lv-pager" aria-label="Owner pages">
      <span class="lv-range">Showing {shownFrom}–{shownTo} of {total}</span>
      <div class="lv-pager-btns">
        {#if hasPrev}<a class="lv-page" href={fleetOwnersUrl({ text: text || undefined, offset: prevOffset })} data-testid="owner-prev" rel="prev">Previous</a>{:else}<span class="lv-page disabled" aria-disabled="true">Previous</span>{/if}
        {#if hasNext}<a class="lv-page" href={fleetOwnersUrl({ text: text || undefined, offset: list.nextOffset })} data-testid="owner-next" rel="next">Next</a>{:else}<span class="lv-page disabled" aria-disabled="true">Next</span>{/if}
      </div>
    </nav>
  {/if}
</div>

<style>
  .list-view { display: flex; flex-direction: column; gap: var(--sp-4); }
  .lv-head { display: flex; align-items: baseline; gap: var(--sp-3); }
  .lv-head h1 { margin: 0; }
  .lv-total { color: var(--c-text-3); }
  .lv-search { display: flex; gap: var(--sp-2); max-width: 420px; }
  .lv-search input { flex: 1; padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); color: var(--c-text); font: inherit; font-size: var(--text-sm); min-height: var(--touch-min); }
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
