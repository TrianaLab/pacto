<script>
  import { onDestroy, untrack } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader, productListState } from '../lib/productLoader.svelte.ts';
  import { fleetOverviewUrl, fleetOfferingsUrl, fleetServicesUrl } from '../lib/router.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import KnowledgeBanner from '../components/KnowledgeBanner.svelte';
  import EntityLink from '../components/EntityLink.svelte';
  import ProductEmptyState from '../components/ProductEmptyState.svelte';
  import StaleRefreshNotice from '../components/StaleRefreshNotice.svelte';
  import ActiveFilterChips from '../components/ActiveFilterChips.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import Pager from '../components/Pager.svelte';
  import ListSort from '../components/ListSort.svelte';
  import Select from '../components/Select.svelte';
  import LazyView from '../components/LazyView.svelte';
  import { loadOfferingsFigure } from '../lib/lazyFigures.ts';
  import { urlInt, limitParam, pastEnd } from '../lib/paging.ts';
  import { formatCount } from '../lib/format.ts';
  import { flip } from 'svelte/animate';
  import { MOTION, dur } from '../lib/motion.ts';

  // The platform-offering list: /api/fleet/entities?kinds=offering, with search, the
  // lifecycle and failed-request filters, order and paging in the URL. The figure reads
  // the fleet phase tally over every matched offering (B3); each row says who maintains
  // the offering, its worst lifecycle, whether it is available and how Pacto knows, how
  // many consume it, how many of those have a failed request and its observed targets.
  let { text = '', lifecycle = '', failed = '', offset = '', limit = '', sort = '', refreshTick = 0 } = $props();

  const PAGE_SIZE = 25;
  // The figure's ranking (T3): the offerings with most failed requests first.
  const DEFAULT_SORT = 'failed';
  const pageOffset = $derived(urlInt(offset));
  const pageLimit = $derived(urlInt(limit) || PAGE_SIZE);
  const order = $derived(sort || DEFAULT_SORT);
  const onlyFailed = $derived(!!failed);
  const anyFilter = $derived(!!(text || lifecycle || onlyFailed));

  let textDraft = $state(untrack(() => text));
  let submittedText = null;
  $effect(() => { if (text !== submittedText) textDraft = text; submittedText = null; });

  const loader = createProductLoader(() => api.fleetEntities({
    kinds: ['offering'], text: text || undefined, lifecycle: lifecycle || undefined, failed: onlyFailed || undefined,
    offset: pageOffset || undefined, limit: pageLimit, sort: order,
  }));
  const queryIdentity = $derived(`${text}@@${lifecycle}@@${onlyFailed}@@${pageOffset}@@${pageLimit}@@${order}`);
  const listIdentity = $derived(`${text}@@${lifecycle}@@${onlyFailed}@@${order}`);
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
  const agg = $derived(list?.aggregate ?? null);
  // Genuinely empty with complete knowledge (empty-fleet): say how to declare an
  // offering. With incomplete knowledge "none declared" would be a guess.
  const noneDeclared = $derived(state.kind === 'empty-fleet');

  const urlWith = (patch, off = 0, lim = pageLimit) => fleetOfferingsUrl({
    text: (patch.text ?? text) || undefined, lifecycle: (patch.lifecycle ?? lifecycle) || undefined, failed: patch.failed ?? onlyFailed,
    offset: off, limit: limitParam(lim, limit, PAGE_SIZE), sort: (patch.sort ?? sort) || undefined,
  });
  const pageHref = (off, lim) => urlWith({}, off, lim);
  const sortHref = (v) => urlWith({ sort: v === DEFAULT_SORT ? '' : v });
  // The figure's Failed station and "N more" list the same matched offerings that have a failed request.
  const failedHref = $derived(urlWith({ failed: true, sort: '' }));
  function submitSearch(e) { e.preventDefault(); submittedText = textDraft; location.hash = urlWith({ text: textDraft }); }
  function clearAll() { location.hash = fleetOfferingsUrl(); }
  const LIFECYCLES = [{ value: '', label: 'All' }, { value: 'deprecated', label: 'Deprecated' }, { value: 'sunset', label: 'Past sunset' }];
  const chips = $derived([
    text ? { key: 'text', label: 'Search', value: text } : null,
    lifecycle ? { key: 'lifecycle', label: 'Lifecycle', value: LIFECYCLES.find((l) => l.value === lifecycle)?.label ?? lifecycle } : null,
    onlyFailed ? { key: 'failed', label: 'Has', value: 'a failed request' } : null,
  ].filter(Boolean));
  function removeChip(key) { location.hash = urlWith({ [key]: key === 'failed' ? false : '' }); }
  const plural = (n, one, many) => `${formatCount(n)} ${n === 1 ? one : many}`;

  const LIFECYCLE_BADGE = { supported: ['supported', 'ok'], deprecated: ['deprecated', 'warn'], sunset: ['past sunset', 'err'] };
  const lifecycleBadge = (v) => LIFECYCLE_BADGE[v] ?? ['unknown', 'neutral'];
  const AVAILABILITY_TONE = { available: 'ok', degraded: 'warn', unavailable: 'err' };
  // The state with how Pacto knows it as visible text: an inferred unavailable never
  // reads like an observed one.
  const availabilityLabel = (r) => {
    const st = r.availability || 'unknown';
    return st !== 'unknown' && r.availabilityProvenance ? `${st} · ${r.availabilityProvenance}` : st;
  };
  const hiddenNote = (r) => `${plural(r.hiddenConsumers, 'consumer', 'consumers')} you may not see ${r.hiddenConsumers === 1 ? 'is' : 'are'} not counted`;
  // Count headers are links that ask the server for that order; the active one carries aria-sort.
  const SORTS = { consumers: 'Consumers', failed: 'Failed' };
  // The Owners column is sized to the longest owner on the page, capped (T1).
  const ownerCh = $derived(Math.min(40, Math.max(12, ...(list?.entities ?? []).map((r) => [...(r.owner ?? '')].length + 2))));
</script>

<div class="product-page">
  <Breadcrumbs trail={[{ label: 'Overview', href: fleetOverviewUrl() }, { label: 'Offerings' }]} />
  <PageHeader
    glyph="offering"
    title="Platform offerings"
    count={list ? plural(total, 'offering', 'offerings') : ''}
    subtitle="Services that platform teams publish for other teams to use, grouped by the service.offering their contracts declare."
    asOf={list?.meta?.asOf}
    {revalidating} paging={view.paging}
  />

  {#if agg?.offeringCount && state.kind === 'ready'}
    <LazyView load={loadOfferingsFigure} aggregate={agg} {failedHref} meta={list?.meta} />
  {/if}

  <div class="ov-filters">
    <form class="lv-search" onsubmit={submitSearch} role="search">
      <input class="input" type="search" bind:value={textDraft} placeholder="Search offerings..." aria-label="Search offerings" />
      <button type="submit" class="btn">Search</button>
    </form>
    <Select label="Lifecycle" value={lifecycle} options={LIFECYCLES} onchange={(e) => { location.hash = urlWith({ lifecycle: e.currentTarget.value }); }} />
    <label class="ov-failed">
      <input type="checkbox" checked={onlyFailed} onchange={(e) => { location.hash = urlWith({ failed: e.currentTarget.checked }); }} data-testid="offering-failed-filter" />
      Only offerings with a failed request
    </label>
    <ListSort value={order} options={[['failed', 'Most failed requests first'], ['consumers', 'Most consumers first'], ['name', 'Name'], ['services', 'Most providers first']]} href={sortHref} testid="offering-sort" />
  </div>
  <ActiveFilterChips {chips} onRemove={removeChip} onClear={clearAll} />

  {#if knowledge.incomplete && (state.kind === 'ready' || state.kind === 'filtered-empty')}
    <KnowledgeBanner {knowledge} noun="list" />
  {/if}

  {#if refreshError}
    <StaleRefreshNotice noun="offering list" onRetry={load} />
  {/if}

  {#if pastEnd(list, count)}
    <Pager page={list} href={pageHref} count={0} noun="offerings" testid="offering" />
  {:else if noneDeclared}
    <div class="ov-empty" role="status">
      <h2 class="t-section-title">No contract declares a platform offering yet.</h2>
      <p class="t-body-2">Set service.offering in a contract (pactoVersion 2.4) to list it here.</p>
    </div>
  {:else if state.kind !== 'ready'}
    <ProductEmptyState {state} noun="offerings" onRetry={load} onClearFilters={anyFilter ? clearAll : null} {skeletonRows} />
  {:else}
    <div class="data-scroll ot-frame" role="region" aria-label="Platform offerings">
      <table class="data-table ot" data-testid="offering-list">
        <caption class="visually-hidden">Platform offerings, who maintains them, their lifecycle, availability, consumers, failed requests and observed targets</caption>
        <colgroup>
          <col />
          <col style:width="min({ownerCh}ch, 24%)" />
          <col style:width="8rem" />
          <col style:width="11rem" />
          <col style:width="7.5rem" />
          <col style:width="7rem" />
          <col style:width="7.5rem" />
        </colgroup>
        <thead>
          <tr>
            <th scope="col">Offering</th>
            <th scope="col">Owners</th>
            <th scope="col">Lifecycle</th>
            <th scope="col">Availability</th>
            {#each Object.entries(SORTS) as [k, label] (k)}
              <th scope="col" class="num" aria-sort={order === k ? 'descending' : 'none'}><a class="ot-sort" href={sortHref(k)}>{label}{#if order === k}<span aria-hidden="true">{' ▼'}</span>{/if}</a></th>
            {/each}
            <th scope="col" class="num">Observed targets</th>
          </tr>
        </thead>
        <tbody>
          {#each list.entities as ref (ref.key)}
            {@const lc = lifecycleBadge(ref.lifecycle)}
            {@const av = ref.availability || 'unknown'}
            {@const consumers = ref.consumers ?? 0}
            {@const failedN = ref.failedRequests ?? 0}
            {@const hidden = ref.hiddenConsumers ?? 0}
            <tr animate:flip={{ duration: dur(MOTION.row) }}>
              <th scope="row">
                <span class="cell-stack"><EntityLink {ref} showStatus={false} showKind={false} /><span class="t-meta">{plural(ref.services ?? 0, 'provider', 'providers')}</span></span>
              </th>
              <td data-label="Owners">{#if ref.owner}<span class="cell-clip" title={ref.owner}><bdi>{ref.owner}</bdi></span>{:else}<span class="t-meta">none declared</span>{/if}</td>
              <td data-label="Lifecycle"><span class="badge badge-{lc[1]}">{lc[0]}</span></td>
              <td data-label="Availability"><span class="badge badge-{AVAILABILITY_TONE[av] ?? 'neutral'}" class:hatch={av === 'unknown'} title={ref.availabilityProvenance ? `${av}, ${ref.availabilityProvenance}` : av}>{availabilityLabel(ref)}</span></td>
              <td class="num" data-label="Consumers">
                {#if consumers}<a href={fleetServicesUrl({ consumes: ref.key })}>{formatCount(consumers)}</a>{:else}<span data-nofilter>0</span>{/if}{#if hidden}<span class="ot-hidden" title={hiddenNote(ref)}>{` +${formatCount(hidden)}`}</span><span class="visually-hidden">, {hiddenNote(ref)}</span>{/if}
              </td>
              <td class="num" data-label="Failed">
                {#if failedN}<a href={fleetServicesUrl({ consumes: ref.key, phase: 'failed' })}>{formatCount(failedN)}</a>{:else}<span data-nofilter>{hidden ? '0 seen' : '0'}</span>{/if}{#if hidden}<span class="visually-hidden">, {hiddenNote(ref)}</span>{/if}
              </td>
              <td class="num" data-label="Observed targets">
                {#if ref.targetsUnknown}<span class="t-meta" title="You may not see every target of these providers">unknown</span><span class="visually-hidden">: you may not see every target of these providers</span>{:else}<span data-nofilter>{formatCount(ref.targets ?? 0)}</span>{/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    <Pager page={list} href={pageHref} label="Offering pages" noun="offerings" testid="offering" />
  {/if}
</div>

<style>
  .ov-filters { display: flex; gap: var(--sp-3); flex-wrap: wrap; align-items: flex-end; }
  .lv-search { display: flex; gap: var(--sp-2); max-width: 420px; flex: 1; min-width: 220px; }
  .lv-search .input { flex: 1; }
  .ov-failed { display: inline-flex; align-items: center; gap: var(--sp-2); font-size: var(--text-sm); color: var(--c-text-2); }
  @media (pointer: coarse) { .ov-failed { min-height: 44px; } }
  .ov-empty { padding: var(--sp-6) var(--sp-4); text-align: center; color: var(--c-text-2); }
  .ov-empty h2, .ov-empty p { margin: 0 0 var(--sp-2); }
  .ot .num { text-align: right; font-variant-numeric: tabular-nums; }
  .ot-sort { color: inherit; text-decoration: none; }
  .ot-sort:hover, .ot-sort:focus-visible { text-decoration: underline; }
  .ot-hidden { color: var(--c-text-3); }
  /* A count link sits beside the hidden-consumers note, so it is underlined, never told by color alone. */
  .ot td a { text-decoration: underline; }
  /* Seven columns, five of them fixed: cards below their sum plus room for the two
     identities (T2), in rem sized for Verdana. */
  .ot-frame { container: otable / inline-size; }
  @container otable (max-width: 66rem) {
    .ot, .ot tbody, .ot tr, .ot td, .ot tbody th { display: block; width: 100%; }
    .ot colgroup { display: none; }
    .ot thead { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); }
    .ot tr { padding: var(--sp-2) 0; }
    .ot tr + tr { border-top: 1px solid var(--c-border); }
    .ot td, .ot tbody th { border: none; padding: var(--sp-1) var(--sp-3); }
    .ot td[data-label]::before { content: attr(data-label); display: block; color: var(--c-text-3); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); }
    .ot .num { text-align: left; }
  }
</style>
