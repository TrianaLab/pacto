<script>
  import { onDestroy, untrack } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader, productListState } from '../lib/productLoader.svelte.ts';
  import { fleetOverviewUrl, fleetExternalsUrl, fleetServicesUrl } from '../lib/router.ts';
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
  import { formatCount, breakableIdentifierHtml } from '../lib/format.ts';
  import { externalClassLabel, externalJoinWords, EXTERNAL_CLASSES } from '../lib/entityLabels.ts';

  // The external-system list: /api/fleet/entities?kinds=external, with search, the class
  // filter, order and paging in the URL. A row is one canonical identity: the services
  // that declare it and the catalog entry that joined them (or "name only", which joins
  // only consumers that spell it the same). External systems only hidden services
  // declare are counted under the list, never named.
  // outsideContract (1) keeps the external systems with a managed resource no offering
  // produced (517-S7).
  let { text = '', klass = '', outsideContract = '', offset = '', limit = '', sort = '', refreshTick = 0 } = $props();

  const PAGE_SIZE = 25;
  const DEFAULT_SORT = 'consumers';
  const pageOffset = $derived(urlInt(offset));
  const pageLimit = $derived(urlInt(limit) || PAGE_SIZE);
  const order = $derived(sort || DEFAULT_SORT);
  const outside = $derived(!!outsideContract);
  const anyFilter = $derived(!!(text || klass || outside));

  let textDraft = $state(untrack(() => text));
  let submittedText = null;
  $effect(() => { if (text !== submittedText) textDraft = text; submittedText = null; });

  const loader = createProductLoader(() => api.fleetEntities({
    kinds: ['external'], text: text || undefined, class: klass || undefined, outsideContract: outside || undefined,
    offset: pageOffset || undefined, limit: pageLimit, sort: order,
  }));
  const queryIdentity = $derived(`${text}@@${klass}@@${outside}@@${pageOffset}@@${pageLimit}@@${order}`);
  const listIdentity = $derived(`${text}@@${klass}@@${outside}@@${order}`);
  $effect(() => { loader.sync(`${queryIdentity}@@${refreshTick}`, queryIdentity, listIdentity); });
  onDestroy(() => loader.destroy());
  function load() { loader.refresh(); }

  const view = productListState(loader, (d) => d.entities, () => ({ tag: queryIdentity, family: listIdentity, filtered: anyFilter, pageLimit }));
  const list = $derived(view.data);
  const knowledge = $derived(view.knowledge);
  const count = $derived(view.count);
  const state = $derived(view.state);
  const total = $derived(list?.total ?? 0);
  const hidden = $derived(list?.hidden ?? 0);
  // Genuinely empty with complete knowledge: say what puts a system here.
  const noneDeclared = $derived(state.kind === 'empty-fleet');

  const urlWith = (patch, off = 0, lim = pageLimit) => fleetExternalsUrl({
    text: (patch.text ?? text) || undefined, class: (patch.klass ?? klass) || undefined, outsideContract: patch.outside ?? outside,
    offset: off, limit: limitParam(lim, limit, PAGE_SIZE), sort: (patch.sort ?? sort) || undefined,
  });
  const pageHref = (off, lim) => urlWith({}, off, lim);
  const sortHref = (v) => urlWith({ sort: v === DEFAULT_SORT ? '' : v });
  function submitSearch(e) { e.preventDefault(); submittedText = textDraft; location.hash = urlWith({ text: textDraft }); }
  function clearAll() { location.hash = fleetExternalsUrl(); }
  const chips = $derived([text ? { key: 'text', label: 'Search', value: text } : null].filter(Boolean));
  function removeChip() { location.hash = urlWith({ text: '' }); }
  const plural = (n, one, many) => `${formatCount(n)} ${n === 1 ? one : many}`;
</script>

<div class="product-page">
  <Breadcrumbs trail={[{ label: 'Overview', href: fleetOverviewUrl() }, { label: 'Services', href: fleetServicesUrl() }, { label: 'External systems' }]} />
  <PageHeader
    glyph="external"
    title="External systems"
    count={list ? plural(total, 'external system', 'external systems') : ''}
    subtitle="Vendor APIs, managed cloud services and legacy hosts the services declare, one per canonical identity. No contract describes them, so Pacto checks none."
    asOf={list?.meta?.asOf}
    revalidating={view.revalidating} paging={view.paging}
  />

  <div class="xv-filters">
    <form class="lv-search" onsubmit={submitSearch} role="search">
      <input class="input" type="search" bind:value={textDraft} placeholder="Search external systems..." aria-label="Search external systems" />
      <button type="submit" class="btn">Search</button>
    </form>
    <ListSort value={order} options={[['consumers', 'Most consumers first'], ['name', 'Name']]} href={sortHref} testid="external-sort" />
  </div>
  <nav class="xv-classes" aria-label="Filter by class" data-testid="external-class-filter">
    {#each EXTERNAL_CLASSES as c (c)}
      <a class="chip" class:active={klass === c} aria-current={klass === c ? 'true' : undefined} href={urlWith({ klass: c })} data-class={c || 'all'}>{c ? externalClassLabel(c) : 'All'}</a>
    {/each}
    <a class="chip" class:active={outside} aria-current={outside ? 'true' : undefined} href={urlWith({ outside: !outside })} title="External systems with a managed resource no offering produced" data-testid="external-outside-contract">Outside contract</a>
  </nav>
  <ActiveFilterChips {chips} onRemove={removeChip} onClear={clearAll} />

  {#if knowledge.incomplete && (state.kind === 'ready' || state.kind === 'filtered-empty')}
    <KnowledgeBanner {knowledge} noun="list" />
  {/if}

  {#if view.refreshError}
    <StaleRefreshNotice noun="external system list" onRetry={load} />
  {/if}

  {#if pastEnd(list, count)}
    <Pager page={list} href={pageHref} count={0} noun="external systems" testid="external" />
  {:else if noneDeclared}
    <div class="xv-empty" role="status">
      <h2 class="t-section-title">No contract names an external system yet.</h2>
      <p class="t-body-2">A dependency marked external: true with no ref is listed here.</p>
    </div>
  {:else if state.kind !== 'ready'}
    <ProductEmptyState {state} noun="external systems" onRetry={load} onClearFilters={anyFilter ? clearAll : null} skeletonRows={view.skeletonRows} />
  {:else}
    <div class="data-scroll xt-frame" role="region" aria-label="External systems">
      <table class="data-table xt" data-testid="external-list">
        <caption class="visually-hidden">External systems, their class, the services that declare them and how they were joined</caption>
        <colgroup>
          <col />
          <col style:width="10rem" />
          <col style:width="8rem" />
          <col style:width="12rem" />
        </colgroup>
        <thead>
          <tr>
            <th scope="col">Identity</th>
            <th scope="col">Class</th>
            <th scope="col" class="num" aria-sort={order === 'consumers' ? 'descending' : 'none'}>Consumers</th>
            <th scope="col">Joined by</th>
          </tr>
        </thead>
        <tbody>
          {#each list.entities as ref (ref.key)}
            <tr>
              <th scope="row">
                <span class="cell-stack"><EntityLink {ref} showStatus={false} showKind={false} />{#if ref.label !== ref.key}<span class="t-meta xt-key" title={ref.key}><bdi>{@html breakableIdentifierHtml(ref.key)}</bdi></span>{/if}</span>
              </th>
              <td data-label="Class">{externalClassLabel(ref.secondary)}</td>
              <td class="num" data-label="Consumers"><span data-nofilter>{formatCount(ref.consumers ?? 0)}</span></td>
              <td data-label="Joined by">{#if ref.catalogEntry}<span class="cell-clip" title={`Catalog entry ${ref.catalogEntry}`}>catalog entry <bdi>{ref.catalogEntry}</bdi></span>{:else}<span class="t-meta" title={externalJoinWords(ref.joinedBy).sentence}>{externalJoinWords(ref.joinedBy).short}</span>{/if}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    <Pager page={list} href={pageHref} label="External system pages" noun="external systems" testid="external" />
  {/if}

  {#if hidden}
    <p class="xv-hidden t-meta" role="status" data-testid="external-hidden">{plural(hidden, 'more external system is', 'more external systems are')} used only by services you cannot see.</p>
  {/if}
</div>

<style>
  .xv-filters { display: flex; gap: var(--sp-3); flex-wrap: wrap; align-items: flex-end; }
  .lv-search { display: flex; gap: var(--sp-2); max-width: 420px; flex: 1; min-width: 220px; }
  .lv-search .input { flex: 1; min-width: 0; }
  .xv-classes { display: flex; flex-wrap: wrap; gap: var(--sp-2); }
  @media (pointer: coarse) { .xv-classes .chip { min-height: 44px; display: inline-flex; align-items: center; } }
  .xv-empty { padding: var(--sp-6) var(--sp-4); text-align: center; color: var(--c-text-2); }
  .xv-empty h2, .xv-empty p { margin: 0 0 var(--sp-2); }
  .xv-hidden { margin: 0; }
  .xt .num { text-align: right; font-variant-numeric: tabular-nums; }
  .xt-key { overflow-wrap: break-word; }
  /* Four columns, three fixed: cards below their sum plus room for the identity. */
  .xt-frame { container: xtable / inline-size; }
  @container xtable (max-width: 44rem) {
    .xt, .xt tbody, .xt tr, .xt td, .xt tbody th { display: block; width: 100%; }
    .xt colgroup { display: none; }
    .xt thead { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); }
    .xt tr { padding: var(--sp-2) 0; }
    .xt tr + tr { border-top: 1px solid var(--c-border); }
    .xt td, .xt tbody th { border: none; padding: var(--sp-1) var(--sp-3); }
    .xt td[data-label]::before { content: attr(data-label); display: block; color: var(--c-text-3); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); }
    .xt .num { text-align: left; }
  }
</style>
