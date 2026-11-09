<script>
  import { onDestroy, untrack } from 'svelte';
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
  import LazyView from '../components/LazyView.svelte';
  import { loadOriginFigure, loadCreationExplainer } from '../lib/lazyFigures.ts';

  // The Golden paths list: /api/fleet/entities?kinds=service&template=only, with search,
  // order and paging in the URL. A golden path is a service whose contract declares
  // service.template and that runs nowhere; each row counts the services created from it
  // and how many of those are non-compliant. The default order is the origin figure's
  // ranking (most services created first, T3).
  let { text = '', offset = '', limit = '', sort = '', refreshTick = 0 } = $props();

  const PAGE_SIZE = 25;
  const GUIDE = 'https://pacto.run/latest/golden-paths/';
  // The fleet checks a path's signature only against the signer its own contract
  // declares, so a verified row never says who may publish golden paths.
  const SIGNATURE_NOTE = 'Verified against the signer its own contract declares. Pacto did not check it against a trust config.';
  const pageOffset = $derived(urlInt(offset));
  const pageLimit = $derived(urlInt(limit) || PAGE_SIZE);
  const order = $derived(sort || 'created');
  const anyFilter = $derived(!!text);

  let textDraft = $state(untrack(() => text));
  let submittedText = null;
  $effect(() => { if (text !== submittedText) textDraft = text; submittedText = null; });

  const loader = createProductLoader(() => api.fleetEntities({ kinds: ['service'], template: 'only', text: text || undefined, offset: pageOffset || undefined, limit: pageLimit, sort: order }));
  const queryIdentity = $derived(`${text}@@${pageOffset}@@${pageLimit}@@${order}`);
  const listIdentity = $derived(`${text}@@${order}`);
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
  // Empty with complete knowledge: say what a golden path is. With incomplete knowledge
  // "none yet" would be a guess.
  const noneDeclared = $derived(state.kind === 'empty-fleet');

  const pageHref = (off, lim) => fleetGoldenPathsUrl({ text: text || undefined, offset: off, limit: limitParam(lim, limit, PAGE_SIZE), sort: sort || undefined });
  function submitSearch(e) { e.preventDefault(); submittedText = textDraft; location.hash = fleetGoldenPathsUrl({ text: textDraft, sort: sort || undefined }); }
  function clearAll() { location.hash = fleetGoldenPathsUrl(); }
  const chips = $derived(text ? [{ key: 'text', label: 'Search', value: text }] : []);
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
    <ListSort value={order} options={[['created', 'Most services created'], ['nonCompliant', 'Most non-compliant created'], ['name', 'Name'], ['health', 'Least healthy first']]} href={(v) => fleetGoldenPathsUrl({ text: text || undefined, sort: v === 'created' ? undefined : v })} testid="golden-path-sort" />
  </div>
  <ActiveFilterChips {chips} onRemove={clearAll} onClear={clearAll} />

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

  {#if pastEnd(list, count)}
    <Pager page={list} href={pageHref} count={0} noun="golden paths" testid="golden-path" />
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
              </th>
              <td data-label="Summary">{ref.summary || ''}</td>
              <td data-label="Owner"><span class="cell-clip" title={ref.owner || undefined}>{ref.owner || 'No owner declared'}</span></td>
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
        </tbody>
      </table>
    </div></div>
    <Pager page={list} href={pageHref} label="Golden path pages" noun="golden paths" testid="golden-path" />
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
  .gp-search { display: flex; gap: var(--sp-2); max-width: 420px; flex: 1; min-width: 220px; }
  .gp-search .input { flex: 1; }
  .gp-explain { display: flex; justify-content: flex-end; }
  .gp-empty { padding: var(--sp-6) var(--sp-4); text-align: center; color: var(--c-text-2); }
  .gp-empty h2, .gp-empty p { margin: 0 0 var(--sp-2); }
  .gp-empty a { color: var(--c-accent); }
  .data-table td a { color: var(--c-accent); }
</style>
