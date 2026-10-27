<script>
  import { onDestroy, untrack } from 'svelte';
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
  import Pager from '../components/Pager.svelte';
  import ListSort from '../components/ListSort.svelte';
  import { urlInt, limitParam, pastEnd } from '../lib/paging.ts';
  import { formatCount } from '../lib/format.ts';
  import DistributionBar from '../components/viz/DistributionBar.svelte';
  import { flip } from 'svelte/animate';
  import { MOTION, dur } from '../lib/motion.ts';
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
  let { kind = 'revision', service = '', text = '', status = '', scope = '', readiness = '', offset = '', limit = '', sort = '', refreshTick = 0 } = $props();

  const PAGE_SIZE = 25;
  const pageOffset = $derived(urlInt(offset));
  const pageLimit = $derived(urlInt(limit) || PAGE_SIZE);
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

  // Seeded once; the effect below keeps it in step with the URL.
  let textDraft = $state(untrack(() => text));
  // The text the last submit wrote to the URL. Its echo reaches `text` a hashchange later,
  // by when the reader may have typed on; adopting it would drop those keystrokes.
  let submittedText = null;
  $effect(() => { if (text !== submittedText) textDraft = text; submittedText = null; });

  const loader = createProductLoader(() => api.fleetEntities({
    kinds: [kind],
    service: service || undefined,
    text: text || undefined,
    status: status || undefined,
    scope: kind === 'target' && scope ? scope : undefined,
    readiness: readinessFilter || undefined,
    offset: pageOffset || undefined,
    limit: pageLimit,
    sort: sort || undefined,
  }));
  // queryIdentity is the QUESTION -- the kind, the service the inventory is scoped to,
  // the filters and the page. refreshTick only re-asks it. Rows are retained across a
  // re-ask and never across a different question, so switching the scoped service can
  // never show one service's revisions under another's heading.
  const queryIdentity = $derived(`${kind}@@${service}@@${text}@@${status}@@${scope}@@${readinessFilter}@@${pageOffset}@@${pageLimit}@@${sort}`);
  // listIdentity is the same question without its page: a page turn keeps the rows.
  const listIdentity = $derived(`${kind}@@${service}@@${text}@@${status}@@${scope}@@${readinessFilter}@@${sort}`);
  $effect(() => { loader.sync(`${queryIdentity}@@${refreshTick}`, queryIdentity, listIdentity); });
  onDestroy(() => loader.destroy());
  function load() { loader.refresh(); }

  const answer = $derived(loader.answer(queryIdentity, listIdentity));
  const list = $derived(answer.data);
  const loading = $derived(loader.loading);
  const error = $derived(loader.error);
  const knowledge = $derived(snapshotKnowledge(list?.meta));
  const count = $derived(list?.entities?.length ?? 0);
  const state = $derived(decideViewState({ loading, error, itemCount: count, filtered: anyFilter, knowledge }));
  // `loader.data`, not `list`: `list` is nulled the instant the query changes, which is
  // exactly when the skeleton renders, and 0 rows is no shape at all.
  const skeletonRows = $derived(loader.data?.entities?.length || pageLimit);
  // A poll that failed over rows we can still show. decideViewState keeps the rows;
  // this is the half that keeps it honest, so a frozen list never reads as a live one.
  const refreshError = $derived(state.kind === 'ready' ? state.refreshError : null);
  // A poll in flight over data already on screen. decideViewState has decided this on
  // every page since stale-while-revalidate landed; the header is the first thing to
  // actually say it, in words rather than by spinning something.
  const revalidating = $derived(state.kind === 'ready' && !!state.revalidating);
  const total = $derived(list?.total ?? 0);

  // The scope is a canonical ServiceKey, so the breadcrumb links straight back to that
  // service's page rather than to an unscoped inventory the user did not come from.
  const trail = $derived(service
    ? [{ label: 'Overview', href: fleetOverviewUrl() }, { label: 'Services', href: fleetServicesUrl() }, { label: service, href: fleetEntityUrl('service', service) }, { label: pluralTitle }]
    : [{ label: 'Overview', href: fleetOverviewUrl() }, { label: pluralTitle }]);

  function urlWith(patch, off = 0, lim = pageLimit) {
    return fleetEntityListUrl(kind, {
      service, text: patch.text ?? text, status: patch.status ?? status, scope: patch.scope ?? scope,
      readiness: patch.readiness ?? readinessFilter, offset: off,
      limit: limitParam(lim, limit, PAGE_SIZE), sort: (patch.sort ?? sort) || undefined,
    });
  }
  function submitSearch(e) { e.preventDefault(); submittedText = textDraft; location.hash = urlWith({ text: textDraft }); }
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
    ? `All ${formatCount(matched)} matching ${matched === 1 ? plural.replace(/s$/, '') : plural}, not just this page.`
    : `All ${formatCount(matched)} ${matched === 1 ? plural.replace(/s$/, '') : plural}${service ? ' for this service' : ' in the snapshot'}.`);
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
    count={list ? `${formatCount(total)} ${total === 1 ? plural.replace(/s$/, '') : plural}` : ''}
    countTestid="entity-list-total"
    subtitle={kind === 'target'
      ? `Every place something has been observed running${service ? ' for this service' : ''}. A target is a runtime observation, not a contract.`
      : `Every known contract revision${service ? ' for this service, newest version first' : ', by service and newest version first'}. A revision is what was declared, whether or not anything runs it.`}
    asOf={list?.meta?.asOf}
    {revalidating} paging={answer.paging}
  />

  <div class="lv-filters">
    <form class="lv-search" onsubmit={submitSearch} role="search">
      <input class="input" type="search" bind:value={textDraft} placeholder={`Search ${plural}...`} aria-label={`Search ${plural}`} />
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
    <ListSort value={sort} href={(v) => urlWith({ sort: v })} testid="entity-list-sort"
      options={[['', isRevisions ? (service ? 'Newest version first' : 'By service, newest version first') : 'Default order'], ['name', 'Name'], ['health', 'Least healthy first']]} />
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

  {#if pastEnd(list, count)}
    <Pager page={list} href={(o, l) => urlWith({}, o, l)} count={0} noun={plural} testid="entity" />
  {:else if state.kind !== 'ready'}
    <ProductEmptyState {state} noun={plural} onRetry={load} onClearFilters={anyFilter ? clearAll : null} {skeletonRows} />
  {:else}
    <ul class="lv-list pl-list pl-list--cols" data-testid="entity-list">
      {#each list.entities as ref (ref.key)}
        <li class="lv-item pl-row" animate:flip={{ duration: dur(MOTION.row) }}><EntityLink {ref} showStatus={true} showKind={false} /></li>
      {/each}
    </ul>
    <Pager page={list} href={(o, l) => urlWith({}, o, l)} label={`${pluralTitle} pages`} noun={plural} testid="entity" />
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
  .lv-search .input { flex: 1; }
  /* The list, the row and the pager are the shared .pl-* rules in styles/components.css. */
</style>
