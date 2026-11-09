<script>
  import { onDestroy, untrack } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader, productListState } from '../lib/productLoader.svelte.ts';
  import { fleetOverviewUrl, fleetDomainsUrl, fleetServicesUrl } from '../lib/router.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import KnowledgeBanner from '../components/KnowledgeBanner.svelte';
  import ProductEmptyState from '../components/ProductEmptyState.svelte';
  import StaleRefreshNotice from '../components/StaleRefreshNotice.svelte';
  import ActiveFilterChips from '../components/ActiveFilterChips.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import Pager from '../components/Pager.svelte';
  import GroupTable from '../components/GroupTable.svelte';
  import { urlInt, limitParam, pastEnd } from '../lib/paging.ts';
  import { formatCount } from '../lib/format.ts';
  import { businessDomainLabel, NO_DOMAIN, UNKNOWN_DOMAIN } from '../lib/entityLabels.ts';
  import LazyView from '../components/LazyView.svelte';
  import { loadRankFigure } from '../lib/lazyFigures.ts';

  // The business-domain list: /api/fleet/entities?kinds=domain, with search, order and
  // paging in the URL. The services whose domain is unknown (businessDomain=(unknown))
  // and those that declare none (businessDomain=(none)) are separate questions, each
  // counted with a one-row request and shown under the list in a neutral tone.
  // The default order is the rank figure's (most non-compliant services first, T3);
  // the list narrows to the domains with a non-compliant service or to the domains
  // where one owner has a service (the owner page's "N more").
  let { text = '', hasNonCompliant = '', ownerKey = '', offset = '', limit = '', sort = '', refreshTick = 0 } = $props();
  const DEFAULT_SORT = 'nonCompliant';
  const nc = $derived(!!hasNonCompliant);

  const PAGE_SIZE = 25;
  const pageOffset = $derived(urlInt(offset));
  const pageLimit = $derived(urlInt(limit) || PAGE_SIZE);
  const order = $derived(sort || DEFAULT_SORT);
  const anyFilter = $derived(!!(text || nc || ownerKey));

  let textDraft = $state(untrack(() => text));
  let submittedText = null;
  $effect(() => { if (text !== submittedText) textDraft = text; submittedText = null; });

  const loader = createProductLoader(() => api.fleetEntities({ kinds: ['domain'], text: text || undefined, hasNonCompliant: nc || undefined, ownerKey: ownerKey || undefined, offset: pageOffset || undefined, limit: pageLimit, sort: order }));
  const queryIdentity = $derived(`${text}@@${nc}@@${ownerKey}@@${pageOffset}@@${pageLimit}@@${order}`);
  const listIdentity = $derived(`${text}@@${nc}@@${ownerKey}@@${order}`);
  $effect(() => { loader.sync(`${queryIdentity}@@${refreshTick}`, queryIdentity, listIdentity); });
  onDestroy(() => loader.destroy());
  function load() { loader.refresh(); }

  const view = productListState(loader, (d) => d.entities, () => ({ tag: queryIdentity, family: listIdentity, filtered: anyFilter, pageLimit }));
  const list = $derived(view.data);
  const knowledge = $derived(view.knowledge);
  const count = $derived(view.count);
  const state = $derived(view.state);
  const skeletonRows = $derived(view.skeletonRows);
  const refreshError = $derived(view.refreshError);
  const revalidating = $derived(view.revalidating);
  const total = $derived(list?.total ?? 0);
  // The two counts under the list. A failure costs only its row, which then says it
  // could not be counted: a missing row would read as zero.
  function countLoader(businessDomain) {
    const l = createProductLoader(() => api.fleetEntities({ kinds: ['service'], businessDomain, template: 'exclude', limit: 1 }));
    $effect(() => { l.sync(`${businessDomain}@@${refreshTick}`, businessDomain); });
    onDestroy(() => l.destroy());
    return l;
  }
  const counts = [
    { key: UNKNOWN_DOMAIN, testid: 'domain-unknown', loader: countLoader(UNKNOWN_DOMAIN), note: 'Services whose domain Pacto cannot tell: an older operator, an unread contract or sources that disagree.' },
    { key: NO_DOMAIN, testid: 'domain-none', loader: countLoader(NO_DOMAIN), note: 'Services with no declared domain.' },
  ];
  const unknownLoader = counts[0].loader;
  // Genuinely empty with complete knowledge and no service of unknown domain: say how
  // to declare a domain. Otherwise "none declared" would be a guess, and the shared
  // "none known" state stays.
  const noneDeclared = $derived(state.kind.startsWith('empty') && !knowledge.incomplete
    && unknownLoader.data != null && (unknownLoader.data.total ?? 0) === 0);

  // Which domains hold the non-compliant services (B8): a fleet-wide question, so its
  // own request; a failure costs only the figure.
  const aggLoader = createProductLoader(() => api.fleetEntities({ kinds: ['service'], limit: 1 }));
  $effect(() => { aggLoader.sync(`agg@@${refreshTick}`, 'agg'); });
  onDestroy(() => aggLoader.destroy());
  const agg = $derived(aggLoader.data?.aggregate ?? null);
  const rankHrefs = {
    services: (d) => fleetServicesUrl({ businessDomain: d, status: 'NonCompliant' }),
    groups: fleetDomainsUrl({ hasNonCompliant: true }),
  };

  const urlWith = (patch, off = 0, lim = pageLimit) => fleetDomainsUrl({
    text: (patch.text ?? text) || undefined, hasNonCompliant: patch.nc ?? nc, ownerKey: (patch.ownerKey ?? ownerKey) || undefined,
    offset: off, limit: limitParam(lim, limit, PAGE_SIZE), sort: (patch.sort ?? sort) || undefined,
  });
  const pageHref = (off, lim) => urlWith({}, off, lim);
  const sortHref = (v) => urlWith({ sort: v === DEFAULT_SORT ? '' : v });
  function submitSearch(e) { e.preventDefault(); submittedText = textDraft; location.hash = urlWith({ text: textDraft }); }
  function clearAll() { location.hash = fleetDomainsUrl(); }
  const chips = $derived([
    text ? { key: 'text', label: 'Search', value: text } : null,
    nc ? { key: 'nc', label: 'Has', value: 'a non-compliant service' } : null,
    ownerKey ? { key: 'ownerKey', label: 'Owner', value: ownerKey } : null,
  ].filter(Boolean));
  function removeChip(key) { location.hash = urlWith({ [key]: key === 'nc' ? false : '' }); }
  const ncHref = (ref) => fleetServicesUrl({ businessDomain: ref.key, status: 'NonCompliant' });
  const plural = (n, one, many) => `${formatCount(n)} ${n === 1 ? one : many}`;
</script>

<div class="product-page">
  <Breadcrumbs trail={[{ label: 'Overview', href: fleetOverviewUrl() }, { label: 'Domains' }]} />
  <PageHeader
    glyph="domain"
    title="Domains"
    count={list ? plural(total, 'domain', 'domains') : ''}
    subtitle="The business domain each service declares in its contract, as service.domain."
    asOf={list?.meta?.asOf}
    {revalidating} paging={view.paging}
  />

  {#if (agg?.serviceCompliance?.nonCompliant ?? 0) > 0}
    <LazyView load={loadRankFigure} kind="domain" aggregate={agg} hrefs={rankHrefs} meta={aggLoader.data?.meta} />
  {/if}

  <div class="dv-filters">
    <form class="lv-search" onsubmit={submitSearch} role="search">
      <input class="input" type="search" bind:value={textDraft} placeholder="Search domains..." aria-label="Search domains" />
      <button type="submit" class="btn">Search</button>
    </form>
    <label class="dv-nc">
      <input type="checkbox" checked={nc} onchange={(e) => { location.hash = urlWith({ nc: e.currentTarget.checked }); }} data-testid="domain-nc-filter" />
      Only domains with a non-compliant service
    </label>
  </div>
  <ActiveFilterChips {chips} onRemove={removeChip} onClear={clearAll} />

  {#if knowledge.incomplete && (state.kind === 'ready' || state.kind === 'filtered-empty')}
    <KnowledgeBanner {knowledge} noun="list" />
  {/if}

  {#if refreshError}
    <StaleRefreshNotice noun="domain list" onRetry={load} />
  {/if}

  {#if pastEnd(list, count)}
    <Pager page={list} href={pageHref} count={0} noun="domains" testid="domain" />
  {:else if noneDeclared}
    <div class="dv-empty" role="status">
      <h2 class="t-section-title">No service declares a domain yet.</h2>
      <p class="t-body-2">Set service.domain in a contract (pactoVersion 2.4) to group services by domain.</p>
    </div>
  {:else if state.kind !== 'ready'}
    <ProductEmptyState {state} noun="domains" onRetry={load} onClearFilters={anyFilter ? clearAll : null} {skeletonRows} />
  {:else}
    <GroupTable kind="domain" entities={list.entities} {order} {sortHref} {ncHref} testid="domain-list" />
    <Pager page={list} href={pageHref} label="Domain pages" noun="domains" testid="domain" />
  {/if}

  {#each counts as c (c.key)}
    {#if c.loader.error}
      <p class="dv-none" role="status" data-testid="domain-count-error">
        <span>{businessDomainLabel(c.key)}: these services could not be counted.</span>
        <button type="button" class="btn btn-sm" onclick={() => c.loader.refresh()}>Retry</button>
      </p>
    {:else if (c.loader.data?.total ?? 0) > 0}
      <p class="dv-none go" data-testid={c.testid}>
        <a href={fleetServicesUrl({ businessDomain: c.key })}>{businessDomainLabel(c.key)}: {plural(c.loader.data.total, 'service', 'services')}</a>
        <span class="dv-none-note">{c.note}</span>
      </p>
    {/if}
  {/each}
</div>

<style>
  .dv-filters { display: flex; gap: var(--sp-3); flex-wrap: wrap; align-items: center; }
  .dv-nc { display: inline-flex; align-items: center; gap: var(--sp-2); font-size: var(--text-sm); color: var(--c-text-2); }
  .lv-search { display: flex; gap: var(--sp-2); max-width: 420px; flex: 1; min-width: 220px; }
  .lv-search .input { flex: 1; }
  .dv-empty { padding: var(--sp-6) var(--sp-4); text-align: center; color: var(--c-text-2); }
  .dv-empty h2, .dv-empty p { margin: 0 0 var(--sp-2); }
  /* No domain is a neutral fact, never a healthy one. */
  .dv-none {
    display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--sp-1) var(--sp-3); margin: 0;
    padding: var(--sp-2) var(--sp-3); border: 1px dashed var(--c-border); border-radius: var(--radius-sm);
    background: var(--c-surface-inset); color: var(--c-text-2);
  }
  .dv-none a { color: var(--c-text); font-weight: 600; text-decoration: underline; }
  .dv-none-note { font-size: var(--text-sm); color: var(--c-text-3); }
</style>
