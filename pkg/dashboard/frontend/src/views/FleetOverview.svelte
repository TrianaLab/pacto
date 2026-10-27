<script>
  import { onMount, untrack } from 'svelte';
  import { api } from '../lib/api.ts';
  import { formatCount } from '../lib/format.ts';
  import { snapshotKnowledge, decideViewState, allClearAllowed } from '../lib/knowledgeState.ts';
  import { sourceHealthTally, attentionCategoryLabel, ATTENTION_CATEGORIES, splitEntryPoints } from '../lib/entityLabels.ts';
  import { fleetAttentionUrl, fleetSourcesUrl, fleetServicesUrl, fleetOwnersUrl, fleetEntityListUrl } from '../lib/router.ts';
  import { ownershipSegments, ownershipHrefs, readinessSegments } from '../lib/distributions.ts';
  import { formatDateTime } from '../lib/dateFormat.ts';
  import { joinAnd } from '../lib/answers.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import KnowledgeBanner from '../components/KnowledgeBanner.svelte';
  import StaleRefreshNotice from '../components/StaleRefreshNotice.svelte';
  import OperationalSummary from '../components/OperationalSummary.svelte';
  import SourceHealth from '../components/SourceHealth.svelte';
  import EntityLink from '../components/EntityLink.svelte';
  import ProductEmptyState from '../components/ProductEmptyState.svelte';
  import SeverityBadge from '../components/SeverityBadge.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import PageToc from '../components/PageToc.svelte';
  import HelpTip from '../components/HelpTip.svelte';
  import PostureBars from '../components/viz/PostureBars.svelte';
  import DistributionBar from '../components/viz/DistributionBar.svelte';

  // The operational landing page. It consumes /api/fleet/overview
  // as the single contract -- it never reconstructs the summary from the raw
  // snapshot -- and answers "what needs attention / is my knowledge incomplete /
  // where do I go next" without requiring graph knowledge.
  //
  // The page asks ONE question in its title -- is anything wrong? -- and answers it in
  // one line straight under it. Then, in order: where (the error tiles and the milder
  // count chips), the fleet posture, ownership and readiness, data sources, the worst
  // open items and recent evidence. This is a summary page, so every section stays open:
  // the answer goes on top and the sections stay visible below it. Definitions sit
  // behind help buttons instead of as sentences under each heading.
  let { refreshTick = 0 } = $props();

  let overview = $state(null);
  let loading = $state(true);
  let error = $state(null);
  let lastTick = untrack(() => refreshTick); // the tick this page last loaded for

  async function load() {
    loading = true;
    error = null;
    try {
      overview = await api.fleetOverview();
    } catch (e) {
      error = e;
    } finally {
      loading = false;
    }
  }

  onMount(load);
  // Re-load when the app-wide refresh tick advances (auto-reload / manual refresh).
  $effect(() => {
    if (refreshTick !== lastTick) {
      lastTick = refreshTick;
      load();
    }
  });

  const knowledge = $derived(snapshotKnowledge(overview?.meta));
  const attentionTotal = $derived(overview?.attention?.total ?? 0);
  // Band 4. Fleet-wide source health from the backend's complete-population tally, not
  // from meta.sources -- that list is capped at MaxMetaSources and keeps the least
  // healthy when it cuts, so counting it would report a fleet of sixty healthy sources
  // and one outage as sixty-one problems.
  const sourceTally = $derived(sourceHealthTally(overview?.meta?.sourceCounts));
  // Page-level state: loading/error gate the whole view; once loaded, the overview
  // always has a summary to render (itemCount 1).
  const pageState = $derived(decideViewState({ loading, error, itemCount: overview ? 1 : 0, knowledge }));
  // A poll in flight over data already on screen, and a poll that failed over it. Both
  // were decided here and rendered nowhere: the overview was the one product page that
  // could silently freeze and still read as live.
  const revalidating = $derived(pageState.kind === 'ready' && !!pageState.revalidating);
  const refreshError = $derived(pageState.kind === 'ready' ? pageState.refreshError : null);

  // A1: distinguish a genuinely empty fleet from a healthy populated one, using the
  // authoritative product summary counts (never the raw snapshot). A fleet with zero
  // services is empty, not "all clear"; "every deployment is compliant" is claimed
  // only when there actually ARE deployments.
  const s = $derived(overview?.summary ?? {});
  const totalServices = $derived(s.services || 0);
  const totalRevisions = $derived(s.revisions || 0);
  // The backend's own target count. It used to be summed from the four link buckets,
  // which is the same number only for as long as those buckets stay exhaustive -- an
  // avoidable coupling now that the population is reported directly.
  const totalTargets = $derived(s.targets || 0);
  const isEmptyFleet = $derived(!!overview && totalServices === 0);
  // All-clear needs complete knowledge, zero attention AND a populated fleet.
  const canAllClear = $derived(!!overview && totalServices > 0 && allClearAllowed(knowledge, attentionTotal));
  // "All clear" is a claim about the attention BACKLOG. "Every operational target is
  // compliant" is a separate, universally-quantified claim about the target
  // population, and the two do not coincide: attention fires on non-compliant,
  // unknown and stale, so a fleet of Warning or Reference targets reaches zero
  // attention with every one of them not compliant. The stronger sentence is only
  // said when the distribution the same payload carries actually says so.
  const allTargetsCompliant = $derived(totalTargets > 0 && (s.compliantTargets || 0) === totalTargets);

  // The answer line. It names the error-grade entry points the backend counted -- the
  // same split the tiles below use -- and never claims "no" past what the knowledge allows.
  const errorPoints = $derived(splitEntryPoints(overview?.entryPoints).lead.filter((ep) => (ep.count || 0) > 0));
  // Plain sentences for the categories we know; any other entry point keeps the
  // backend's own label, so a new category still reaches the answer.
  const PHRASE = {
    'non-compliant': (n) => (n === 1 ? 'running target breaks its contract' : 'running targets break their contract'),
    invalid: (n) => (n === 1 ? 'revision is invalid' : 'revisions are invalid'),
  };
  const phrase = (ep) => (PHRASE[ep.category]?.(ep.count) ?? (ep.label || '').toLowerCase());
  const errorText = $derived(joinAnd(errorPoints.map((ep) => `${formatCount(ep.count)} ${phrase(ep)}`)));

  // Band 2. The fleet posture is drawn by the SAME component a service page and an
  // owner page use, so the three surfaces cannot drift in wording, ordering or colour.
  // The flat OverviewSummary counters are reshaped into the shared tally shape here
  // rather than in the component: the overview is the one surface whose aggregate
  // predates the shared shape, and translating it once at the edge beats teaching the
  // component about a second field layout.
  const posture = $derived({
    targets: totalTargets,
    compliance: {
      compliant: s.compliantTargets,
      nonCompliant: s.nonCompliantTargets,
      unknown: s.unknownTargets,
      warning: s.warningTargets,
      invalid: s.invalidTargets,
      reference: s.referenceTargets,
      notEvaluated: s.notEvaluatedTargets,
      other: s.otherComplianceTargets,
    },
    links: {
      exact: s.exactTargetLinks,
      inferred: s.inferredTargetLinks,
      ambiguous: s.ambiguousTargetLinks,
      unresolved: s.unresolvedTargetLinks,
    },
    evidence: s.evidence,
  });
  // Fleet scope: no service/owner filter, so a drill-down lands on the whole backlog
  // for that category -- which is exactly the population this chart drew.
  const attentionUrl = (category) => fleetAttentionUrl({ category });

  // Band 3. Two authored-fact distributions, each over a COMPLETE population the
  // backend tallied (OverviewSummary.ownership over every service, .readiness over
  // every contract revision) -- never over the bounded attention or evidence previews
  // further down this page. Each bucket drills into the SAME filter the backend
  // classified it by, so the list a reader lands on is exactly the slice they clicked.
  const ownership = $derived(ownershipSegments(s.ownership,
    ownershipHrefs((v) => fleetServicesUrl({ ownership: v }))));
  const readiness = $derived(readinessSegments(s.readiness, {
    passing: fleetEntityListUrl('revision', { readiness: 'passing' }),
    belowThreshold: fleetEntityListUrl('revision', { readiness: 'below-threshold' }),
    expired: fleetEntityListUrl('revision', { readiness: 'expired' }),
    notDeclared: fleetEntityListUrl('revision', { readiness: 'not-declared' }),
  }));
</script>

<div class="product-page">
  <Breadcrumbs trail={[{ label: 'Overview' }]} />
  <PageHeader title="Is anything wrong?" asOf={overview?.meta?.asOf} {revalidating} />

  {#if pageState.kind !== 'ready'}
    <ProductEmptyState state={pageState} noun="operational data" onRetry={load} />
  {:else}
  <!-- The answer, before anything else. The chain is an if/else on purpose: with the
       snapshot short of data we can neither call the fleet healthy NOR call it
       confirmed-empty, so the caveat replaces both claims rather than sitting above one. -->
  <div class="ov-answer-block">
    {#if errorPoints.length}
      <p class="page-answer t-body" data-testid="overview-answer"><strong>Yes.</strong> {errorText}.</p>
    {:else if knowledge.incomplete}
      <p class="page-answer t-body" data-testid="overview-answer"><strong>Nothing broken found</strong>, but some data is missing.</p>
    {:else if isEmptyFleet}
      <p class="page-answer t-body empty-fleet" role="status" data-testid="overview-answer"><strong>No services tracked yet.</strong> Nothing has reported a contract or a running target yet. That is not a health assessment.</p>
    {:else if canAllClear}
      <p class="page-answer t-body all-clear" role="status" data-testid="overview-answer">
        <strong>No. All clear.</strong>
        {#if allTargetsCompliant}
          Every operational target is compliant and every data source is healthy.
        {:else}
          No open attention items, and every source is healthy.
        {/if}
      </p>
    {:else}
      <p class="page-answer t-body" data-testid="overview-answer"><strong>Nothing is broken.</strong> {formatCount(attentionTotal)} {attentionTotal === 1 ? 'item is' : 'items are'} worth a look.</p>
    {/if}
    <KnowledgeBanner {knowledge} noun="overview" />
  </div>
  <div class="page-toc-layout">
    <PageToc />
    <div class="page-toc-main">
      {#if refreshError}
        <StaleRefreshNotice noun="overview" onRetry={load} />
      {/if}
    <section class="band" id="sec-now" data-toc="Where" aria-labelledby="ov-now">
      <h2 id="ov-now" class="t-section-title">Where</h2>
      <OperationalSummary summary={overview.summary} entryPoints={overview.entryPoints} {attentionTotal} />
    </section>

    <section class="band" id="sec-posture" data-toc="Operational posture" aria-labelledby="ov-posture">
      <div class="ov-head">
        <div class="ov-title"><h2 id="ov-posture" class="t-section-title">Operational posture</h2><HelpTip label="operational posture" text="Three separate questions about every running target, never rolled into one score." /></div>
        <span class="ov-tally t-body-2">{formatCount(totalTargets)} operational {totalTargets === 1 ? 'target' : 'targets'}</span>
      </div>
      <PostureBars summary={posture} {attentionUrl} />
    </section>

    <section class="band" id="sec-org" data-toc="Ownership and readiness" aria-labelledby="ov-org">
      <!-- Owners is a DIMENSION of the four primary destinations, not a fifth one, so it
           is not in the nav. This is where a reader who has just been shown a fleet-wide
           ownership gap goes to find out whose it is. -->
      <div class="ov-head">
        <div class="ov-title"><h2 id="ov-org" class="t-section-title">Ownership and readiness</h2><HelpTip label="ownership and readiness" text="Gaps in the contracts rather than failures at run time: whether ownership is declared at all, and whether anyone assesses readiness." /></div>
        <a class="ov-viewall" href={fleetOwnersUrl()}>Browse owners</a>
      </div>
      <div class="ov-org-grid">
        <DistributionBar
          title="Declared ownership"
          description="Authored on each contract revision, so a service is cleanly owned only when its revisions agree."
          scopeNote={`All ${formatCount(totalServices)} ${totalServices === 1 ? 'service' : 'services'} in the snapshot.`}
          segments={ownership}
          total={totalServices}
          emptyLabel="No services are tracked yet, so there is no ownership picture."
        />
        <DistributionBar
          title="Contract revision readiness"
          description="Declared preparedness of each revision, judged against its own threshold. This is not compliance: a passing revision can still be running on a target that violates its contract."
          scopeNote={`All ${formatCount(totalRevisions)} contract ${totalRevisions === 1 ? 'revision' : 'revisions'} in the snapshot.`}
          segments={readiness}
          total={totalRevisions}
          emptyLabel="No contract revisions are tracked yet, so there is no readiness picture."
        />
      </div>
    </section>

    <!-- Data sources are SECONDARY but not a footnote: every count above this point is
         bounded by which sources answered. It appears ONCE on this page. -->
    <section class="band" id="sec-sources" data-toc="Data sources" aria-labelledby="ov-sources">
      <div class="ov-head">
        <div class="ov-title"><h2 id="ov-sources" class="t-section-title">Data sources</h2><HelpTip label="data sources" text="Where everything above came from. A degraded source makes the counts on this page incomplete, not zero." /></div>
        <a class="ov-viewall" href={fleetSourcesUrl()}>View all data sources</a>
      </div>
      <!-- The tally is over the COMPLETE source population; the chips below it are the
           bounded, least-healthy-first list from the meta. -->
      <p class="ov-tally t-body-2">{sourceTally}</p>
      <SourceHealth sources={overview.meta?.sources || []} truncated={overview.meta?.sourcesTruncated} />
    </section>

    <section class="ov-section" id="sec-attention" data-toc="Needs attention" aria-labelledby="ov-attention">
      <div class="ov-head">
        <h2 id="ov-attention" class="t-section-title">Needs attention</h2>
        <a class="ov-viewall" href={fleetAttentionUrl()}>View all ({formatCount(attentionTotal)})</a>
      </div>
      <!-- Triage dimensions, not destinations. Filters, so deliberately count-free -- a
           chip claiming "0" would be a health assessment the overview has not made. -->
      <nav class="ov-cats" aria-label="Filter attention by category">
        {#each ATTENTION_CATEGORIES as c}
          <a class="ov-cat" href={fleetAttentionUrl({ category: c })}>{attentionCategoryLabel(c)}</a>
        {/each}
      </nav>
      {#if overview.attention.items.length === 0}
        <ProductEmptyState state={decideViewState({ loading: false, itemCount: 0, knowledge })} noun="attention items" />
      {:else}
        <ul class="attn-list pl-list">
          {#each overview.attention.items as it}
            <li class="attn-item pl-row">
              <SeverityBadge severity={it.severity} />
              <EntityLink ref={it.entity} showStatus={false} />
              <span class="attn-reason">{it.summary || it.reason || it.label}</span>
            </li>
          {/each}
        </ul>
        {#if overview.attention.truncated}
          <p class="ov-more">Showing {formatCount(overview.attention.count)} of {formatCount(attentionTotal)}. <a href={fleetAttentionUrl()}>See all</a></p>
        {/if}
      {/if}
    </section>

    <section class="ov-section" id="sec-evidence" data-toc="Recent evidence" aria-labelledby="ov-evidence">
      <h2 id="ov-evidence" class="t-section-title">Recent evidence</h2>
      {#if overview.recentEvidence.items.length}
        <ul class="evi-list pl-list">
          {#each overview.recentEvidence.items as ev}
            <li class="evi-item pl-row">
              <EntityLink ref={ev.target} showStatus={false} />
              {#if ev.at}<time class="evi-at" datetime={ev.at} title={ev.at}>{formatDateTime(ev.at)}</time>{/if}
            </li>
          {/each}
        </ul>
      {:else}
        <!-- The window is the engine's: fleet.RecentEvidenceWindow, 24 hours before the snapshot. -->
        <p class="ov-none">No evidence arrived in the 24 hours before this snapshot.</p>
      {/if}
    </section>
    </div>
  </div>
  {/if}
</div>

<style>
  .band, .ov-section { display: flex; flex-direction: column; gap: var(--sp-3); }
  /* A band is a question, so it is separated by space and a hairline rather than by a
     card: three nested boxes on a page already inside a shell is chrome, not structure. */
  .band + .band, .band + .ov-section { padding-top: var(--sp-4); border-top: 1px solid var(--c-border); }
  .ov-head { display: flex; align-items: baseline; justify-content: space-between; gap: var(--sp-3); flex-wrap: wrap; }
  .ov-head h2, .band > h2, .ov-section > h2 { margin: 0; }
  .ov-title { display: flex; align-items: center; gap: var(--sp-2); }
  .ov-answer-block { display: flex; flex-direction: column; gap: var(--sp-2); margin-bottom: var(--sp-4); }
  .ov-answer-block .page-answer { margin: 0; }
  .ov-tally { margin: 0; color: var(--c-text); }
  /* Two bars side by side where there is room; one on a phone. Same rule as the posture
     grid, so the two bands line up instead of each inventing a breakpoint. */
  .ov-org-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 320px), 1fr)); gap: var(--sp-4); }
  .ov-viewall { color: var(--c-accent); text-decoration: none; font-size: var(--text-sm); }
  /* .ov-more's "See all" is an INLINE link within a sentence, so it stays underlined to
     be distinguishable from the surrounding text without color alone (WCAG 1.4.1). */
  .ov-more a { color: var(--c-accent); text-decoration: underline; font-size: var(--text-sm); }
  .ov-viewall:hover { text-decoration: underline; }
  .ov-cats { display: flex; gap: var(--sp-2); flex-wrap: wrap; }
  .ov-cat {
    font-size: var(--text-sm); color: var(--c-text-2); text-decoration: none;
    padding: 4px 10px; border: 1px solid var(--c-border); border-radius: var(--radius-pill, var(--radius-sm));
    background: var(--c-surface); min-height: var(--touch-min); display: inline-flex; align-items: center;
  }
  .ov-cat:hover { border-color: var(--c-accent); color: var(--c-accent); }
  /* The degraded-knowledge caveat is KnowledgeBanner's, styled once there. */
  /* An all-clear is a green confirmation and an empty fleet a neutral fact. */
  .all-clear, .empty-fleet { padding: var(--sp-2) var(--sp-3); border-radius: var(--radius-md); }
  .all-clear { background: var(--c-ok-bg); border: 1px solid var(--c-ok-border); }
  .empty-fleet { background: var(--c-surface-inset); border: 1px solid var(--c-border); color: var(--c-text-2); }
  /* Shape only. The frame, the divider and the hover come from the shared .pl-row in
     styles/components.css; these rows keep their own padding because the entity link is
     a middle child, so handing it the padding would leave the badge flush to the edge. */
  .attn-item, .evi-item {
    gap: var(--sp-2); flex-wrap: wrap;
    padding: var(--sp-2) var(--sp-3);
  }
  .attn-reason { color: var(--c-text-3); font-size: var(--text-sm); }
  .evi-at { color: var(--c-text-3); font-size: var(--text-xs); margin-left: auto; white-space: nowrap; }
  .ov-none, .ov-more { color: var(--c-text-3); font-size: var(--text-sm); }
</style>
