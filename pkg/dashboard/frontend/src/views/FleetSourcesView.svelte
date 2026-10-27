<script>
  import { onDestroy, untrack } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { decideViewState, snapshotKnowledge } from '../lib/knowledgeState.ts';
  import { sourceHealthLabel, sourceHealthTallyParts } from '../lib/entityLabels.ts';
  import { fleetOverviewUrl, fleetSourcesUrl } from '../lib/router.ts';
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
  import { flip } from 'svelte/animate';
  import { MOTION, dur } from '../lib/motion.ts';

  // The product Sources list: source discovery through
  // /api/fleet/entities?kinds=source via the SDK facade, with a search box and a
  // health filter (the backend sourceHealth query param), all kept in the URL. Rows
  // navigate to the rich source page.
  let { text = '', sourceHealth = '', offset = '', limit = '', sort = '', refreshTick = 0 } = $props();

  const PAGE_SIZE = 25;
  const HEALTH_OPTIONS = ['available', 'partial', 'stale', 'unavailable'];
  const pageOffset = $derived(urlInt(offset));
  const pageLimit = $derived(urlInt(limit) || PAGE_SIZE);
  // Least healthy first by default: the sources worth a look are the broken ones.
  const order = $derived(sort || 'health');
  const anyFilter = $derived(!!(text || sourceHealth));

  // Seeded once; the effect below keeps it in step with the URL.
  let textDraft = $state(untrack(() => text));
  // The text the last submit wrote to the URL. Its echo reaches `text` a hashchange later,
  // by when the reader may have typed on; adopting it would drop those keystrokes.
  let submittedText = null;
  $effect(() => { if (text !== submittedText) textDraft = text; submittedText = null; });

  // One reusable, race-safe loader: dedupes the initial load and
  // guards against a stale response overwriting a newer route/filter/refresh.
  const loader = createProductLoader(() => api.fleetEntities({ kinds: ['source'], text: text || undefined, sourceHealth: sourceHealth || undefined, offset: pageOffset || undefined, limit: pageLimit, sort: order }));
  // queryIdentity is the QUESTION (search + health filter + page); refreshTick only
  // re-asks it. Rows are retained across a re-ask and never across a different question.
  const queryIdentity = $derived(`${text}@@${sourceHealth}@@${pageOffset}@@${pageLimit}@@${order}`);
  // listIdentity is the same question without its page: a page turn keeps the rows.
  const listIdentity = $derived(`${text}@@${sourceHealth}@@${order}`);
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

  // Fleet-wide source health over the COMPLETE population the backend counted, which
  // is a different question from the page of rows below it and stays true while a
  // filter is on. It answers the one thing the health <select> cannot: how many
  // sources are in each state before you go looking. Each bucket is the filter that
  // selects it, so the summary is also the way in.
  //
  // The population count deliberately stays out of it -- the page header already
  // states the authoritative total, and a second total here would be a second claim
  // to reconcile.
  const tallyParts = $derived(sourceHealthTallyParts(list?.meta?.sourceCounts));

  function urlWith(patch, off = 0, lim = pageLimit) {
    return fleetSourcesUrl({
      text: patch.text ?? text, sourceHealth: patch.sourceHealth ?? sourceHealth, offset: off,
      limit: limitParam(lim, limit, PAGE_SIZE), sort: (patch.sort ?? sort) || undefined,
    });
  }
  function submitSearch(e) { e.preventDefault(); submittedText = textDraft; location.hash = urlWith({ text: textDraft }); }
  function clearAll() { location.hash = fleetSourcesUrl(); }
  const chips = $derived([
    text ? { key: 'text', label: 'Search', value: text } : null,
    sourceHealth ? { key: 'sourceHealth', label: 'Health', value: sourceHealthLabel(sourceHealth) } : null,
  ].filter(Boolean));
  function removeChip(key) { location.hash = urlWith({ [key]: '' }); }
</script>

<div class="product-page">
  <Breadcrumbs trail={[{ label: 'Overview', href: fleetOverviewUrl() }, { label: 'Data sources' }]} />
  <!-- The distinction a first-time user needs here: a data source supplies records to
       the snapshot, so a degraded one makes every other number incomplete rather than
       zero. It is not a collector -- it does not observe anything by itself. -->
  <PageHeader
    title="Data sources"
    count={list ? `${formatCount(total)} data source${total === 1 ? '' : 's'}` : ''}
    subtitle="Where this view's knowledge came from. When a data source is degraded, the counts elsewhere are incomplete — not zero."
    asOf={list?.meta?.asOf}
    {revalidating} paging={answer.paging}
  />

  {#if tallyParts.length}
    <p class="lv-tally t-body-2" data-testid="source-tally">
      Across every data source:
      {#each tallyParts as p, i (p.status || 'unclassified')}{#if i > 0}{', '}{/if}{#if p.status}<a
          href={urlWith({ sourceHealth: p.status })}
          aria-current={sourceHealth === p.status ? 'true' : undefined}>{p.text}</a>{:else}<span
          class="lv-unclassified"
          title="A health state Pacto does not recognize, so it cannot be filtered on">{p.text}</span>{/if}{/each}.
    </p>
  {/if}

  <div class="lv-filters">
    <form class="lv-search" onsubmit={submitSearch} role="search">
      <input class="input" type="search" bind:value={textDraft} placeholder="Search data sources..." aria-label="Search data sources" />
      <button type="submit" class="btn">Search</button>
    </form>
    <label class="lv-field">
      <span>Health</span>
      <select value={sourceHealth} aria-label="Filter by source health" onchange={(e) => (location.hash = urlWith({ sourceHealth: e.currentTarget.value }))}>
        <option value="">Any health</option>
        {#each HEALTH_OPTIONS as h}<option value={h}>{sourceHealthLabel(h)}</option>{/each}
      </select>
    </label>
    <ListSort value={order} href={(v) => urlWith({ sort: v === 'health' ? '' : v })} options={[['health', 'Least healthy first'], ['name', 'Name']]} testid="source-sort" />
  </div>
  <ActiveFilterChips {chips} onRemove={removeChip} onClear={clearAll} />

  {#if knowledge.incomplete && (state.kind === 'ready' || state.kind === 'filtered-empty')}
    <KnowledgeBanner {knowledge} noun="list" />
  {/if}

  {#if refreshError}
    <StaleRefreshNotice noun="data source list" onRetry={load} />
  {/if}

  {#if pastEnd(list, count)}
    <Pager page={list} href={(o, l) => urlWith({}, o, l)} count={0} noun="data sources" testid="source" />
  {:else if state.kind !== 'ready'}
    <ProductEmptyState {state} noun="data sources" onRetry={load} onClearFilters={anyFilter ? clearAll : null} {skeletonRows} />
  {:else}
    <ul class="lv-list pl-list pl-list--cols" data-testid="source-list">
      {#each list.entities as ref (ref.key)}
        <li class="lv-item pl-row" animate:flip={{ duration: dur(MOTION.row) }}><EntityLink {ref} showStatus={true} showKind={false} /></li>
      {/each}
    </ul>
    <Pager page={list} href={(o, l) => urlWith({}, o, l)} label="Data source pages" noun="data sources" testid="source" />
  {/if}
</div>

<style>
  .lv-tally { margin: 0; }
  /* Inline links inside a sentence, so they are underlined rather than only accent-
     coloured -- the same rule the overview's "See all" follows (WCAG 1.4.1). */
  .lv-tally a { color: var(--c-accent); text-decoration: underline; }
  .lv-tally a[aria-current='true'] { font-weight: 700; }
  .lv-unclassified { color: var(--c-text-3); }
  .lv-filters { display: flex; gap: var(--sp-3); flex-wrap: wrap; align-items: flex-end; }
  .lv-search { display: flex; gap: var(--sp-2); flex: 1; min-width: 220px; }
  /* On a phone the search takes its own row, so its button is never squeezed to 'Searc'. */
  @media (max-width: 40rem) { .lv-search { flex: 1 1 100%; min-width: 0; } }
  .lv-search .input { flex: 1; }
  .lv-field { display: flex; flex-direction: column; gap: 2px; font-size: var(--text-xs); color: var(--c-text-3); }
  /* The Search control is the shared .btn, the search box the shared .input, the select the
     shared bare-element rule and the list, row and pager the shared .pl-* rules -- all from
     styles/components.css. Each list view used to carry its own byte-identical copy, which
     is how one product ends up with four Search buttons in three flavours. */
</style>
