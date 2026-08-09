<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { decideViewState, snapshotKnowledge } from '../lib/knowledgeState.ts';
  import { knowledgeLabel, knowledgeTone } from '../lib/entityLabels.ts';
  import { fleetOverviewUrl, fleetServicesUrl } from '../lib/router.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import EntityLink from '../components/EntityLink.svelte';
  import ProductEmptyState from '../components/ProductEmptyState.svelte';
  import ActiveFilterChips from '../components/ActiveFilterChips.svelte';

  // The product Services list (requirement C / A3). It is the canonical destination of
  // the backend EntryPointServices href (/fleet/services) and the primary Navbar
  // Services link on fleet-capable hosts. It consumes /api/fleet/entities?kinds=service
  // through the generated SDK facade -- NEVER the legacy preloaded /api/services list
  // and never a FleetSnapshot reconstruction. Filters and the page offset live in the
  // URL, so the filtered/paged list is deep-linkable and back/forward-restorable.
  let { text = '', owner = '', status = '', domain = '', offset = '', refreshTick = 0 } = $props();

  const PAGE_SIZE = 25;
  const STATUS_OPTIONS = ['Compliant', 'NonCompliant', 'Unknown', 'Invalid'];
  const pageOffset = $derived(Math.max(0, Math.trunc(Number(offset) || 0)));
  const anyFilter = $derived(!!(text || owner || status || domain));

  // The search box is a local draft synced from the URL; it commits on submit so
  // typing does not spam browser history. It re-syncs when the URL text changes
  // (external navigation / back-forward), which never clobbers mid-typing because the
  // prop only changes on a committed navigation.
  let textDraft = $state(text);
  $effect(() => { textDraft = text; });

  // One reusable, race-safe loader (requirement E): the fetcher reads the current
  // params at request time; sync(key) dedupes the initial load and the generation
  // guard prevents an older response overwriting a newer route/filter/refresh.
  const loader = createProductLoader(() => api.fleetEntities({
    kinds: ['service'],
    text: text || undefined,
    owner: owner || undefined,
    status: status || undefined,
    domain: domain || undefined,
    offset: pageOffset || undefined,
    limit: PAGE_SIZE,
  }));
  $effect(() => {
    loader.sync(`${text}@@${owner}@@${status}@@${domain}@@${pageOffset}@@${refreshTick}`);
  });
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

  // Every filter change resets the offset to page 1; a patch value of '' clears that
  // filter (`?? current` only falls back on null/undefined, never on the empty string).
  function urlWith(patch, off = 0) {
    return fleetServicesUrl({
      text: patch.text ?? text,
      owner: patch.owner ?? owner,
      status: patch.status ?? status,
      domain: patch.domain ?? domain,
      offset: off,
    });
  }
  function apply(patch) { location.hash = urlWith(patch); }
  function submitSearch(e) { e.preventDefault(); apply({ text: textDraft }); }
  function clearAll() { location.hash = fleetServicesUrl(); }

  const chips = $derived([
    text ? { key: 'text', label: 'Search', value: text } : null,
    owner ? { key: 'owner', label: 'Owner', value: owner } : null,
    status ? { key: 'status', label: 'Status', value: status } : null,
    domain ? { key: 'domain', label: 'Domain', value: domain } : null,
  ].filter(Boolean));
  function removeChip(key) { apply({ [key]: '' }); }
</script>

<div class="svc-view">
  <Breadcrumbs trail={[{ label: 'Overview', href: fleetOverviewUrl() }, { label: 'Services' }]} />
  <div class="sv-head">
    <h1>Services</h1>
    {#if list}<span class="sv-total">{total} service{total === 1 ? '' : 's'}</span>{/if}
  </div>

  <div class="sv-filters">
    <form class="sv-search" onsubmit={submitSearch} role="search">
      <input
        type="search"
        bind:value={textDraft}
        placeholder="Search services..."
        aria-label="Search services by name, key or domain"
      />
      <button type="submit" class="sv-btn">Search</button>
    </form>
    <label class="sv-field">
      <span>Owner</span>
      <input type="text" value={owner} placeholder="team or DRI" aria-label="Filter by owner"
        onchange={(e) => apply({ owner: e.currentTarget.value.trim() })} />
    </label>
    <label class="sv-field">
      <span>Status</span>
      <select value={status} aria-label="Filter by compliance status" onchange={(e) => apply({ status: e.currentTarget.value })}>
        <option value="">Any status</option>
        {#each STATUS_OPTIONS as s}<option value={s}>{s}</option>{/each}
      </select>
    </label>
    <label class="sv-field">
      <span>Domain</span>
      <input type="text" value={domain} placeholder="exact domain" aria-label="Filter by domain"
        onchange={(e) => apply({ domain: e.currentTarget.value.trim() })} />
    </label>
  </div>

  <ActiveFilterChips {chips} onRemove={removeChip} onClear={clearAll} />

  {#if knowledge.incomplete && (state.kind === 'ready' || state.kind === 'filtered-empty')}
    <div class="sv-knowledge tone-{knowledgeTone(knowledge.level)}" role="status">
      {knowledgeLabel(knowledge.level)} — this list may be incomplete.
    </div>
  {/if}

  {#if state.kind !== 'ready'}
    <ProductEmptyState {state} noun="services" onRetry={load} onClearFilters={anyFilter ? clearAll : null} />
  {:else}
    <ul class="sv-list" data-testid="service-list">
      {#each list.entities as ref (ref.kind + '::' + ref.key)}
        <li class="sv-item">
          <EntityLink {ref} showStatus={true} />
        </li>
      {/each}
    </ul>

    <nav class="sv-pager" aria-label="Service pages">
      <span class="sv-range">Showing {shownFrom}–{shownTo} of {total}</span>
      <div class="sv-pager-btns">
        {#if hasPrev}
          <a class="sv-page" href={urlWith({}, prevOffset)} data-testid="svc-prev" rel="prev">Previous</a>
        {:else}
          <span class="sv-page disabled" aria-disabled="true">Previous</span>
        {/if}
        {#if hasNext}
          <a class="sv-page" href={urlWith({}, list.nextOffset)} data-testid="svc-next" rel="next">Next</a>
        {:else}
          <span class="sv-page disabled" aria-disabled="true">Next</span>
        {/if}
      </div>
    </nav>
  {/if}
</div>

<style>
  .svc-view { display: flex; flex-direction: column; gap: var(--sp-4); }
  .sv-head { display: flex; align-items: baseline; gap: var(--sp-3); }
  .sv-head h1 { margin: 0; }
  .sv-total { color: var(--c-text-3); }
  .sv-filters { display: flex; gap: var(--sp-3); flex-wrap: wrap; align-items: flex-end; }
  .sv-search { display: flex; gap: var(--sp-2); flex: 1; min-width: 220px; }
  .sv-search input { flex: 1; }
  .sv-field { display: flex; flex-direction: column; gap: 2px; font-size: var(--text-xs); color: var(--c-text-3); }
  .sv-filters input, .sv-filters select {
    padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    background: var(--c-surface); color: var(--c-text); font: inherit; font-size: var(--text-sm); min-height: var(--touch-min);
  }
  .sv-btn {
    padding: var(--sp-2) var(--sp-4); border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    background: var(--c-surface); color: var(--c-text); font: inherit; font-size: var(--text-sm);
    cursor: pointer; min-height: var(--touch-min);
  }
  .sv-btn:hover { border-color: var(--c-accent); }
  .sv-knowledge {
    padding: var(--sp-2) var(--sp-3); border-radius: var(--radius-sm); font-size: var(--text-sm);
    background: var(--c-warn-bg); border: 1px solid var(--c-warn-border);
  }
  .sv-knowledge.tone-err { background: var(--c-err-bg); border-color: color-mix(in srgb, var(--c-err) 30%, transparent); }
  .sv-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .sv-item {
    display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap;
    padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    background: var(--c-surface);
  }
  .sv-pager {
    display: flex; align-items: center; justify-content: space-between; gap: var(--sp-3);
    flex-wrap: wrap; margin-top: var(--sp-2);
  }
  .sv-range { color: var(--c-text-3); font-size: var(--text-sm); }
  .sv-pager-btns { display: flex; gap: var(--sp-2); }
  .sv-page {
    padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    font-size: var(--text-sm); color: var(--c-text); text-decoration: none; background: var(--c-surface);
    min-height: var(--touch-min); display: inline-flex; align-items: center;
  }
  .sv-page:hover { border-color: var(--c-accent); text-decoration: none; }
  .sv-page.disabled { color: var(--c-text-3); opacity: 0.5; pointer-events: none; }
</style>
