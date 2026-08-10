<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.ts';
  import { snapshotKnowledge, decideViewState, allClearAllowed } from '../lib/knowledgeState.ts';
  import { knowledgeLabel, knowledgeTone, attentionCategoryLabel, ATTENTION_CATEGORIES } from '../lib/entityLabels.ts';
  import { fleetAttentionUrl, fleetSourcesUrl, fleetServicesUrl, fleetOwnersUrl, fleetEntityListUrl } from '../lib/router.ts';
  import { ownershipSegments, readinessSegments } from '../lib/distributions.ts';
  import { formatDate } from '../lib/dateFormat.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import OperationalSummary from '../components/OperationalSummary.svelte';
  import SourceHealth from '../components/SourceHealth.svelte';
  import EntityLink from '../components/EntityLink.svelte';
  import ProductEmptyState from '../components/ProductEmptyState.svelte';
  import SeverityBadge from '../components/SeverityBadge.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import PageToc from '../components/PageToc.svelte';
  import PostureBars from '../components/viz/PostureBars.svelte';
  import DistributionBar from '../components/viz/DistributionBar.svelte';

  // The operational landing page (requirement G). It consumes /api/fleet/overview
  // as the single contract -- it never reconstructs the summary from the raw
  // snapshot -- and answers "what needs attention / is my knowledge incomplete /
  // where do I go next" without requiring graph knowledge.
  //
  // The page is three bands, in the order the questions actually get asked:
  //
  //   1. Immediate situation   what requires action, and can I trust what I am reading
  //   2. Operational posture   how is what is RUNNING behaving
  //   3. Organization          is ownership/readiness a systemic gap rather than a bug
  //
  // Everything below them is the itemized detail of band 1. The bands exist because a
  // flat page of eleven equal sections made the reader rank the fleet's problems
  // themselves; a band says which question its contents answer, and answers it in one
  // screenful before the lists begin.
  let { refreshTick = 0 } = $props();

  let overview = $state(null);
  let loading = $state(true);
  let error = $state(null);
  let lastTick = refreshTick;

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
  // Page-level state: loading/error gate the whole view; once loaded, the overview
  // always has a summary to render (itemCount 1).
  const pageState = $derived(decideViewState({ loading, error, itemCount: overview ? 1 : 0, knowledge }));

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
      invalid: s.invalidTargets,
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
  const ownership = $derived(ownershipSegments(s.ownership, {
    conflicting: fleetServicesUrl({ ownership: 'conflicting' }),
    unowned: fleetServicesUrl({ ownership: 'unowned' }),
    consistent: fleetServicesUrl({ ownership: 'consistent' }),
  }));
  const readiness = $derived(readinessSegments(s.readiness, {
    passing: fleetEntityListUrl('revision', { readiness: 'passing' }),
    belowThreshold: fleetEntityListUrl('revision', { readiness: 'below-threshold' }),
    expired: fleetEntityListUrl('revision', { readiness: 'expired' }),
    notDeclared: fleetEntityListUrl('revision', { readiness: 'not-declared' }),
  }));
</script>

<div class="overview">
  <Breadcrumbs trail={[{ label: 'Overview' }]} />
  <PageHeader title="Operational overview" />

  {#if pageState.kind !== 'ready'}
    <ProductEmptyState state={pageState} noun="operational data" onRetry={load} />
  {:else}
  <div class="page-toc-layout">
    <PageToc />
    <div class="page-toc-main">
    <section class="band" id="sec-now" data-toc="Immediate situation" aria-labelledby="ov-now">
      <h2 id="ov-now" class="t-section-title">Immediate situation</h2>

      {#if knowledge.incomplete}
        <div class="knowledge-banner tone-{knowledgeTone(knowledge.level)}" role="status">
          <strong>{knowledgeLabel(knowledge.level)}.</strong>
          <span>
            {#if isEmptyFleet}
              Nothing is being tracked yet, and some sources are degraded — we can neither confirm there is nothing to track nor call it healthy.
            {:else}
              Some sources are degraded, so the counts below may be incomplete — this is not a clean bill of health.
            {/if}
          </span>
        </div>
      {:else if isEmptyFleet}
        <div class="empty-fleet" role="status">
          <strong>No services tracked yet.</strong>
          <span>Nothing has reported a contract or a running target yet. That is not a health assessment.</span>
        </div>
      {:else if canAllClear}
        <div class="all-clear" role="status">
          <strong>All clear.</strong>
          <span>
            {#if totalTargets > 0}
              Every operational target is compliant and every data source is healthy.
            {:else}
              No open attention items, and every source is healthy.
            {/if}
          </span>
        </div>
      {/if}

      <OperationalSummary summary={overview.summary} entryPoints={overview.entryPoints} {attentionTotal} />

      <!-- "Can I trust what I just read" is part of the immediate situation, not a
           footnote: every count on this page is bounded by which sources answered. -->
      <div class="ov-head">
        <h3 class="t-subsection-title">Where this knowledge came from</h3>
        <a class="ov-viewall" href={fleetSourcesUrl()}>View all data sources</a>
      </div>
      <SourceHealth sources={overview.meta?.sources || []} truncated={overview.meta?.sourcesTruncated} />
    </section>

    <section class="band" id="sec-posture" data-toc="Operational posture" aria-labelledby="ov-posture">
      <h2 id="ov-posture" class="t-section-title">Operational posture</h2>
      <p class="ov-sub t-body-2">
        Over all {totalTargets} operational {totalTargets === 1 ? 'target' : 'targets'} the snapshot knows about.
        Compliance, revision-match certainty and evidence freshness are three separate questions and are never rolled into one score.
      </p>
      <PostureBars summary={posture} {attentionUrl} />
      {#if totalTargets > 0}
        <p class="ov-note t-body-2">We know exactly which revision is running on {s.exactTargetLinks || 0} of {totalTargets} operational targets{(s.staleTargets || 0) > 0 ? `, and ${s.staleTargets} of them were last observed too long ago to trust` : ''}.</p>
      {/if}
    </section>

    <section class="band" id="sec-org" data-toc="Organization and contract" aria-labelledby="ov-org">
      <!-- Owners is a DIMENSION of the four primary destinations, not a fifth one, so it
           is not in the nav. This is where a reader who has just been shown a fleet-wide
           ownership gap goes to find out whose it is. -->
      <div class="ov-head">
        <h2 id="ov-org" class="t-section-title">Organization and contract</h2>
        <a class="ov-viewall" href={fleetOwnersUrl()}>Browse owners</a>
      </div>
      <p class="ov-sub t-body-2">
        Two things nobody can see from a single service page: whether ownership is declared at all, and whether anyone is assessing readiness.
        Neither is an operational failure — both are systemic, and both are counted over everything the snapshot holds.
      </p>
      <div class="ov-org-grid">
        <DistributionBar
          title="Declared ownership"
          description="Ownership is authored on each contract revision, so a service is cleanly owned only when its revisions agree. Revisions naming different owners is its own state, never folded into 'no owner'."
          scopeNote={`All ${totalServices} ${totalServices === 1 ? 'service' : 'services'} in the snapshot.`}
          segments={ownership}
          total={totalServices}
          emptyLabel="No services are tracked yet, so there is no ownership picture."
        />
        <DistributionBar
          title="Contract revision readiness"
          description="Declared preparedness of each immutable contract revision, judged against the threshold that revision set for itself. It is not compliance: a passing revision can still be running on a target observed to violate its contract."
          scopeNote={`All ${totalRevisions} contract ${totalRevisions === 1 ? 'revision' : 'revisions'} in the snapshot.`}
          segments={readiness}
          total={totalRevisions}
          emptyLabel="No contract revisions are tracked yet, so there is no readiness picture."
        />
      </div>
    </section>

    <section class="ov-section" id="sec-attention" data-toc="Needs attention" aria-labelledby="ov-attention">
      <div class="ov-head">
        <h2 id="ov-attention" class="t-section-title">Needs attention</h2>
        <a class="ov-viewall" href={fleetAttentionUrl()}>View all ({attentionTotal})</a>
      </div>
      <!-- Triage dimensions, not destinations. Readiness lives here rather than in the
           primary nav: it is declared contract preparedness, one reason a thing needs
           attention, and it shares the product's single definition of it. These are
           filters, so they are deliberately count-free -- a chip claiming "0" would be a
           health assessment the overview has not made. -->
      <nav class="ov-cats" aria-label="Filter attention by category">
        {#each ATTENTION_CATEGORIES as c}
          <a class="ov-cat" href={fleetAttentionUrl({ category: c })}>{attentionCategoryLabel(c)}</a>
        {/each}
      </nav>
      {#if overview.attention.items.length === 0}
        <ProductEmptyState state={decideViewState({ loading: false, itemCount: 0, knowledge })} noun="attention items" />
      {:else}
        <ul class="attn-list">
          {#each overview.attention.items as it}
            <li class="attn-item">
              <SeverityBadge severity={it.severity} />
              <EntityLink ref={it.entity} showStatus={false} />
              <span class="attn-reason">{it.summary || it.reason || it.label}</span>
            </li>
          {/each}
        </ul>
        {#if overview.attention.truncated}
          <p class="ov-more">Showing {overview.attention.count} of {attentionTotal}. <a href={fleetAttentionUrl()}>See all</a></p>
        {/if}
      {/if}
    </section>

    <section class="ov-section" id="sec-evidence" data-toc="Recent evidence" aria-labelledby="ov-evidence">
      <h2 id="ov-evidence" class="t-section-title">Recent evidence</h2>
      {#if overview.recentEvidence.items.length}
        <ul class="evi-list">
          {#each overview.recentEvidence.items as ev}
            <li class="evi-item">
              <EntityLink ref={ev.target} showStatus={false} />
              {#if ev.at}<span class="evi-at">{formatDate(ev.at)}</span>{/if}
            </li>
          {/each}
        </ul>
      {:else}
        <p class="ov-none">No evidence arrived recently.</p>
      {/if}
    </section>
    </div>
  </div>
  {/if}
</div>

<style>
  .overview { display: flex; flex-direction: column; gap: var(--sp-5); }
  .band, .ov-section { display: flex; flex-direction: column; gap: var(--sp-3); }
  /* A band is a question, so it is separated by space and a hairline rather than by a
     card: three nested boxes on a page already inside a shell is chrome, not structure. */
  .band + .band, .band + .ov-section { padding-top: var(--sp-4); border-top: 1px solid var(--c-border); }
  .ov-head { display: flex; align-items: baseline; justify-content: space-between; gap: var(--sp-3); flex-wrap: wrap; }
  .ov-head h2, .ov-head h3, .band > h2, .ov-section > h2 { margin: 0; }
  .ov-sub, .ov-note { margin: 0; max-width: 80ch; }
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
  .knowledge-banner, .all-clear, .empty-fleet {
    display: flex; gap: var(--sp-2); flex-wrap: wrap; align-items: baseline;
    padding: var(--sp-3); border-radius: var(--radius-md); font-size: var(--text-sm);
  }
  .knowledge-banner { background: var(--c-warn-bg); border: 1px solid var(--c-warn-border); color: var(--c-text); }
  .knowledge-banner.tone-err { background: var(--c-err-bg); border-color: color-mix(in srgb, var(--c-err) 30%, transparent); }
  .all-clear { background: var(--c-ok-bg); border: 1px solid var(--c-ok-border); color: var(--c-text); }
  /* An empty fleet is a neutral fact, never a green all-clear. */
  .empty-fleet { background: var(--c-surface-inset); border: 1px solid var(--c-border); color: var(--c-text-2); }
  .attn-list, .evi-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .attn-item, .evi-item {
    display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap;
    padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    background: var(--c-surface);
  }
  .attn-reason { color: var(--c-text-3); font-size: var(--text-sm); }
  .evi-at { color: var(--c-text-3); font-size: var(--text-xs); margin-left: auto; }
  .ov-none, .ov-more { color: var(--c-text-3); font-size: var(--text-sm); }
</style>
