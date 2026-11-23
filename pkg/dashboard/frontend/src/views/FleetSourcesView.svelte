<script>
  import { onDestroy, untrack } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader, productListState } from '../lib/productLoader.svelte.ts';
  import { sourceHealthLabel, sourceHealthTallyParts, sourceHealthTone } from '../lib/entityLabels.ts';
  import { SOURCE_STATES, bucketLabel, sourceSegments } from '../lib/distributions.ts';
  import DistributionBar from '../components/viz/DistributionBar.svelte';
  import { fleetEntityUrl, fleetOverviewUrl, fleetSourcesUrl } from '../lib/router.ts';
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
  import { formatDate } from '../lib/dateFormat.ts';
  import LazyView from '../components/LazyView.svelte';
  import DetectedProviders from '../components/DetectedProviders.svelte';
  import ConnectAgent from '../components/ConnectAgent.svelte';
  import { loadTrustFigure } from '../lib/lazyFigures.ts';
  import HelpTip from '../components/HelpTip.svelte';
  import { clusterWord, clusterReason, showClusters, shortKey, joinNameTip, joinHostsTip, approvedWords, removedWords, snapshotWords, joinCounts, hiddenCandidateCount } from '../lib/clusters.ts';
  import { linkWords, linkTip } from '../lib/clusterLink.ts';
  import ProvenanceMark from '../components/ProvenanceMark.svelte';
  import ClusterApprove from '../components/ClusterApprove.svelte';
  import ClusterCandidates from '../components/ClusterCandidates.svelte';
  import LimitationsList from '../components/LimitationsList.svelte';

  // The product Sources list: source discovery through
  // /api/fleet/entities?kinds=source via the SDK facade, with a search box and a
  // health filter (the backend sourceHealth query param), all kept in the URL. Rows
  // navigate to the rich source page.
  // `sourceKind` narrows to one adapter kind: the list a figure's kind tally opens.
  let { text = '', sourceHealth = '', sourceKind = '', offset = '', limit = '', sort = '', refreshTick = 0, mcp = undefined } = $props();

  const PAGE_SIZE = 25;
  const HEALTH_OPTIONS = ['available', 'partial', 'stale', 'unavailable', 'incomplete'];
  const pageOffset = $derived(urlInt(offset));
  const pageLimit = $derived(urlInt(limit) || PAGE_SIZE);
  // Least healthy first by default: the sources worth a look are the broken ones.
  const order = $derived(sort || 'health');
  const anyFilter = $derived(!!(text || sourceHealth || sourceKind));

  // Seeded once; the effect below keeps it in step with the URL.
  let textDraft = $state(untrack(() => text));
  // The text the last submit wrote to the URL. Its echo reaches `text` a hashchange later,
  // by when the reader may have typed on; adopting it would drop those keystrokes.
  let submittedText = null;
  $effect(() => { if (text !== submittedText) textDraft = text; submittedText = null; });

  // One reusable, race-safe loader: dedupes the initial load and
  // guards against a stale response overwriting a newer route/filter/refresh.
  const loader = createProductLoader(() => api.fleetEntities({ kinds: ['source'], text: text || undefined, sourceHealth: sourceHealth || undefined, sourceKind: sourceKind || undefined, offset: pageOffset || undefined, limit: pageLimit, sort: order }));
  // queryIdentity is the QUESTION (search + health filter + page); refreshTick only
  // re-asks it. Rows are retained across a re-ask and never across a different question.
  const queryIdentity = $derived(`${text}@@${sourceHealth}@@${sourceKind}@@${pageOffset}@@${pageLimit}@@${order}`);
  // listIdentity is the same question without its page: a page turn keeps the rows.
  const listIdentity = $derived(`${text}@@${sourceHealth}@@${sourceKind}@@${order}`);
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
  // The advisory sources the state bar leaves out (sourceCounts skips one that is not
  // available), named under it so the header's total and the bar's sum reconcile.
  const untallied = $derived((list?.meta?.sources ?? []).filter((s) => s?.advisory && s?.status !== 'available'));

  // The figure names the least healthy sources of the WHOLE population, whatever the
  // list is filtered or paged to: a second question, so a second request (S7).
  const worstLoader = createProductLoader(() => api.fleetEntities({ kinds: ['source'], sort: 'health', limit: 8 }));
  $effect(() => { worstLoader.sync(`worst@@${refreshTick}`, 'worst'); });
  onDestroy(() => worstLoader.destroy());
  // The figure counts what sourceCounts counts, so it names no untallied advisory source.
  const worst = $derived((worstLoader.data?.entities ?? []).filter((e) => !untallied.some((s) => s.id === e.key)));

  function urlWith(patch, off = 0, lim = pageLimit) {
    return fleetSourcesUrl({
      text: patch.text ?? text, sourceHealth: patch.sourceHealth ?? sourceHealth, sourceKind: patch.sourceKind ?? sourceKind, offset: off,
      limit: limitParam(lim, limit, PAGE_SIZE), sort: (patch.sort ?? sort) || undefined,
    });
  }
  function submitSearch(e) { e.preventDefault(); submittedText = textDraft; location.hash = urlWith({ text: textDraft }); }
  function clearAll() { location.hash = fleetSourcesUrl(); }
  const chips = $derived([
    text ? { key: 'text', label: 'Search', value: text } : null,
    sourceHealth ? { key: 'sourceHealth', label: 'Health', value: sourceHealthLabel(sourceHealth) } : null,
    sourceKind ? { key: 'sourceKind', label: 'Kind', value: sourceKind } : null,
  ].filter(Boolean));
  function removeChip(key) { location.hash = urlWith({ [key]: '' }); }

  // Each cluster's status (514-S2), from the latest answer: shown with two or more
  // clusters or when a restricted view hides one. An access mark is neutral, never a
  // status tone: it says what this reader may see, not how the cluster is doing.
  const clusters = $derived(loader.data?.meta?.clusters ?? []);
  const clusterTone = (c) => (c.access ? 'neutral' : sourceHealthTone(c.status));

  // The member join list (514-S3): null on a host that reads no joins, which leaves the
  // block exactly as above. Pending rows, removed rows and the counts show inside it;
  // an error shows inside it too, never as an empty list.
  let joins = $state(null);
  let joinsError = $state('');
  $effect(() => {
    void refreshTick;
    let live = true;
    api.fleetClusters().then(
      (j) => { if (live) { joins = j; joinsError = ''; } },
      // Rows from an earlier read would pass for current, so only the error shows.
      (e) => { if (live) { joins = null; joinsError = e?.message || 'Pacto could not read the member clusters.'; } },
    );
    return () => { live = false; };
  });
  const joinRows = $derived(joins?.clusters ?? []);
  const pending = $derived(joinRows.filter((r) => r.state === 'pending'));
  const removed = $derived(joinRows.filter((r) => r.state === 'removed'));
  const approved = $derived(new Map(joinRows.filter((r) => r.state === 'approved').map((r) => [r.uid, r])));
  const counts = $derived([...joinCounts(joins?.hidden ?? 0, joins?.otherHubs ?? 0, (joins?.limitations ?? []).some((l) => l.source === 'cluster-join-approver')),
    hiddenCandidateCount(joins?.hiddenCandidates ?? 0)].filter(Boolean));
  // The candidate clusters (514-S5): a hub with candidates and no member yet still shows the block.
  const candidates = $derived(joins?.candidates ?? []);
  const memberIds = $derived(new Set(clusters.map((c) => c.id)));
  const memberAnchor = (uid) => `sc-member-${uid}`;
  const joinLimits = $derived(joins?.limitations ?? []);
  const showBlock = $derived(showClusters(clusters) || !!joinsError || pending.length > 0 || removed.length > 0 || counts.length > 0 || joinLimits.length > 0 || candidates.length > 0);
</script>

<div class="product-page">
  <Breadcrumbs trail={[{ label: 'Overview', href: fleetOverviewUrl() }, { label: 'Data sources' }]} />
  <!-- The distinction a first-time user needs here: a data source supplies records to
       the snapshot, so a degraded one makes every other number incomplete rather than
       zero. It is not a collector -- it does not observe anything by itself. -->
  <PageHeader
    glyph="source"
    title="Data sources"
    count={list ? `${formatCount(total)} data source${total === 1 ? '' : 's'}` : ''}
    subtitle="Where this view's knowledge came from. When a data source is degraded, the counts elsewhere are incomplete."
    asOf={list?.meta?.asOf}
    {revalidating} paging={view.paging}
  />

  {#if tallyParts.length}
    <LazyView load={loadTrustFigure} mode="sources" counts={list?.meta?.sourceCounts} meta={list?.meta} {worst} asOf={list?.meta?.asOf ? formatDate(list.meta.asOf) : ''}
      stateHref={(st, kind) => urlWith({ text: '', sourceHealth: st, sourceKind: kind ?? '' })}>
      {#snippet aside()}
        <!-- The state bar (SPEC 7 r5): one carrier for the counts, over every source,
             whatever the list is filtered to; each legend item is its filter. -->
        <div class="lv-tally" data-testid="source-tally" data-nofilter>
          <DistributionBar title="Data sources by state" level={2} total={list?.meta?.sourceCounts?.total ?? 0}
            segments={sourceSegments(list?.meta?.sourceCounts, (st) => urlWith({ sourceHealth: st }))}
            selected={bucketLabel(SOURCE_STATES, sourceHealth)} emptyLabel="No data sources reported." />
          {#if untallied.length}
            <p class="t-meta" data-testid="source-untallied">
              {formatCount(untallied.length)} advisory source{untallied.length === 1 ? '' : 's'} not counted:
              {#each untallied as s, i (s.id)}{i ? ', ' : ''}<a href={fleetEntityUrl('source', s.id)}><bdi>{s.id}</bdi></a>{/each}
              <HelpTip label="advisory sources" text="An advisory source only adds hints, such as the calls a service mesh records. While it is not available it stays out of these counts, since it never makes an answer incomplete." />
            </p>
          {/if}
        </div>
      {/snippet}
    </LazyView>
  {/if}

  {#if showBlock}
    <section class="sc" data-testid="source-clusters" aria-labelledby="sc-title">
      <h2 id="sc-title" class="t-subsection-title">Clusters</h2>
      <ul class="sc-list">
        {#each clusters as c, i (`${c.id}@@${i}`)}
          {@const link = linkWords(c, approved.has(c.id))}
          <li class="sc-chip tone-{clusterTone(c)}" id={memberAnchor(c.id)} tabindex="-1" data-testid="source-cluster">
            <span class="sc-dot" aria-hidden="true"></span>
            <bdi title={c.name || c.id}>{c.name || c.id}</bdi>
            <span class="sc-word">{clusterWord(c)}</span>
            {#if link}<span class="sc-link" title={link}>{link}</span>{/if}
            <HelpTip label={`cluster ${c.name || c.id}`} text={clusterReason(c) + approvedWords(approved.get(c.id)) + linkTip(c, approved.has(c.id))} />
          </li>
        {/each}
      </ul>
      {#if loader.data?.meta?.clustersTruncated}<p class="t-meta">Showing the {formatCount(clusters.length)} least healthy clusters.</p>{/if}
      {#if joinsError}<p class="sc-error" role="alert" data-testid="cluster-join-error">{joinsError}</p>{/if}
      {#if pending.length}
        <ul class="sc-joins" data-testid="cluster-join-pending">
          {#each pending as r (r.uid)}
            <li class="sc-join" data-testid="cluster-join-row">
              <p class="sc-line">
                <span class="sc-state">pending</span>
                <bdi class="sc-name" title={r.name || r.uid}>{r.name || r.uid}</bdi>
                <ProvenanceMark label={`Where the name of ${r.name || r.uid} came from`} text={joinNameTip(r)} />
                {#if r.keyID}<code class="sc-key"><bdi title={`sha256:${r.keyID}`}>{shortKey(r.keyID)}</bdi></code>{/if}
              </p>
              {#if r.environments?.length}
                <p class="sc-line sc-hosts">
                  <span>hosts: <bdi title={r.environments.map((e) => e.name).join(', ')}>{r.environments.map((e) => e.name).join(', ')}</bdi></span>
                  <ProvenanceMark label={`Where the environments of ${r.name || r.uid} came from`} text={joinHostsTip(r)} />
                </p>
              {/if}
              {#if r.mayReplace}<p class="sc-reason" data-testid="cluster-join-replace">may replace <bdi title={r.mayReplace.name || r.mayReplace.uid}>{r.mayReplace.name || r.mayReplace.uid}</bdi></p>{/if}
              {#if snapshotWords(r)}<p class="sc-reason" data-testid="cluster-join-snapshot">{snapshotWords(r)}</p>{/if}
              {#if r.reason}<p class="sc-reason">{r.reason}</p>{/if}
              {#if joins?.canApprove && r.keyID}<ClusterApprove uid={r.uid} keyID={r.keyID} name={r.name} registry={joins.repository} />{/if}
            </li>
          {/each}
        </ul>
      {/if}
      {#if removed.length}
        <details class="disclosure sc-removed" data-testid="cluster-join-removed">
          <summary><span class="disclosure-caret" data-motion aria-hidden="true"></span>{removed.length} removed</summary>
          <ul class="sc-joins">
            {#each removed as r (r.uid)}
              <li class="sc-line"><bdi class="sc-name" title={r.name || r.uid}>{r.name || r.uid}</bdi>{#if removedWords(r)}<span class="sc-reason">{removedWords(r)}</span>{/if}{#if r.reason}<span class="sc-reason">{r.reason}</span>{/if}</li>
            {/each}
          </ul>
        </details>
      {/if}
      {#if candidates.length}<ClusterCandidates {candidates} asOf={joins?.candidatesAsOf} completeness={joins?.candidatesCompleteness} members={memberIds} {memberAnchor} />{/if}
      {#if counts.length}<p class="t-meta" data-testid="cluster-join-counts">{counts.join('; ')}.</p>{/if}
      {#if joinLimits.length}<LimitationsList items={joinLimits} />{/if}
    </section>
  {/if}

  <DetectedProviders {refreshTick} />
  <!-- installation.mcp from App's capabilities: where an agent connects. -->
  <ConnectAgent {mcp} />

  <!-- Ends the Detected providers section in the heading outline without a visible block. -->
  <h2 class="visually-hidden" data-testid="sources-inventory-title">All data sources</h2>
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
  .sc { display: flex; flex-direction: column; gap: var(--sp-2); min-inline-size: 0; }
  .sc h2, .sc p { margin: 0; }
  .sc-list { list-style: none; margin: 0; padding: 0; display: flex; flex-wrap: wrap; gap: var(--sp-2); min-inline-size: 0; }
  .sc-chip { display: inline-flex; align-items: center; gap: var(--sp-2); max-inline-size: 100%; min-inline-size: 0; padding: 2px var(--sp-1) 2px var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-pill); font-size: var(--text-sm); }
  .sc-dot { flex: none; inline-size: 8px; block-size: 8px; border-radius: 50%; background: var(--tone-c); }
  /* A 63-character cluster name ends in an ellipsis; its title holds it whole. */
  /* A matched candidate card focuses its chip by script, which :focus-visible skips after a click. */
  .sc-chip:focus { outline: 2px solid var(--c-accent); outline-offset: 2px; }
  .sc-chip bdi { min-inline-size: 0; max-inline-size: 28ch; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 600; }
  .sc-word { flex: none; color: var(--c-text-2); }
  /* The member's channel and identity model (514-S4): discreet, details in the tip. */
  .sc-link { min-inline-size: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--c-text-3); }
  /* Pending members (514-S3): one row each, wrapping at any width; hosts are inferred,
     so muted. A 63-character name and the key end in an ellipsis with a title. */
  .sc-joins { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); min-inline-size: 0; }
  .sc-join { display: flex; flex-direction: column; gap: var(--sp-1); min-inline-size: 0; padding: var(--sp-2) var(--sp-3); border: 1px dashed var(--c-border); border-radius: var(--radius-sm); }
  .sc-line { display: flex; flex-wrap: wrap; align-items: center; gap: var(--sp-2); min-inline-size: 0; margin: 0; font-size: var(--text-sm); }
  .sc-state { flex: none; color: var(--c-text-2); font-weight: 600; }
  .sc-name { min-inline-size: 0; max-inline-size: 28ch; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 600; }
  .sc-key { flex: none; font-family: var(--font-mono); font-size: var(--text-xs); color: var(--c-text-2); }
  .sc-hosts, .sc-hosts bdi { color: var(--c-text-3); min-inline-size: 0; overflow-wrap: anywhere; }
  .sc-reason { margin: 0; font-size: var(--text-sm); color: var(--c-text-2); overflow-wrap: anywhere; min-inline-size: 0; }
  .sc-error { margin: 0; color: var(--c-warn); overflow-wrap: anywhere; }
  .sc-removed .sc-joins { margin-top: var(--sp-2); }
  .tone-ok { --tone-c: var(--c-ok); }
  .tone-warn { --tone-c: var(--c-warn); }
  .tone-err { --tone-c: var(--c-err); }
  .tone-neutral { --tone-c: var(--c-neutral); }
  /* Inline links inside a sentence, so they are underlined rather than only accent-
     coloured -- the same rule the overview's "See all" follows (WCAG 1.4.1). */
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
