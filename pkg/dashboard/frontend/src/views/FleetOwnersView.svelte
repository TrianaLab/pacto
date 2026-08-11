<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { decideViewState, snapshotKnowledge } from '../lib/knowledgeState.ts';
  import { fleetOverviewUrl, fleetOwnersUrl, fleetServicesUrl } from '../lib/router.ts';
  import { ownershipSegments, ownershipHrefs, ownerRanking, segmentTotal } from '../lib/distributions.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import KnowledgeBanner from '../components/KnowledgeBanner.svelte';
  import EntityLink from '../components/EntityLink.svelte';
  import ProductEmptyState from '../components/ProductEmptyState.svelte';
  import StaleRefreshNotice from '../components/StaleRefreshNotice.svelte';
  import ActiveFilterChips from '../components/ActiveFilterChips.svelte';
  import DistributionBar from '../components/viz/DistributionBar.svelte';
  import HorizontalBars from '../components/viz/HorizontalBars.svelte';
  import PageHeader from '../components/PageHeader.svelte';

  // The product Owners list (requirement G): owner discovery through
  // /api/fleet/entities?kinds=owner via the SDK facade, with a search box and stable
  // backend pagination kept in the URL. Rows navigate to the rich owner page.
  let { text = '', offset = '', refreshTick = 0 } = $props();

  const PAGE_SIZE = 25;
  const pageOffset = $derived(Math.max(0, Math.trunc(Number(offset) || 0)));
  const anyFilter = $derived(!!text);

  let textDraft = $state(text);
  $effect(() => { textDraft = text; });

  // One reusable, race-safe loader (requirement E): dedupes the initial load and
  // guards against a stale response overwriting a newer route/filter/refresh.
  const loader = createProductLoader(() => api.fleetEntities({ kinds: ['owner'], text: text || undefined, offset: pageOffset || undefined, limit: PAGE_SIZE }));
  // queryIdentity is the QUESTION (search + page); refreshTick only re-asks it. Rows
  // are retained across a re-ask and never across a different question.
  const queryIdentity = $derived(`${text}@@${pageOffset}`);
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

  function submitSearch(e) { e.preventDefault(); location.hash = fleetOwnersUrl({ text: textDraft }); }
  function clearAll() { location.hash = fleetOwnersUrl(); }
  const chips = $derived(text ? [{ key: 'text', label: 'Search', value: text }] : []);

  // The ownership picture above the inventory.
  //
  // A list of owners answers "who exists", never "is this fleet owned" -- the two
  // failures a reader opens this page about, a service nobody claims and a service two
  // teams claim, are both INVISIBLE in a list of owners, because neither has an owner
  // row to appear under. So the page asks the backend a second, separate question: the
  // ownership aggregate over every SERVICE, which is the population ownership is a
  // property of.
  //
  // It is a fleet-scoped question deliberately. The search box filters the owner
  // inventory; it does not narrow what "the fleet's ownership" means, and it must not,
  // or paging and searching would each redraw the summary into a different fleet. One
  // row of the page (limit 1) is fetched because the aggregate is computed over the
  // complete matched population BEFORE paging -- the rows themselves are not wanted here.
  const ownershipLoader = createProductLoader(() => api.fleetEntities({ kinds: ['service'], limit: 1 }));
  $effect(() => { ownershipLoader.sync(`ownership@@${refreshTick}`, 'ownership'); });
  onDestroy(() => ownershipLoader.destroy());
  const agg = $derived(ownershipLoader.data?.aggregate ?? null);
  const services = $derived(agg?.services ?? 0);
  const ownershipOfFleet = $derived(ownershipSegments(agg?.ownership,
    ownershipHrefs((v) => fleetServicesUrl({ ownership: v }))));
  // Each row counts the services CONSISTENTLY owned by that team, so it drills into both
  // -- see ownerRanking. The destination is the Services list, which is where a
  // population of services can be read; the owner page beside it is one owner's estate,
  // a different question with a different total.
  const rank = $derived(ownerRanking(agg, (o) => fleetServicesUrl({ owner: o, ownership: 'consistent' })));
  // The bars' own denominator, never `services`: if the backend ever tallied a service
  // into no bucket, a bar drawn against the service count would hide the gap in
  // whitespace instead of showing it.
  const ownershipTotal = $derived(segmentTotal(ownershipOfFleet));
</script>

<div class="product-page">
  <Breadcrumbs trail={[{ label: 'Overview', href: fleetOverviewUrl() }, { label: 'Owners' }]} />
  <PageHeader title="Owners" count={list ? `${total} owner${total === 1 ? '' : 's'}` : ''} />

  <form class="lv-search" onsubmit={submitSearch} role="search">
    <input type="search" bind:value={textDraft} placeholder="Search owners..." aria-label="Search owners" />
    <button type="submit" class="btn">Search</button>
  </form>
  <ActiveFilterChips {chips} onRemove={clearAll} onClear={clearAll} />

  {#if knowledge.incomplete && (state.kind === 'ready' || state.kind === 'filtered-empty')}
    <KnowledgeBanner {knowledge} noun="list" />
  {/if}

  {#if refreshError}
    <StaleRefreshNotice noun="owners list" onRetry={load} />
  {/if}

  {#if ownershipTotal > 0}
    <section class="ow-summary" aria-labelledby="ow-sum-h" data-testid="owners-aggregate">
      <div class="ow-sum-head">
        <h2 id="ow-sum-h" class="t-section-title">Ownership across every service</h2>
        <a class="ow-viewall" href={fleetServicesUrl()}>Browse services</a>
      </div>
      <DistributionBar
        title="Declared ownership"
        description="Ownership is authored on each contract revision, so a service is cleanly owned only when its revisions agree. A service nobody claims and a service two teams claim are opposite problems, and neither appears in the owner list below."
        scopeNote={`All ${services} ${services === 1 ? 'service' : 'services'} in the snapshot, whatever this page is filtered or paged to.`}
        segments={ownershipOfFleet}
        total={ownershipTotal}
      />
      <details class="disclosure ow-sum-more">
        <summary>
          <span class="disclosure-caret" aria-hidden="true">&#9656;</span>
          <span>Per-owner breakdown</span>
          <span class="ow-more-count t-meta">{rank.distinct} declared {rank.distinct === 1 ? 'owner' : 'owners'}</span>
        </summary>
        <div class="ow-sum-grid">
          <HorizontalBars
            title="Services per owner"
            description="Who carries the most services."
            scopeNote={rank.note}
            items={rank.services}
            unit="services"
            unitOne="service"
            emptyLabel="No service has a single declared owner to rank by."
          />
          <HorizontalBars
            title="Operational targets per owner"
            description="How much is actually running behind each of those owners."
            scopeNote="Same owners, in the same service-count order as above — this is not a ranking by target count."
            items={rank.targets}
            unit="targets"
            unitOne="target"
            emptyLabel="None of these owners has anything running yet."
          />
        </div>
      </details>
    </section>
  {/if}

  {#if state.kind !== 'ready'}
    <ProductEmptyState {state} noun="owners" onRetry={load} onClearFilters={anyFilter ? clearAll : null} />
  {:else}
    <ul class="lv-list" data-testid="owner-list">
      {#each list.entities as ref (ref.key)}
        <li class="lv-item"><EntityLink {ref} showStatus={false} /></li>
      {/each}
    </ul>
    <nav class="lv-pager" aria-label="Owner pages">
      <span class="lv-range">Showing {shownFrom}–{shownTo} of {total}</span>
      <div class="lv-pager-btns">
        {#if hasPrev}<a class="lv-page" href={fleetOwnersUrl({ text: text || undefined, offset: prevOffset })} data-testid="owner-prev" rel="prev">Previous</a>{:else}<span class="lv-page disabled" aria-disabled="true">Previous</span>{/if}
        {#if hasNext}<a class="lv-page" href={fleetOwnersUrl({ text: text || undefined, offset: list.nextOffset })} data-testid="owner-next" rel="next">Next</a>{:else}<span class="lv-page disabled" aria-disabled="true">Next</span>{/if}
      </div>
    </nav>
  {/if}
</div>

<style>
  /* Same card, same grid and same disclosure as the Services inventory: the two pages
     ask one question in one visual language, one scope apart. */
  .ow-summary { display: flex; flex-direction: column; gap: var(--sp-3); padding-bottom: var(--sp-3); border-bottom: 1px solid var(--c-border); }
  .ow-sum-head { display: flex; align-items: baseline; justify-content: space-between; gap: var(--sp-3); flex-wrap: wrap; }
  .ow-sum-head h2 { margin: 0; }
  .ow-viewall { font-size: var(--text-sm); color: var(--c-accent); text-decoration: none; }
  .ow-viewall:hover { text-decoration: underline; }
  .ow-sum-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 380px), 1fr)); gap: var(--sp-4); align-items: start; }
  .ow-more-count { color: var(--c-text-3); }
  .ow-sum-more[open] > .ow-sum-grid { margin-top: var(--sp-3); }
  .lv-search { display: flex; gap: var(--sp-2); max-width: 420px; }
  .lv-search input { flex: 1; padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); color: var(--c-text); font: inherit; font-size: var(--text-sm); min-height: var(--touch-min); }
  /* The Search control is the shared .btn from styles/components.css. Each list view
     used to carry its own byte-identical copy, which is how one product ends up with
     four Search buttons in three flavours. */
  .lv-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .lv-item { padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); }
  .lv-pager { display: flex; align-items: center; justify-content: space-between; gap: var(--sp-3); flex-wrap: wrap; margin-top: var(--sp-2); }
  .lv-range { color: var(--c-text-3); font-size: var(--text-sm); }
  .lv-pager-btns { display: flex; gap: var(--sp-2); }
  .lv-page { padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); font-size: var(--text-sm); color: var(--c-text); text-decoration: none; background: var(--c-surface); min-height: var(--touch-min); display: inline-flex; align-items: center; }
  .lv-page:hover { border-color: var(--c-accent); }
  .lv-page.disabled { color: var(--c-text-3); opacity: 0.5; pointer-events: none; }
</style>
