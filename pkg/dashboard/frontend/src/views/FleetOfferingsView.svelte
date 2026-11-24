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
  import ProvenanceMark from '../components/ProvenanceMark.svelte';
  import Select from '../components/Select.svelte';
  import LazyView from '../components/LazyView.svelte';
  import { loadOfferingsFigure } from '../lib/lazyFigures.ts';
  import { urlInt, limitParam, pastEnd } from '../lib/paging.ts';
  import { formatCount } from '../lib/format.ts';
  import { formatDateTime } from '../lib/dateFormat.ts';
  import { flip } from 'svelte/animate';
  import { MOTION, dur } from '../lib/motion.ts';

  // The platform-offering list: /api/fleet/entities?kinds=offering, with search, the
  // lifecycle and failed-request filters, order and paging in the URL. The figure reads
  // the fleet phase tally over every matched offering (B3); each row says who maintains
  // the offering, its worst lifecycle, whether it is available and how Pacto knows, how
  // many consume it, how many of those have a failed request and its observed targets.
  // `inferred` switches the same list to the offerings producers inferred (declaration=
  // inferred): muted rows that never count as declared, each opening its contributed service.
  // `consumable` keeps the offerings the viewer may consume, each row with one muted line
  // naming its version, classes and tunable fields; the engine refuses it with inferred.
  let { text = '', lifecycle = '', failed = '', inferred = '', consumable = '', offset = '', limit = '', sort = '', refreshTick = 0 } = $props();

  const PAGE_SIZE = 25;
  // The figure's ranking (T3): the offerings with the most consumers with a failed request first.
  const DEFAULT_SORT = 'failed';
  const pageOffset = $derived(urlInt(offset));
  const pageLimit = $derived(urlInt(limit) || PAGE_SIZE);
  const order = $derived(sort || DEFAULT_SORT);
  const onlyFailed = $derived(!!failed);
  const onlyInferred = $derived(!!inferred);
  const onlyConsumable = $derived(!!consumable && !onlyInferred);
  const anyFilter = $derived(!!(text || lifecycle || onlyFailed || onlyInferred || onlyConsumable));

  let textDraft = $state(untrack(() => text));
  let submittedText = null;
  $effect(() => { if (text !== submittedText) textDraft = text; submittedText = null; });

  const loader = createProductLoader(() => api.fleetEntities({
    kinds: ['offering'], text: text || undefined, lifecycle: lifecycle || undefined, failed: onlyFailed || undefined,
    declaration: onlyInferred ? 'inferred' : undefined, consumable: onlyConsumable || undefined,
    offset: pageOffset || undefined, limit: pageLimit, sort: order,
  }));
  const queryIdentity = $derived(`${text}@@${lifecycle}@@${onlyFailed}@@${onlyInferred}@@${onlyConsumable}@@${pageOffset}@@${pageLimit}@@${order}`);
  const listIdentity = $derived(`${text}@@${lifecycle}@@${onlyFailed}@@${onlyInferred}@@${onlyConsumable}@@${order}`);
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
  // Inferred offerings are never counted as declared: the declared answer only says how
  // many the inferred switch would list.
  const inferredTotal = $derived(onlyInferred ? 0 : (list?.inferredTotal ?? 0));

  const urlWith = (patch, off = 0, lim = pageLimit) => fleetOfferingsUrl({
    text: (patch.text ?? text) || undefined, lifecycle: (patch.lifecycle ?? lifecycle) || undefined, failed: patch.failed ?? onlyFailed,
    inferred: patch.inferred ?? onlyInferred, consumable: patch.consumable ?? onlyConsumable, offset: off, limit: limitParam(lim, limit, PAGE_SIZE), sort: (patch.sort ?? sort) || undefined,
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
    onlyInferred ? { key: 'inferred', label: 'Shows', value: 'inferred' } : null,
    onlyConsumable ? { key: 'consumable', label: 'Consumable', value: 'yes' } : null,
  ].filter(Boolean));
  const BOOLEAN_FILTERS = new Set(['failed', 'inferred', 'consumable']);
  function removeChip(key) { location.hash = urlWith({ [key]: BOOLEAN_FILTERS.has(key) ? false : '' }); }
  // No inferred row has a failed request or a lifecycle: the inferred list keeps the search alone.
  // Consumable and inferred exclude each other: checking one clears the other.
  const INFERRED_ON = { inferred: true, failed: false, lifecycle: '', consumable: false };
  const CONSUMABLE_ON = { consumable: true, inferred: false };
  // The consumable row's muted line: the first provider's version, classes and tunable
  // fields, then how many more providers; the title adds the publisher and the asOf.
  const consumableLine = (c) => {
    const p = c.providers?.[0];
    if (!p) return null;
    const tunable = [...new Set((p.parameters ?? []).filter((x) => x.tier === 'tunable').map((x) => x.path))];
    const more = (c.providersTotal ?? 0) - 1;
    const line = [
      p.version,
      p.classes?.length ? `classes ${p.classes.join(', ')}` : '',
      tunable.length ? `tunable ${tunable.join(', ')}` : '',
      more > 0 ? `+${plural(more, 'more provider', 'more providers')}` : '',
    ].filter(Boolean).join(' · ');
    const asOf = list?.meta?.asOf ? `, as of ${formatDateTime(list.meta.asOf)}` : '';
    return { line, title: `${line}; publisher ${p.publisher}${asOf}` };
  };
  const inferredHref = $derived(urlWith(INFERRED_ON));
  // The inferred row's tip: who produced it, from which source object and when.
  const inferredTip = (f) => [
    `Inferred by ${f.producer}`,
    f.from ? `from ${f.from}${f.generation ? ` generation ${f.generation}` : ''}` : '',
    f.classes?.length ? `classes ${f.classes.join(', ')}` : '',
    f.asOf ? `as of ${formatDateTime(f.asOf)}` : '',
  ].filter(Boolean).join(', ') + '. A producer\'s record, never a contract: it never counts as declared or clears a gate.';
  // An offering a person already accepted names the declared one instead of the accept hint.
  const inferredLine = (f) => `Inferred from ${f.from || f.producer}${f.classes?.length ? `, classes ${f.classes.join(', ')}` : ''}; ${f.acceptedAs ? 'accepted as' : 'accept to make it consumable'}`;
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
    count={list ? plural(total, onlyInferred ? 'inferred offering' : 'offering', onlyInferred ? 'inferred offerings' : 'offerings') : ''}
    subtitle="Services that platform teams publish for other teams to use, grouped by the service.offering their contracts declare."
    asOf={list?.meta?.asOf}
    {revalidating} paging={view.paging}
  >
    {#if inferredTotal && !noneDeclared}<a class="t-meta" href={inferredHref} data-testid="offering-inferred-total">+{formatCount(inferredTotal)} inferred</a>{/if}
  </PageHeader>

  {#if agg?.offeringCount && state.kind === 'ready'}
    <LazyView load={loadOfferingsFigure} aggregate={agg} {failedHref} meta={list?.meta} />
  {/if}

  <div class="ov-filters">
    <form class="lv-search" onsubmit={submitSearch} role="search">
      <input class="input" type="search" bind:value={textDraft} placeholder="Search offerings..." aria-label="Search offerings" />
      <button type="submit" class="btn">Search</button>
    </form>
    {#if !onlyInferred}
    <Select label="Lifecycle" value={lifecycle} options={LIFECYCLES} onchange={(e) => { location.hash = urlWith({ lifecycle: e.currentTarget.value }); }} />
    <label class="ov-failed">
      <input type="checkbox" checked={onlyFailed} onchange={(e) => { location.hash = urlWith({ failed: e.currentTarget.checked }); }} data-testid="offering-failed-filter" />
      Only offerings with a failed request
    </label>
    {/if}
    <label class="ov-failed">
      <input type="checkbox" checked={onlyInferred} onchange={(e) => { location.hash = urlWith(e.currentTarget.checked ? INFERRED_ON : { inferred: false }); }} data-testid="offering-inferred-filter" />
      Inferred offerings
    </label>
    <label class="ov-failed">
      <input type="checkbox" checked={onlyConsumable} onchange={(e) => { location.hash = urlWith(e.currentTarget.checked ? CONSUMABLE_ON : { consumable: false }); }} data-testid="offering-consumable-filter" />
      Only offerings you may consume
    </label>
    <ListSort value={order} options={[['failed', 'Most consumers with a failed request first'], ['consumers', 'Most consumers first'], ['name', 'Name'], ['services', 'Most providers first']]} href={sortHref} testid="offering-sort" />
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
      {#if inferredTotal}<p><a class="t-meta" href={inferredHref} data-testid="offering-inferred-total">+{formatCount(inferredTotal)} inferred</a></p>{/if}
    </div>
  {:else if state.kind !== 'ready'}
    <ProductEmptyState {state} noun={onlyConsumable ? 'offerings you may consume' : 'offerings'} onRetry={load} onClearFilters={anyFilter ? clearAll : null} {skeletonRows} />
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
            {@const hiddenP = ref.hiddenProviders ?? 0}
            {@const f = ref.inferredOffering}
            {@const cl = ref.consumable ? consumableLine(ref.consumable) : null}
            <tr class:ot-inferred={!!f} animate:flip={{ duration: dur(MOTION.row) }}>
              {#if f}
              <th scope="row">
                <span class="ot-inferred-name"><EntityLink {ref} showStatus={false} showKind={false} /><span data-testid="offering-inferred"><ProvenanceMark label={`Inferred offering: from ${f.producer}`} text={inferredTip(f)} /></span></span>
              </th>
              <td colspan="6" data-label="Inferred"><span class="ot-note" title={inferredLine(f) + (f.acceptedAs ? ` ${f.acceptedAs.label}` : '')}><bdi>{inferredLine(f)}</bdi>{#if f.acceptedAs}{' '}<span data-testid="offering-inferred-accepted"><EntityLink ref={f.acceptedAs} showStatus={false} showKind={false} /></span>{/if}</span></td>
              {:else}
              <th scope="row">
                <span class="cell-stack"><EntityLink {ref} showStatus={false} showKind={false} /><span class="t-meta">{plural(ref.services ?? 0, 'provider', 'providers')}</span>{#if cl}<span class="t-meta cell-clip" title={cl.title} data-testid="offering-consumable"><bdi data-format="consumable">{cl.line}</bdi></span>{/if}{#if hiddenP}<span class="t-meta" data-testid="offering-hidden-providers">+{plural(hiddenP, 'provider', 'providers')} you may not see</span>{/if}</span>
              </th>
              <td data-label="Owners">{#if ref.owner}<span class="cell-clip" title={ref.owner}><bdi>{ref.owner}</bdi></span>{:else}<span class="t-meta">none declared</span>{/if}</td>
              <td data-label="Lifecycle"><span class="badge badge-{lc[1]}">{lc[0]}</span></td>
              <td data-label="Availability"><span class="badge badge-{AVAILABILITY_TONE[av] ?? 'neutral'}" title={ref.availabilityProvenance ? `${av}, ${ref.availabilityProvenance}` : av}>{#if av === 'unknown'}<span class="ot-hatch hatch" aria-hidden="true"></span>{/if}{availabilityLabel(ref)}</span></td>
              <td class="num" data-label="Consumers">
                {#if consumers}<a href={fleetServicesUrl({ consumes: ref.key })}>{formatCount(consumers)}</a>{:else}<span data-nofilter>0</span>{/if}{#if hidden}<span class="t-meta ot-note" data-testid="offering-hidden-consumers">+{formatCount(hidden)} you may not see</span>{/if}
              </td>
              <td class="num" data-label="Failed">
                {#if failedN}<a href={fleetServicesUrl({ consumes: ref.key, phase: 'failed' })}>{formatCount(failedN)}</a>{:else}<span data-nofilter>{hidden ? '0 seen' : '0'}</span>{/if}{#if hidden}<span class="t-meta ot-note">of the consumers you may see</span>{/if}
              </td>
              <td class="num" data-label="Observed targets">
                {#if ref.targetsUnknown}<span class="t-meta" title="You may not see every target of these providers, or a target source is incomplete">unknown</span><span class="visually-hidden">: you may not see every target of these providers, or a target source is incomplete</span>{:else}<span data-nofilter>{formatCount(ref.targets ?? 0)}</span>{/if}
              </td>
              {/if}
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
  /* The input yields its intrinsic width, so the button stays inside the form at its floor. */
  .lv-search .input { flex: 1; min-width: 0; }
  .ov-failed { display: inline-flex; align-items: center; gap: var(--sp-2); font-size: var(--text-sm); color: var(--c-text-2); }
  @media (pointer: coarse) { .ov-failed { min-height: 44px; } }
  .ov-empty { padding: var(--sp-6) var(--sp-4); text-align: center; color: var(--c-text-2); }
  .ov-empty h2, .ov-empty p { margin: 0 0 var(--sp-2); }
  .ot .num { text-align: right; font-variant-numeric: tabular-nums; }
  .ot-sort { color: inherit; text-decoration: none; }
  .ot-sort:hover, .ot-sort:focus-visible { text-decoration: underline; }
  /* The words beside a hidden count stay visible: a title is unreachable on touch. */
  .ot-note { display: block; overflow-wrap: anywhere; }
  /* An inferred row is muted through the AA-safe text token, never a fade (axe fails faded text). */
  .ot-inferred, .ot-inferred :global(.ei-label), .ot-inferred :global(.ei-context) { color: var(--c-text-3); }
  .ot-inferred-name { display: inline-flex; align-items: center; gap: var(--sp-2); min-width: 0; overflow-wrap: anywhere; }
  /* The grey hatch marks unknown; it sits beside the word, never behind text. */
  .ot-hatch { display: inline-block; inline-size: 0.75em; block-size: 0.75em; margin-inline-end: var(--sp-1); border-radius: 2px; vertical-align: middle; box-shadow: inset 0 0 0 1px var(--c-neutral); }
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
