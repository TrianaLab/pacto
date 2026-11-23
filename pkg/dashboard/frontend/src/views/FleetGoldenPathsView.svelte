<script>
  import { onDestroy, tick, untrack } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader, productListState } from '../lib/productLoader.svelte.ts';
  import { fleetOverviewUrl, fleetGoldenPathsUrl, fleetServicesUrl } from '../lib/router.ts';
  import { SIGNATURE_STATES } from '../lib/distributions.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import KnowledgeBanner from '../components/KnowledgeBanner.svelte';
  import EntityLink from '../components/EntityLink.svelte';
  import EntityStatusBadge from '../components/EntityStatusBadge.svelte';
  import IdentityBadge from '../components/IdentityBadge.svelte';
  import ProductEmptyState from '../components/ProductEmptyState.svelte';
  import StaleRefreshNotice from '../components/StaleRefreshNotice.svelte';
  import ActiveFilterChips from '../components/ActiveFilterChips.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import Pager from '../components/Pager.svelte';
  import ListSort from '../components/ListSort.svelte';
  import { urlInt, limitParam, pastEnd } from '../lib/paging.ts';
  import { formatCount } from '../lib/format.ts';
  import { formatDateTime } from '../lib/dateFormat.ts';
  import ProvenanceMark from '../components/ProvenanceMark.svelte';
  import DeclarePanel from '../components/DeclarePanel.svelte';
  import LazyView from '../components/LazyView.svelte';
  import { isTemplateKey } from '../lib/declare.ts';
  import { loadOriginFigure, loadCreationExplainer } from '../lib/lazyFigures.ts';

  // The Golden paths list: /api/fleet/entities?kinds=service&template=only, with search,
  // order and paging in the URL. A golden path is a service whose contract declares
  // service.template and that runs nowhere; each row counts the services created from it
  // and how many of those are non-compliant. The default order is the origin figure's
  // ranking (most services created first, T3).
  // Golden paths a catalog holds (inferredTemplates) follow the declared rows on the
  // last declared page, muted, with one provenance mark each: never signed, declared
  // or counted. declaration narrows to declared or inferred ones; declare opens the
  // Declare panel of an inferred one and signed is the intent a signing login came
  // back with. canAccept: this caller may accept one (capabilities.declareTemplate);
  // declares: the host declares for this caller at all (capabilities.declare), null
  // while the capabilities are unknown.
  let { text = '', offset = '', limit = '', sort = '', declaration = '', declare = '', signed = '', canAccept = false, declares = null, refreshTick = 0 } = $props();

  const PAGE_SIZE = 25;
  const GUIDE = 'https://pacto.run/latest/golden-paths/';
  // The fleet checks who signed a path against a publisher rule, pin or policy;
  // the path's own contract never decides who may sign it.
  const SIGNATURE_NOTE = 'Checked against a publisher rule, pin or policy. A path\'s own contract never decides who may sign it.';
  const pageOffset = $derived(urlInt(offset));
  const pageLimit = $derived(urlInt(limit) || PAGE_SIZE);
  const order = $derived(sort || 'created');
  const anyFilter = $derived(!!text || !!declaration);
  const INFERRED_SHOWN = 5;

  let textDraft = $state(untrack(() => text));
  let submittedText = null;
  $effect(() => { if (text !== submittedText) textDraft = text; submittedText = null; });

  const loader = createProductLoader(() => api.fleetEntities({ kinds: ['service'], template: 'only', text: text || undefined, declaration: declaration || undefined, offset: pageOffset || undefined, limit: pageLimit, sort: order }));
  const queryIdentity = $derived(`${text}@@${declaration}@@${pageOffset}@@${pageLimit}@@${order}`);
  const listIdentity = $derived(`${text}@@${declaration}@@${order}`);
  $effect(() => { loader.sync(`${queryIdentity}@@${refreshTick}`, queryIdentity, listIdentity); });
  onDestroy(() => loader.destroy());
  function load() { loader.refresh(); }

  // An inferred golden path is a row too: a list holding only those is no empty list.
  const view = productListState(loader, (d) => [...(d.entities ?? []), ...(d.inferredTemplates?.items ?? [])], () => ({ tag: queryIdentity, family: listIdentity, filtered: anyFilter, pageLimit }));
  const list = $derived(view.data);
  const knowledge = $derived(view.knowledge);
  const state = $derived(view.state);
  const skeletonRows = $derived(view.skeletonRows);
  const refreshError = $derived(view.refreshError);
  const revalidating = $derived(view.revalidating);
  const total = $derived(list?.total ?? 0);
  const limitations = $derived(list?.inferredTemplates?.completeness === 'partial' ? list.inferredTemplates.limitations ?? [] : []);
  // Empty with complete knowledge: say what a golden path is. With incomplete knowledge,
  // or a catalog that could not be read whole, "none yet" would be a guess.
  const noneDeclared = $derived(state.kind === 'empty-fleet' && !limitations.length);

  // The inferred rows follow the declared ones on the last declared page.
  const inferredList = $derived(list?.inferredTemplates ?? null);
  const lastPage = $derived(!!list && pageOffset + (list.entities?.length ?? 0) >= total);
  const inferred = $derived(lastPage ? inferredList?.items ?? [] : []);
  let showAllInferred = $state(false);
  const inferredShown = $derived(showAllInferred ? inferred : inferred.slice(0, INFERRED_SHOWN));
  // The catalog template a declared golden path holds the name of gives it its link.
  const linked = $derived(new Map((inferredList?.declared ?? []).filter((t) => t.declaredAs && t.url).map((t) => [t.declaredAs, t])));
  const readAt = (t) => formatDateTime(t.observedAt || inferredList?.asOf);
  const inferredMark = (t) => `Inferred from ${t.from}, ${t.entity}, ${readAt(t) ? `read ${readAt(t)}` : 'read time unknown'}. Not signed or declared, and never offered for creation until a person accepts it.`;
  // Why a row offers no Accept. While the capabilities are unknown nothing is said; a
  // host that declares nothing for this caller points at the CLI, and only a host that
  // declares but refused the right names it.
  const whyNoAccept = (t) => !t.acceptable ? 'Set dashboard.backstage.url to accept templates from this catalog'
    : t.nameTaken ? 'The name is held by a service that is no golden path'
      : canAccept || declares === null ? ''
        : !declares ? `Accept it from a terminal: pacto declare ${t.entity}`
          : 'Accepting needs update pactos in the installation namespace';

  // The Declare panel of the inferred golden path being accepted: opened by Accept, or
  // by the URL (declare) when a person comes back from a signing login. Only a template
  // key opens it: the panel's heading says it accepts a golden path. The intent the
  // login came back with stays with the open panel, whatever list navigation drops
  // from the URL, until Close.
  let declaring = $state(untrack(() => (isTemplateKey(declare) ? declare : '')));
  let panelSigned = $state(untrack(() => (isTemplateKey(declare) ? signed : '')));
  $effect(() => { if (isTemplateKey(declare)) { declaring = declare; panelSigned = signed; } });
  let acceptHeading = $state(null);
  async function openAccept(key) {
    declaring = key;
    panelSigned = '';
    await tick();
    acceptHeading?.scrollIntoView?.({ block: 'nearest' });
    acceptHeading?.focus();
  }
  const declaringName = $derived(declaring.split('/').pop());

  const current = $derived({ text: text || undefined, declaration: declaration || undefined, sort: sort || undefined });
  const pageHref = (off, lim) => fleetGoldenPathsUrl({ ...current, offset: off, limit: limitParam(lim, limit, PAGE_SIZE) });
  function submitSearch(e) { e.preventDefault(); submittedText = textDraft; location.hash = fleetGoldenPathsUrl({ ...current, text: textDraft }); }
  function clearAll() { location.hash = fleetGoldenPathsUrl(); }
  function removeChip(key) { location.hash = fleetGoldenPathsUrl({ ...current, [key]: undefined }); }
  const DECLARATIONS = [['', 'All'], ['declared', 'Declared'], ['inferred', 'Inferred']];
  const chips = $derived([
    ...(text ? [{ key: 'text', label: 'Search', value: text }] : []),
    ...(declaration ? [{ key: 'declaration', label: 'Showing', value: declaration === 'inferred' ? 'Inferred' : 'Declared' }] : []),
  ]);
  const sig = (s) => SIGNATURE_STATES.find((x) => x.value === s) ?? SIGNATURE_STATES[3];

  // The origin figure asks a second question over every SERVICE (limit 1: only the
  // aggregate is wanted), the population origins partition; the search box narrows the
  // golden path list, never the fleet the figure draws. Its failure never takes the list down.
  const originLoader = createProductLoader(() => api.fleetEntities({ kinds: ['service'], template: 'exclude', limit: 1 }));
  $effect(() => { originLoader.sync(`origins@@${refreshTick}`, 'origins'); });
  onDestroy(() => originLoader.destroy());
  const originAgg = $derived(originLoader.data?.aggregate ?? null);
  const originHrefs = { services: (b) => fleetServicesUrl({ createdFrom: b }), paths: fleetGoldenPathsUrl() };
  let showCreation = $state(false);
</script>

<div class="product-page">
  <Breadcrumbs trail={[{ label: 'Overview', href: fleetOverviewUrl() }, { label: 'Golden paths' }]} />
  <PageHeader
    title="Golden paths"
    count={list ? `${formatCount(total)} golden ${total === 1 ? 'path' : 'paths'}` : ''}
    subtitle="Contracts that platform teams publish for other teams to create services from. Each declares service.template and runs nowhere."
    asOf={list?.meta?.asOf}
    {revalidating} paging={view.paging}
  />

  <div class="gp-filters">
    <form class="gp-search" onsubmit={submitSearch} role="search">
      <input class="input" type="search" bind:value={textDraft} placeholder="Search golden paths..." aria-label="Search golden paths" />
      <button type="submit" class="btn">Search</button>
    </form>
    {#if declaration || inferredList}
      <div class="gp-seg" role="group" aria-label="Declared or inferred" data-testid="golden-path-declaration">
        {#each DECLARATIONS as [v, label] (v)}
          <button type="button" class="btn btn-sm" aria-pressed={declaration === v} onclick={() => (location.hash = fleetGoldenPathsUrl({ ...current, declaration: v || undefined }))}>{v === 'inferred' && inferredList ? `${label} ${formatCount(inferredList.total)}` : label}</button>
        {/each}
      </div>
    {/if}
    <ListSort value={order} options={[['created', 'Most services created'], ['nonCompliant', 'Most non-compliant created'], ['name', 'Name'], ['health', 'Least healthy first']]} href={(v) => fleetGoldenPathsUrl({ ...current, sort: v === 'created' ? undefined : v })} testid="golden-path-sort" />
  </div>
  <ActiveFilterChips {chips} onRemove={removeChip} onClear={clearAll} />

  {#if declaring}
    <section class="gp-accept" aria-labelledby="gp-accept-title" data-testid="golden-path-accept">
      <div class="gp-accept-head">
        <h2 class="t-section-title" id="gp-accept-title" tabindex="-1" bind:this={acceptHeading}>Accept <bdi>{declaringName}</bdi> as a golden path</h2>
        <button type="button" class="btn btn-sm" onclick={() => { declaring = ''; panelSigned = ''; if (declare) location.hash = fleetGoldenPathsUrl(current); }}>Close</button>
      </div>
      {#key declaring}<DeclarePanel serviceKey={declaring} open signed={panelSigned} />{/key}
    </section>
  {/if}

  {#if originAgg && (originAgg.origins?.paths || originAgg.origins?.hidden)}
    <LazyView load={loadOriginFigure} aggregate={originAgg} hrefs={originHrefs} meta={originLoader.data?.meta} />
  {/if}
  <div class="gp-explain">
    <button type="button" class="btn btn-sm" aria-expanded={showCreation} aria-controls="gp-creation" onclick={() => (showCreation = !showCreation)} data-testid="creation-toggle">{showCreation ? 'Hide' : 'Show'} what creating a service involves</button>
  </div>
  <div id="gp-creation">{#if showCreation}<LazyView load={loadCreationExplainer} />{/if}</div>

  {#if knowledge.incomplete && (state.kind === 'ready' || state.kind === 'filtered-empty')}
    <KnowledgeBanner {knowledge} noun="list" />
  {/if}

  {#if refreshError}
    <StaleRefreshNotice noun="golden path list" onRetry={load} />
  {/if}

  <!-- Above every branch: an empty list with a catalog that could not be read whole is
       unknown, never "no golden paths". -->
  {#if limitations.length}
    <div class="gp-partial" data-testid="inferred-partial">
      <p class="t-body-2">Inferred golden paths may be incomplete: {limitations[0].message}</p>
      {#if limitations.length > 1}
        <details class="disclosure"><summary><span class="disclosure-caret" data-motion aria-hidden="true"></span>{formatCount(limitations.length - 1)} more {limitations.length === 2 ? 'reason' : 'reasons'}</summary>
          <ul>{#each limitations.slice(1) as l, i (i)}<li>{l.message}</li>{/each}</ul>
        </details>
      {/if}
    </div>
  {/if}

  <!-- Past the end counts the declared rows only: the inferred block rides on every page. -->
  {#if pastEnd(list, list?.entities?.length ?? 0)}
    <Pager page={list} href={pageHref} count={0} noun="golden paths" testid="golden-path" />
  {:else if state.kind === 'empty-fleet' && limitations.length}
    <p class="t-body-2" role="status" data-testid="golden-paths-unknown">No golden path is known yet.</p>
  {:else if noneDeclared}
    <div class="gp-empty" role="status" data-testid="golden-paths-empty">
      <h2 class="t-section-title">No golden paths yet: a golden path is a contract that declares service.template</h2>
      <p class="t-body-2">Publish one to a registry Pacto reads (pactoVersion 2.4) to list it here. <a href={GUIDE} target="_blank" rel="noopener noreferrer">Create services from golden paths</a></p>
    </div>
  {:else if state.kind !== 'ready'}
    <ProductEmptyState {state} noun="golden paths" onRetry={load} onClearFilters={anyFilter ? clearAll : null} {skeletonRows} />
  {:else}
    <!-- T1/T2 (SPEC 22-24): short values get fixed rem columns, the name and summary share
         the rest, and under 56rem of table width (a container query, so the sidebar or a
         split view counts) every row becomes a card. The source sits under the name. -->
    <div class="cq-cards"><div class="data-scroll">
      <table class="data-table" data-testid="golden-path-list">
        <caption class="visually-hidden">Golden paths</caption>
        <colgroup><col /><col style="width: 26%" /><col style="width: 9rem" /><col style="width: 10rem" /><col style="width: 7rem" /><col style="width: 7.5rem" /></colgroup>
        <thead><tr><th scope="col">Name and source</th><th scope="col">Summary</th><th scope="col">Owner</th><th scope="col">Status and signature</th><th scope="col">Services created</th><th scope="col">Non-compliant</th></tr></thead>
        <tbody>
          {#each list.entities as ref (ref.key)}
            {@const s = sig(ref.signature)}
            {@const source = ref.secondary || ref.domain || ''}
            <tr>
              <th scope="row">
                <span class="gp-name"><EntityLink {ref} showStatus={false} showKind={false} /></span>
                {#if source}<span class="cell-clip t-meta" title={source}>{source}</span>{/if}
                {#if linked.get(ref.key)}
                  {@const t = linked.get(ref.key)}
                  <span class="gp-actions"><a href={t.url} target="_blank" rel="noopener noreferrer" data-testid="path-open-catalog">Open in Backstage</a><ProvenanceMark label="Inferred link" text={`Linked to ${t.entity} in ${t.from} by name`} /></span>
                {/if}
              </th>
              <td data-label="Summary">{ref.summary || ''}</td>
              <td data-label="Owner"><span class="cell-clip" title={ref.owner || 'No owner declared'}>{ref.owner || 'No owner declared'}</span></td>
              <td data-label="Status and signature">
                <span class="cell-badges">
                  <EntityStatusBadge kind="service" status={ref.status} />
                  <span data-testid="path-signature" title={s.value === 'verified' ? SIGNATURE_NOTE : undefined}><IdentityBadge label={s.label} tone={s.value === 'verified' ? 'neutral' : s.tone} /></span>
                </span>
                {#if s.value === 'verified'}<span class="visually-hidden">{SIGNATURE_NOTE}</span>{/if}
              </td>
              <td data-label="Services created"><a data-testid="path-created" href={fleetServicesUrl({ createdFrom: ref.key })}>{formatCount(ref.created ?? 0)}</a></td>
              <td data-label="Non-compliant">{#if ref.nonCompliant}<a data-testid="path-noncompliant" href={fleetServicesUrl({ createdFrom: ref.key, status: 'NonCompliant' })}>{formatCount(ref.nonCompliant)}</a>{:else}0{/if}</td>
            </tr>
          {/each}
          {#each inferredShown as t (t.entity)}
            <tr class="gp-inferred" data-testid="inferred-golden-path">
              <th scope="row">
                <span class="gp-iname">
                  {#if t.url}<a class="cell-clip" href={t.url} target="_blank" rel="noopener noreferrer" title={t.name}><bdi>{t.name}</bdi></a>{:else}<span class="cell-clip" title={t.name}><bdi>{t.name}</bdi></span>{/if}
                  <ProvenanceMark label="Inferred" text={inferredMark(t)} />
                </span>
                <span class="cell-clip t-meta" title={t.from}><bdi>{t.from}</bdi></span>
                <span class="gp-actions">
                  {#if t.url}<a href={t.url} target="_blank" rel="noopener noreferrer">Open in Backstage</a>{:else}<span class="t-meta">Open it in your Backstage</span>{/if}
                  {#if whyNoAccept(t)}<span class="t-meta gp-why" data-testid="inferred-no-accept">{whyNoAccept(t)}</span>{:else if canAccept}<button type="button" class="btn btn-sm" onclick={() => openAccept(t.entity)} data-testid="inferred-accept">Accept as golden path</button>{/if}
                </span>
              </th>
              <td data-label="Summary">{t.summary || t.title || ''}</td>
              <td data-label="Owner"><span class="cell-clip" title={t.owner || 'No owner in the catalog'}><bdi>{t.owner || 'No owner in the catalog'}</bdi></span></td>
              <td data-label="Status and signature">Not declared</td>
              <td data-label="Services created"><span aria-label="not applicable">–</span></td>
              <td data-label="Non-compliant"><span aria-label="not applicable">–</span></td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div></div>
    {#if inferred.length > INFERRED_SHOWN}
      <button type="button" class="btn btn-sm gp-more" aria-expanded={showAllInferred} onclick={() => (showAllInferred = !showAllInferred)} data-testid="inferred-more">{showAllInferred ? 'Show fewer inferred' : `Show ${formatCount(inferred.length - INFERRED_SHOWN)} more inferred`}</button>
    {/if}
    {#if list.entities?.length}<Pager page={list} href={pageHref} label="Golden path pages" noun="golden paths" testid="golden-path" />{/if}
  {/if}
</div>

<style>
  /* The name column is what the fixed columns leave, so it can be narrow: the label
     itself takes the ellipsis (its title holds the whole name). An ellipsis on a box
     around the inline-flex link hid the whole link or cut it with no ellipsis. */
  .gp-name { display: flex; min-width: 0; }
  .gp-name :global(.entity-link), .gp-name :global(.entity-link .entity-identity) { min-width: 0; max-width: 100%; flex-wrap: nowrap; }
  .gp-name :global(.entity-identity .ei-label) { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .gp-filters { display: flex; gap: var(--sp-3); flex-wrap: wrap; align-items: flex-end; }
  .gp-search { display: flex; gap: var(--sp-2); max-width: 420px; flex: 1 1 220px; min-width: 220px; }
  .gp-search .input { flex: 1; min-width: 0; }
  .gp-search .btn { flex: none; }
  .gp-explain { display: flex; justify-content: flex-end; }
  .gp-empty { padding: var(--sp-6) var(--sp-4); text-align: center; color: var(--c-text-2); }
  .gp-empty h2, .gp-empty p { margin: 0 0 var(--sp-2); }
  .gp-empty a { color: var(--c-accent); }
  .data-table td a { color: var(--c-accent); }
  .gp-seg { display: flex; gap: var(--sp-1); flex-wrap: wrap; }
  .gp-seg .btn[aria-pressed='true'] { background: var(--c-accent); border-color: var(--c-accent); color: var(--c-on-accent, #fff); }
  /* Inferred rows are muted; the mark and the "Not declared" text carry the meaning. */
  .gp-inferred, .gp-inferred th { color: var(--c-text-2); font-weight: normal; }
  .gp-iname { display: flex; align-items: center; gap: var(--sp-1); min-width: 0; }
  .gp-iname a { color: inherit; }
  .gp-actions { display: flex; flex-wrap: wrap; align-items: center; gap: var(--sp-1) var(--sp-2); min-width: 0; }
  .gp-actions a { color: var(--c-accent); }
  .gp-why { overflow-wrap: anywhere; white-space: normal; }
  .gp-more { align-self: flex-start; }
  .gp-partial p { margin: 0; overflow-wrap: anywhere; }
  .gp-partial ul { margin: var(--sp-1) 0 0; padding-inline-start: var(--sp-4); overflow-wrap: anywhere; }
  .gp-accept { display: flex; flex-direction: column; gap: var(--sp-2); padding: var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); min-width: 0; }
  .gp-accept-head { display: flex; align-items: center; justify-content: space-between; gap: var(--sp-2); flex-wrap: wrap; }
  .gp-accept-head h2 { margin: 0; overflow-wrap: anywhere; min-width: 0; }
  @media (pointer: coarse) {
    .gp-actions a, .gp-iname a, .gp-partial summary { display: inline-flex; align-items: center; min-block-size: 44px; }
  }
</style>
