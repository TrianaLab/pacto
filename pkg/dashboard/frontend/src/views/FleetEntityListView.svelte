<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { decideViewState, snapshotKnowledge } from '../lib/knowledgeState.ts';
  import { statusLabel } from '../lib/format.ts';
  import { fleetOverviewUrl, fleetServicesUrl, fleetEntityUrl, fleetEntityListUrl } from '../lib/router.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import KnowledgeBanner from '../components/KnowledgeBanner.svelte';
  import EntityLink from '../components/EntityLink.svelte';
  import ProductEmptyState from '../components/ProductEmptyState.svelte';
  import StaleRefreshNotice from '../components/StaleRefreshNotice.svelte';
  import ActiveFilterChips from '../components/ActiveFilterChips.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import DistributionBar from '../components/viz/DistributionBar.svelte';
  import { complianceSegments, readinessSegments, statusHrefs, bucketLabel, READINESS_STATES } from '../lib/distributions.ts';

  // The scoped inventory list for the two kinds that have no list page of their
  // own. A rich entity page shows a BOUNDED preview of its revisions or
  // targets; this is where "and the other 42" actually lives, served by the same
  // bounded /api/fleet/entities endpoint with a canonical `service` scope and real
  // paging -- never by a legacy name-based /api/services/{name}/versions call.
  //
  // Revisions are returned in the backend's canonical order. This view does not
  // re-sort them: the chronology is the engine's answer, and a client re-sort by key
  // is exactly the digest-ordered version list the redesign was fixing.
  let { kind = 'revision', service = '', text = '', status = '', scope = '', readiness = '', offset = '', refreshTick = 0 } = $props();

  const PAGE_SIZE = 25;
  const pageOffset = $derived(Math.max(0, Math.trunc(Number(offset) || 0)));
  const isRevisions = $derived(kind === 'revision');
  // Readiness is declared BY a revision, so the Entities API rejects the filter on any
  // other kind. Carrying it here for a target list would build a query that 422s.
  const readinessFilter = $derived(isRevisions ? readiness : '');
  const anyFilter = $derived(!!(text || status || scope || readinessFilter));
  const plural = $derived(kind === 'target' ? 'operational targets' : 'contract revisions');
  // The page names the POPULATION, like every sibling list ("Services", "Owners",
  // "Data sources"). It used to take the singular entity kind from kindLabel, so a
  // page of twenty-six was headed "Revision" -- and headed it with the bare kind
  // rather than the product noun the count underneath was already using.
  const pluralTitle = $derived(plural[0].toUpperCase() + plural.slice(1));

  let textDraft = $state(text);
  $effect(() => { textDraft = text; });

  const loader = createProductLoader(() => api.fleetEntities({
    kinds: [kind],
    service: service || undefined,
    text: text || undefined,
    status: status || undefined,
    scope: kind === 'target' && scope ? scope : undefined,
    readiness: readinessFilter || undefined,
    offset: pageOffset || undefined,
    limit: PAGE_SIZE,
  }));
  // queryIdentity is the QUESTION -- the kind, the service the inventory is scoped to,
  // the filters and the page. refreshTick only re-asks it. Rows are retained across a
  // re-ask and never across a different question, so switching the scoped service can
  // never show one service's revisions under another's heading.
  const queryIdentity = $derived(`${kind}@@${service}@@${text}@@${status}@@${scope}@@${readinessFilter}@@${pageOffset}`);
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
    ? [{ label: 'Overview', href: fleetOverviewUrl() }, { label: 'Services', href: fleetServicesUrl() }, { label: service, href: fleetEntityUrl('service', service) }, { label: pluralTitle }]
    : [{ label: 'Overview', href: fleetOverviewUrl() }, { label: pluralTitle }]);

  function urlWith(patch, off = 0) {
    return fleetEntityListUrl(kind, {
      service, text: patch.text ?? text, status: patch.status ?? status, scope: patch.scope ?? scope,
      readiness: patch.readiness ?? readinessFilter, offset: off,
    });
  }
  function submitSearch(e) { e.preventDefault(); location.hash = urlWith({ text: textDraft }); }
  function clearAll() { location.hash = fleetEntityListUrl(kind, { service }); }
  const chips = $derived([
    text ? { key: 'text', label: 'Search', value: text } : null,
    status ? { key: 'status', label: 'Status', value: statusLabel(status) } : null,
    scope ? { key: 'scope', label: 'Scope', value: scope } : null,
    readinessFilter ? { key: 'readiness', label: 'Readiness', value: bucketLabel(READINESS_STATES, readinessFilter) } : null,
  ].filter(Boolean));
  function removeChip(key) { location.hash = urlWith({ [key]: '' }); }

  // The inventory over the COMPLETE filtered population, computed by the backend with
  // paging excluded -- so it describes the query, not the 25 rows underneath it.
  //
  // A revision list gets its declared readiness; a target list does not, because
  // readiness is a property of the revision that declares it. A revision can be passing
  // its own readiness threshold and still be running on a target observed to violate
  // its contract: those are two different questions about two different units, and this
  // page keeps them apart by never drawing readiness where the unit is not a revision.
  const agg = $derived(list?.aggregate ?? null);
  const matched = $derived(agg?.matched ?? 0);
  const scopeNote = $derived(anyFilter
    ? `All ${matched} matching ${matched === 1 ? plural.replace(/s$/, '') : plural}, not just this page.`
    : `All ${matched} ${matched === 1 ? plural.replace(/s$/, '') : plural}${service ? ' for this service' : ' in the snapshot'}.`);
  const readinessOfMatch = $derived(readinessSegments(agg?.readiness, Object.fromEntries(
    READINESS_STATES.map((s) => [s.field, urlWith({ readiness: s.value })]),
  )));
  const complianceOfMatch = $derived(complianceSegments(agg?.targetCompliance,
    statusHrefs((s) => urlWith({ status: s }))));
</script>

<div class="product-page">
  <Breadcrumbs {trail} />
  <PageHeader
    title={`${pluralTitle}${service ? ` of ${service}` : ''}`}
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
    {#if isRevisions}
      <label class="lv-field">
        <span>Readiness</span>
        <select value={readinessFilter} aria-label="Filter by declared readiness" onchange={(e) => { location.hash = urlWith({ readiness: e.currentTarget.value }); }}>
          <option value="">Any assessment</option>
          {#each READINESS_STATES as r}<option value={r.value}>{r.label}</option>{/each}
        </select>
      </label>
    {/if}
  </div>
  <ActiveFilterChips {chips} onRemove={removeChip} onClear={clearAll} />

  {#if knowledge.incomplete && (state.kind === 'ready' || state.kind === 'filtered-empty')}
    <KnowledgeBanner {knowledge} noun="list" />
  {/if}

  {#if refreshError}
    <StaleRefreshNotice noun="list" onRetry={load} />
  {/if}

  {#if state.kind === 'ready' && matched > 0}
    <section class="lv-inventory" aria-labelledby="lv-inv-h">
      <h2 id="lv-inv-h" class="t-section-title">{anyFilter ? 'What these filters select' : `This ${isRevisions ? 'revision' : 'target'} inventory`}</h2>
      <div class="lv-inv-grid">
        {#if isRevisions}
          <DistributionBar
            title="Contract revision readiness"
            description="Declared preparedness of each revision, judged against the threshold that revision set for itself. This is not compliance: a passing revision can still be running on a target observed to violate its contract."
            {scopeNote}
            segments={readinessOfMatch}
            total={matched}
            emptyLabel="None of these revisions declares a readiness assessment."
          />
        {:else}
          <DistributionBar
            title="Compliance"
            description="Whether each target is observed to obey the contract of the revision it runs."
            {scopeNote}
            segments={complianceOfMatch}
            total={matched}
          />
        {/if}
      </div>
    </section>
  {/if}

  {#if state.kind !== 'ready'}
    <ProductEmptyState {state} noun={plural} onRetry={load} onClearFilters={anyFilter ? clearAll : null} />
  {:else}
    <ul class="lv-list" data-testid="entity-list">
      {#each list.entities as ref (ref.key)}
        <li class="lv-item"><EntityLink {ref} showStatus={true} /></li>
      {/each}
    </ul>
    <nav class="lv-pager" aria-label={`${pluralTitle} pages`}>
      <span class="lv-range">Showing {shownFrom}–{shownTo} of {total}</span>
      <div class="lv-pager-btns">
        {#if hasPrev}<a class="lv-page" href={urlWith({}, prevOffset)} data-testid="entity-list-prev" rel="prev">Previous</a>{:else}<span class="lv-page disabled" aria-disabled="true">Previous</span>{/if}
        {#if hasNext}<a class="lv-page" href={urlWith({}, list.nextOffset)} data-testid="entity-list-next" rel="next">Next</a>{:else}<span class="lv-page disabled" aria-disabled="true">Next</span>{/if}
      </div>
    </nav>
  {/if}
</div>

<style>
  .lv-filters { display: flex; gap: var(--sp-3); flex-wrap: wrap; align-items: flex-end; }
  .lv-search { display: flex; gap: var(--sp-2); flex: 1; min-width: 220px; }
  .lv-field { display: flex; flex-direction: column; gap: 2px; min-width: 180px; font-size: var(--text-xs); color: var(--c-text-3); }
  .lv-inventory { display: flex; flex-direction: column; gap: var(--sp-3); padding-top: var(--sp-3); border-top: 1px solid var(--c-border); }
  .lv-inventory h2 { margin: 0; }
  /* Same track rule as every other chart block in the product. */
  .lv-inv-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 320px), 1fr)); gap: var(--sp-4); }
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
