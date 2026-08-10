<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { decideViewState, snapshotKnowledge } from '../lib/knowledgeState.ts';
  import { kindLabel } from '../lib/entityLabels.ts';
  import { statusLabel } from '../lib/format.ts';
  import { fleetOverviewUrl, fleetServicesUrl, fleetEntityUrl, fleetEntityListUrl } from '../lib/router.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import KnowledgeBanner from '../components/KnowledgeBanner.svelte';
  import EntityLink from '../components/EntityLink.svelte';
  import ProductEmptyState from '../components/ProductEmptyState.svelte';
  import StaleRefreshNotice from '../components/StaleRefreshNotice.svelte';
  import ActiveFilterChips from '../components/ActiveFilterChips.svelte';
  import PageHeader from '../components/PageHeader.svelte';

  // The scoped inventory list for the two kinds that have no list page of their own
  // (requirement 12). A rich entity page shows a BOUNDED preview of its revisions or
  // targets; this is where "and the other 42" actually lives, served by the same
  // bounded /api/fleet/entities endpoint with a canonical `service` scope and real
  // paging -- never by a legacy name-based /api/services/{name}/versions call.
  //
  // Revisions are returned in the backend's canonical order. This view does not
  // re-sort them: the chronology is the engine's answer, and a client re-sort by key
  // is exactly the digest-ordered version list the redesign was fixing.
  let { kind = 'revision', service = '', text = '', status = '', scope = '', offset = '', refreshTick = 0 } = $props();

  const PAGE_SIZE = 25;
  const pageOffset = $derived(Math.max(0, Math.trunc(Number(offset) || 0)));
  const anyFilter = $derived(!!(text || status || scope));
  const plural = $derived(kind === 'target' ? 'operational targets' : 'contract revisions');

  let textDraft = $state(text);
  $effect(() => { textDraft = text; });

  const loader = createProductLoader(() => api.fleetEntities({
    kinds: [kind],
    service: service || undefined,
    text: text || undefined,
    status: status || undefined,
    scope: kind === 'target' && scope ? scope : undefined,
    offset: pageOffset || undefined,
    limit: PAGE_SIZE,
  }));
  // queryIdentity is the QUESTION -- the kind, the service the inventory is scoped to,
  // the filters and the page. refreshTick only re-asks it. Rows are retained across a
  // re-ask and never across a different question, so switching the scoped service can
  // never show one service's revisions under another's heading.
  const queryIdentity = $derived(`${kind}@@${service}@@${text}@@${status}@@${scope}@@${pageOffset}`);
  $effect(() => { loader.sync(`${queryIdentity}@@${refreshTick}`, queryIdentity); });
  onDestroy(() => loader.destroy());
  function load() { loader.refresh(); }

  const list = $derived(loader.dataTag === queryIdentity ? loader.data : null);
  const loading = $derived(loader.loading);
  const error = $derived(loader.error);
  const knowledge = $derived(snapshotKnowledge(list?.meta));
  const count = $derived(list?.entities?.length ?? 0);
  const state = $derived(decideViewState({ loading, error, itemCount: count, filtered: anyFilter, knowledge }));
  // A poll that failed over rows we can still show. decideViewState keeps the rows;
  // this is the half that keeps it honest, so a frozen list never reads as a live one.
  const refreshError = $derived(state.kind === 'ready' ? state.refreshError : null);
  const total = $derived(list?.total ?? 0);
  const shownFrom = $derived(total === 0 ? 0 : (list?.offset ?? pageOffset) + 1);
  const shownTo = $derived((list?.offset ?? pageOffset) + count);
  const hasPrev = $derived((list?.offset ?? pageOffset) > 0);
  const hasNext = $derived(list?.nextOffset != null);
  const prevOffset = $derived(Math.max(0, (list?.offset ?? pageOffset) - PAGE_SIZE));

  // The scope is a canonical ServiceKey, so the breadcrumb links straight back to that
  // service's page rather than to an unscoped inventory the user did not come from.
  const trail = $derived(service
    ? [{ label: 'Overview', href: fleetOverviewUrl() }, { label: 'Services', href: fleetServicesUrl() }, { label: service, href: fleetEntityUrl('service', service) }, { label: kindLabel(kind) }]
    : [{ label: 'Overview', href: fleetOverviewUrl() }, { label: kindLabel(kind) }]);

  function urlWith(patch, off = 0) {
    return fleetEntityListUrl(kind, {
      service, text: patch.text ?? text, status: patch.status ?? status, scope: patch.scope ?? scope, offset: off,
    });
  }
  function submitSearch(e) { e.preventDefault(); location.hash = urlWith({ text: textDraft }); }
  function clearAll() { location.hash = fleetEntityListUrl(kind, { service }); }
  const chips = $derived([
    text ? { key: 'text', label: 'Search', value: text } : null,
    status ? { key: 'status', label: 'Status', value: statusLabel(status) } : null,
    scope ? { key: 'scope', label: 'Scope', value: scope } : null,
  ].filter(Boolean));
  function removeChip(key) { location.hash = urlWith({ [key]: '' }); }
</script>

<div class="list-view">
  <Breadcrumbs {trail} />
  <PageHeader
    title={`${kindLabel(kind)}${service ? ` of ${service}` : ''}`}
    count={list ? `${total} ${total === 1 ? plural.replace(/s$/, '') : plural}` : ''}
    countTestid="entity-list-total"
    subtitle={kind === 'target'
      ? `Every place something has been observed running${service ? ' for this service' : ''}. A target is a runtime observation, not a contract.`
      : `Every known contract revision${service ? ' for this service' : ''}, newest first. A revision is what was declared, whether or not anything runs it.`}
  />

  <div class="lv-filters">
    <form class="lv-search" onsubmit={submitSearch} role="search">
      <input type="search" bind:value={textDraft} placeholder={`Search ${plural}...`} aria-label={`Search ${plural}`} />
      <button type="submit" class="btn">Search</button>
    </form>
  </div>
  <ActiveFilterChips {chips} onRemove={removeChip} onClear={clearAll} />

  {#if knowledge.incomplete && (state.kind === 'ready' || state.kind === 'filtered-empty')}
    <KnowledgeBanner {knowledge} noun="list" />
  {/if}

  {#if refreshError}
    <StaleRefreshNotice noun="list" onRetry={load} />
  {/if}

  {#if state.kind !== 'ready'}
    <ProductEmptyState {state} noun={plural} onRetry={load} onClearFilters={anyFilter ? clearAll : null} />
  {:else}
    <ul class="lv-list" data-testid="entity-list">
      {#each list.entities as ref (ref.key)}
        <li class="lv-item"><EntityLink {ref} showStatus={true} /></li>
      {/each}
    </ul>
    <nav class="lv-pager" aria-label={`${kindLabel(kind)} pages`}>
      <span class="lv-range">Showing {shownFrom}–{shownTo} of {total}</span>
      <div class="lv-pager-btns">
        {#if hasPrev}<a class="lv-page" href={urlWith({}, prevOffset)} data-testid="entity-list-prev" rel="prev">Previous</a>{:else}<span class="lv-page disabled" aria-disabled="true">Previous</span>{/if}
        {#if hasNext}<a class="lv-page" href={urlWith({}, list.nextOffset)} data-testid="entity-list-next" rel="next">Next</a>{:else}<span class="lv-page disabled" aria-disabled="true">Next</span>{/if}
      </div>
    </nav>
  {/if}
</div>

<style>
  .list-view { display: flex; flex-direction: column; gap: var(--sp-4); }
  .lv-filters { display: flex; gap: var(--sp-3); flex-wrap: wrap; align-items: flex-end; }
  .lv-search { display: flex; gap: var(--sp-2); flex: 1; min-width: 220px; }
  .lv-search input { flex: 1; padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); color: var(--c-text); font: inherit; font-size: var(--text-sm); min-height: var(--touch-min); }
  .lv-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .lv-item { padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); }
  .lv-pager { display: flex; align-items: center; justify-content: space-between; gap: var(--sp-3); flex-wrap: wrap; margin-top: var(--sp-2); }
  .lv-range { color: var(--c-text-3); font-size: var(--text-sm); }
  .lv-pager-btns { display: flex; gap: var(--sp-2); }
  .lv-page { padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); font-size: var(--text-sm); color: var(--c-text); text-decoration: none; background: var(--c-surface); min-height: var(--touch-min); display: inline-flex; align-items: center; }
  .lv-page:hover { border-color: var(--c-accent); }
  .lv-page.disabled { color: var(--c-text-3); opacity: 0.5; pointer-events: none; }
</style>
