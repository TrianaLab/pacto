<script>
  import { onMount, onDestroy, untrack } from 'svelte';
  import { api } from '../lib/api.ts';
  import { formatCount } from '../lib/format.ts';
  import { snapshotKnowledge, decideViewState, allClearAllowed } from '../lib/knowledgeState.ts';
  import { splitEntryPoints, businessDomainLabel, UNKNOWN_DOMAIN, NO_DOMAIN, GAP_WORDS } from '../lib/entityLabels.ts';
  import { fleetAttentionUrl, fleetSourcesUrl, fleetServicesUrl, fleetOwnersUrl, fleetEntityListUrl, fleetDomainsUrl, fleetEntityUrl, fleetGoldenPathsUrl, fleetExternalsUrl } from '../lib/router.ts';
  import { ownershipSegments, ownershipHrefs, readinessSegments, complianceSegments, statusHrefs, healthSegments, knowledgeSegments, KNOWLEDGE_GAPS } from '../lib/distributions.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { formatDateTime, formatDate } from '../lib/dateFormat.ts';
  import LazyView from '../components/LazyView.svelte';
  import { loadTrustFigure, loadKnowsExplainer } from '../lib/lazyFigures.ts';
  import { joinAnd, problemPhrase, healthCaption, knowledgeCaption } from '../lib/answers.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import KnowledgeBanner from '../components/KnowledgeBanner.svelte';
  import StaleRefreshNotice from '../components/StaleRefreshNotice.svelte';
  import OperationalSummary from '../components/OperationalSummary.svelte';
  import SourceHealth from '../components/SourceHealth.svelte';
  import EntityLink from '../components/EntityLink.svelte';
  import EvidenceList from '../components/EvidenceList.svelte';
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
  // The "How does Pacto know?" explainer opens on request: it explains, it reports nothing.
  let showKnows = $state(false);
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

  // The trust figure's aside: what the snapshot says about the services, one bar whose
  // legend items list each bucket (L3: the bar is the one carrier). The overview
  // counts targets, so the service tally is the entities aggregate, a second
  // question with its own request; a failure costs only the bar.
  const svcLoader = createProductLoader(() => api.fleetEntities({ kinds: ['service'], limit: 1 }));
  $effect(() => { svcLoader.sync(`svc@@${refreshTick}`, 'svc'); });
  onDestroy(() => svcLoader.destroy());
  const svcAgg = $derived(svcLoader.data?.aggregate ?? null);
  const svcCompliance = $derived(complianceSegments(svcAgg?.serviceCompliance, statusHrefs((status) => fleetServicesUrl({ status }))));

  const knowledge = $derived(snapshotKnowledge(overview?.meta));
  const attentionTotal = $derived(overview?.attention?.total ?? 0);
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
  // Golden paths are counted apart: they run nowhere and stay out of every service
  // count on this page.
  const goldenPaths = $derived(s.goldenPaths || 0);
  const invalidGoldenPaths = $derived(s.invalidGoldenPaths || 0);
  // The external systems the services declare, counted apart from the declared services
  // (509-S1 inserts the inferred ones between them); those only hidden services declare
  // are counted, never named.
  const externals = $derived(s.externals || 0);
  const hiddenExternals = $derived(s.hiddenExternals || 0);
  // The workloads no contract describes, counted apart too; the unverified ones sit
  // where Pacto could not read the Pacto targets, so "no contract" is not certain there.
  const inferred = $derived(s.inferredServices || 0);
  const inferredUnverified = $derived(s.inferredUnverified || 0);
  // A cluster that only runs inferred services is not an empty fleet.
  const isEmptyFleet = $derived(!!overview && totalServices === 0 && inferred === 0 && goldenPaths === 0);
  // All-clear needs complete knowledge, zero attention AND a populated fleet.
  // A failing or unknown health check withholds it too, whatever the attention count.
  const health = $derived(s.operationalHealth ?? {});
  const canAllClear = $derived(!!overview && totalServices > 0 && allClearAllowed(knowledge, attentionTotal, health));
  const healthUnknown = $derived(health.unknown || 0);
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
  const errorText = $derived(joinAnd(errorPoints.map((ep) => (problemPhrase(ep.category, ep.count) || `${formatCount(ep.count)} ${(ep.label || '').toLowerCase()}`))));

  // Where the broken targets run: the scope with the most of them, for the answer line,
  // and every scope the backend tallied (worst first, bounded) for the Where section.
  const scopes = $derived(s.byScope ?? []);
  const worstScope = $derived(scopes[0]?.nonCompliant > 0 ? scopes[0] : null);
  const whereText = $derived(!worstScope ? ''
    : (s.scopes ?? scopes.length) === 1 ? `All the targets that break their contract run in ${worstScope.scope || 'one unnamed scope'}.`
    : `${formatCount(worstScope.nonCompliant)} of the targets that break their contract run in ${worstScope.scope || 'an unnamed scope'}.`);
  // Every service by business domain, the most broken first, ending with the Domain
  // unknown row (domainUnknown) and the No domain row (domain "") when they apply.
  const domains = $derived(s.byDomain ?? []);
  const namedShown = $derived(domains.filter((d) => d.domain).length);
  // The overview shows the worst few open items; the rest is one link away.
  const topAttention = $derived((overview?.attention?.items ?? []).slice(0, 3));

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
      staleCompliant: s.staleCompliantTargets,
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
  const attentionUrl = (category, extra = {}) => fleetAttentionUrl({ category, ...extra });

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

  // Operational health: each target's own health check, over every target. Failing is
  // an observed problem with its own triage list; unknown is a gap in what Pacto sees.
  const healthSegs = $derived(healthSegments(health, { unavailable: fleetAttentionUrl({ category: 'health' }) }));
  // Knowledge: what the contracts leave out, over every service that is no golden path.
  // `knowledge` above is the snapshot's source completeness, a different question.
  const contractKnowledge = $derived(s.knowledge ?? {});
  const knowledgeSegs = $derived(knowledgeSegments(contractKnowledge, { unknown: fleetServicesUrl({ gap: 'unknown' }) }));
  // One row per gap kind a service has; a kind no service has is not listed.
  const gapRows = $derived(KNOWLEDGE_GAPS.map((g) => ({ ...g, label: GAP_WORDS[g.value], count: contractKnowledge.gaps?.[g.field] || 0 })).filter((g) => g.count > 0));
  const coverage = $derived(s.coverage ?? {});
  // A domain row's filter value: its slug, or the No domain and Domain unknown groups.
  const domainFilter = (dt) => dt.domain || (dt.domainUnknown ? UNKNOWN_DOMAIN : NO_DOMAIN);
</script>

<div class="product-page">
  <!-- A count that opens the list it counts, when there is anything in it. -->
  {#snippet cnt(v, href)}
    {#if v > 0}<a href={href}>{formatCount(v)}</a>{:else}{formatCount(v || 0)}{/if}
  {/snippet}

  <Breadcrumbs trail={[{ label: 'Overview' }]} />
  <PageHeader glyph="snapshot" title="Is anything wrong?" asOf={overview?.meta?.asOf} {revalidating} backdrop={pageState.kind === 'ready'} />

  {#if pageState.kind !== 'ready'}
    <ProductEmptyState state={pageState} noun="operational data" onRetry={load} />
  {:else}
  <!-- The answer, before anything else. The chain is an if/else on purpose: with the
       snapshot short of data we can neither call the fleet healthy NOR call it
       confirmed-empty, so the caveat replaces both claims rather than sitting above one. -->
  <div class="ov-answer-block" id="ov-answer">
    {#if errorPoints.length}
      <p class="page-answer t-body" data-testid="overview-answer"><strong>Yes.</strong> {errorText}.{#if whereText}{' '}{whereText}{/if}</p>
    {:else if totalServices === 0 && inferred > 0}
      <!-- Only inferred services: no contract is checked, so no verdict either way. -->
      <p class="page-answer t-body" role="status" data-testid="overview-answer"><strong>No contracts yet.</strong> Pacto sees <a href={fleetServicesUrl({ declaration: 'inferred' })}>{formatCount(inferred)} {inferred === 1 ? 'workload' : 'workloads'}</a> as inferred services; a contract lets it check them.</p>
    {:else if knowledge.incomplete}
      <!-- The banner right under it says what is missing, so the answer does not say it twice. -->
      <p class="page-answer t-body" data-testid="overview-answer"><strong>Nothing broken found.</strong></p>
    {:else if isEmptyFleet}
      <p class="page-answer t-body empty-fleet" role="status" data-testid="overview-answer"><strong>No services tracked yet.</strong> Nothing has reported a contract or a running target yet. Health appears here once a source reports.</p>
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
      {#if attentionTotal === 0 && healthUnknown > 0}
        <p class="page-answer t-body" data-testid="overview-answer"><strong>Nothing broken found.</strong> Pacto cannot tell the health of {formatCount(healthUnknown)} operational {healthUnknown === 1 ? 'target' : 'targets'}.</p>
      {:else}
        <p class="page-answer t-body" data-testid="overview-answer"><strong>Nothing is broken.</strong> {formatCount(attentionTotal)} {attentionTotal === 1 ? 'item is' : 'items are'} worth a look.</p>
      {/if}
    {/if}
    <KnowledgeBanner {knowledge} noun="overview" sources={overview.meta?.sources || []} tone={knowledge.level === 'unavailable' ? 'warn' : ''} />
  </div>
  <!-- How sure the answer above is: every count on this page is bounded by which
       sources answered. The figure counts sources by state; Data sources below names them. -->
  <LazyView load={loadTrustFigure} mode="overview" counts={overview.meta?.sourceCounts} meta={overview.meta} asOf={overview.meta?.asOf ? formatDate(overview.meta.asOf) : ''}
    stateHref={(st) => fleetSourcesUrl({ sourceHealth: st })}>
    {#snippet aside()}
      {#if svcAgg && (svcAgg.services || !inferred)}
        <!-- The bar's in-segment counts repeat its legend, whose items are the links. -->
        <div data-nofilter><DistributionBar title="What the snapshot says about {formatCount(svcAgg.services ?? 0)} {svcAgg.services === 1 ? 'service' : 'services'}" level={2}
          segments={svcCompliance} total={svcAgg.services ?? 0} emptyLabel="No declared services yet." /></div>
      {/if}
      <dl class="facts">
        {#if externals || hiddenExternals || inferred}
          <!-- The parts add up to the headline: declared + inferred + external. -->
          <dt>Services</dt><dd data-testid="overview-services-line">{formatCount(totalServices + inferred + externals)}: <a href={fleetServicesUrl({ declaration: 'declared' })}>{formatCount(totalServices)} declared</a>{#if inferred}, <a href={fleetServicesUrl({ declaration: 'inferred' })} data-testid="overview-inferred">{formatCount(inferred)} inferred</a>{#if inferredUnverified}{' '}<span class="t-meta" title="Pacto could not read the Pacto targets where these run, so a contract may describe them">({formatCount(inferredUnverified)} unverified)</span>{/if}{/if}{#if externals || hiddenExternals}, <a href={fleetExternalsUrl()} data-testid="overview-externals">{formatCount(externals)} external</a>{#if hiddenExternals}{' '}<span class="t-meta" title="External systems only services you cannot see declare">({formatCount(hiddenExternals)} hidden)</span>{/if}{/if}</dd>
        {:else}
          <dt>Services</dt><dd><a href={fleetServicesUrl()}>{formatCount(totalServices)}</a></dd>
        {/if}
        <dt>Revisions</dt><dd><a href={fleetEntityListUrl('revision')}>{formatCount(totalRevisions)}</a></dd>
        <dt>Targets</dt><dd><a href={fleetEntityListUrl('target')}>{formatCount(totalTargets)}</a></dd>
      </dl>
    {/snippet}
  </LazyView>
  <div class="ov-knows">
    <button type="button" class="btn btn-sm" aria-expanded={showKnows} aria-controls="ov-knows" onclick={() => (showKnows = !showKnows)} data-testid="knows-toggle">{showKnows ? 'Hide' : 'Show'} how Pacto knows</button>
  </div>
  <div id="ov-knows">{#if showKnows}<LazyView load={loadKnowsExplainer} />{/if}</div>
  <div class="page-toc-layout">
    <PageToc />
    <div class="page-toc-main">
      {#if refreshError}
        <StaleRefreshNotice noun="overview" onRetry={load} />
      {/if}
    <section class="band" id="sec-now" data-toc="Where" aria-labelledby="ov-now">
      <h2 id="ov-now" class="t-section-title">Where</h2>
      <OperationalSummary summary={overview.summary} entryPoints={overview.entryPoints} {attentionTotal} />
      {#if scopes.length > 1 || (scopes.length === 1 && scopes[0].scope)}
        <div class="data-scroll">
          <table class="data-table ov-scopes" data-testid="overview-by-scope">
            <caption class="visually-hidden">Targets by scope, the most broken first</caption>
            <colgroup><col style="width: 40%" /><col /><col /><col /><col /></colgroup>
            <thead><tr><th scope="col">Scope</th><th scope="col">Targets</th><th scope="col">Not compliant</th><th scope="col">Unknown</th><th scope="col">Stale</th></tr></thead>
            <tbody>
              {#each scopes as sc (sc.scope)}
                <tr>
                  <th scope="row"><a class="cell-clip" href={fleetEntityListUrl('target', { scope: sc.scope })} title={sc.scope || 'No scope'}>{sc.scope || 'No scope'}</a></th>
                  <td data-label="Targets">{formatCount(sc.targets)}</td>
                  <td data-label="Not compliant" class:ov-bad={sc.nonCompliant > 0}>{formatCount(sc.nonCompliant)}</td>
                  <td data-label="Unknown">{formatCount(sc.unknown)}</td>
                  <td data-label="Stale">{formatCount(sc.stale)}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
        {#if (s.scopes ?? 0) > scopes.length}<p class="ov-more">The {formatCount(scopes.length)} with the most broken targets of {formatCount(s.scopes)} scopes. <a href={fleetEntityListUrl('target')}>See every target</a></p>{/if}
      {/if}
    </section>

    {#if domains.length}
      <section class="band" id="sec-domains" data-toc="Domains" aria-labelledby="ov-domains">
        <div class="ov-head">
          <div class="ov-title"><h2 id="ov-domains" class="t-section-title">Domains</h2><HelpTip label="domains" text="The business domain each service declares in its contract. Services that declare none are under No domain, and those whose domain Pacto cannot tell under Domain unknown." /></div>
          <a class="ov-viewall" href={fleetDomainsUrl()}>See every domain</a>
        </div>
        <div class="data-scroll cq-cards">
          <table class="data-table ov-scopes ov-domains" data-testid="overview-by-domain">
            <caption class="visually-hidden">Services by domain, the most broken first</caption>
            <!-- Eight columns, so the table form fits the capped content column on every desktop and
                 turns into cards only below 56rem (components.css, dtable-wide). Runs nowhere, gaps and
                 owner unknown per domain live on each domain's page. -->
            <colgroup><col style="width: max(8rem, 22%)" /><col /><col /><col /><col /><col /><col /><col /></colgroup>
            <thead>
              <tr><th scope="col" rowspan="2">Domain</th><th scope="col" rowspan="2">Services</th><th scope="colgroup" colspan="5">Targets</th><th scope="col" rowspan="2">No owner</th></tr>
              <tr><th scope="col">Targets</th><th scope="col">Not compliant</th><th scope="col">Failing</th><th scope="col">Unknown</th><th scope="col">Stale</th></tr>
            </thead>
            <tbody>
              {#each domains as dt (dt.domainUnknown ? UNKNOWN_DOMAIN : dt.domain)}
                {@const bd = domainFilter(dt)}
                <tr class:ov-none-row={!dt.domain}>
                  <th scope="row">
                    {#if dt.domain}
                      <a class="cell-clip" href={fleetEntityUrl('domain', dt.domain)} title={dt.domain}><bdi>{dt.domain}</bdi></a>
                    {:else if dt.domainUnknown}
                      <a class="cell-clip ov-none" href={fleetServicesUrl({ businessDomain: UNKNOWN_DOMAIN })} title="Services whose domain Pacto cannot tell">{businessDomainLabel(UNKNOWN_DOMAIN)}</a>
                    {:else}
                      <a class="cell-clip ov-none" href={fleetServicesUrl({ businessDomain: NO_DOMAIN })} title="Services with no declared domain">No domain</a>
                    {/if}
                  </th>
                  <td data-label="Services">{@render cnt(dt.services, fleetServicesUrl({ businessDomain: bd }))}</td>
                  <td data-label="Targets: all">{formatCount(dt.targets)}</td>
                  <td data-label="Targets: not compliant" class:ov-bad={dt.nonCompliant > 0}>{@render cnt(dt.nonCompliant, fleetAttentionUrl({ category: 'non-compliant', businessDomain: bd }))}</td>
                  <td data-label="Targets: failing" class:ov-warn={dt.failingHealth > 0}>{@render cnt(dt.failingHealth, fleetAttentionUrl({ category: 'health', businessDomain: bd }))}</td>
                  <td data-label="Targets: unknown">{@render cnt(dt.unknown, fleetAttentionUrl({ category: 'unknown', businessDomain: bd }))}</td>
                  <td data-label="Targets: stale">{@render cnt(dt.stale, fleetAttentionUrl({ category: 'stale', businessDomain: bd }))}</td>
                  <td data-label="Services: no owner">{@render cnt(dt.unowned, fleetServicesUrl({ ownership: 'unowned', businessDomain: bd }))}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
        {#if (s.domains ?? 0) > namedShown}<p class="ov-more">The {formatCount(namedShown)} with the most broken targets of {formatCount(s.domains)} domains. <a href={fleetDomainsUrl()}>See every domain</a></p>{/if}
      </section>
    {/if}

    <section class="band" id="sec-posture" data-toc="Operational posture" aria-labelledby="ov-posture">
      <div class="ov-head">
        <div class="ov-title"><h2 id="ov-posture" class="t-section-title">Operational posture</h2><HelpTip label="operational posture" text="Three separate questions about every running target, each answered on its own." /></div>
        <span class="ov-tally t-body-2">{formatCount(totalTargets)} operational {totalTargets === 1 ? 'target' : 'targets'}</span>
      </div>
      <PostureBars summary={posture} {attentionUrl} />
    </section>

    <section class="band" id="sec-health" data-toc="Operational health" aria-labelledby="ov-health">
      <div class="ov-head">
        <div class="ov-title"><h2 id="ov-health" class="t-section-title">Operational health</h2><HelpTip label="operational health" text="A target's own health check, answered on its own. Failing is an observed problem; unknown means Pacto cannot tell. Neither changes compliance." /></div>
      </div>
      <DistributionBar
        title="Health checks"
        scopeNote={`All ${formatCount(totalTargets)} operational ${totalTargets === 1 ? 'target' : 'targets'}.`}
        segments={healthSegs}
        total={totalTargets}
        emptyLabel="Nothing running has been observed yet, so there is no health picture."
      />
      {#if healthCaption(health)}<p class="ov-caption t-body-2" data-testid="health-caption">{healthCaption(health)}</p>{/if}
    </section>

    <section class="band" id="sec-org" data-toc="Ownership and readiness" aria-labelledby="ov-org">
      <!-- Owners is a DIMENSION of the four primary destinations, not a fifth one, so it
           is not in the nav. This is where a reader who has just been shown a fleet-wide
           ownership gap goes to find out whose it is. -->
      <div class="ov-head">
        <div class="ov-title"><h2 id="ov-org" class="t-section-title">Ownership and readiness</h2><HelpTip label="ownership and readiness" text="Gaps in the contracts: whether ownership is declared at all and whether anyone assesses readiness. Failures at run time appear under Operational posture." /></div>
        <a class="ov-viewall" href={fleetOwnersUrl()}>Browse owners</a>
      </div>
      {#if goldenPaths > 0}
        <a class="ov-gp" href={fleetGoldenPathsUrl(invalidGoldenPaths > 0 ? { sort: 'health' } : {})} data-testid="overview-golden-paths">
          <span class="ov-gp-count t-metric">{formatCount(goldenPaths)}</span>
          <span class="ov-gp-label">Golden {goldenPaths === 1 ? 'path' : 'paths'}</span><span class="ov-gp-sub ov-gp-note">counted apart from services</span>
          {#if invalidGoldenPaths > 0}<span class="ov-gp-sub">{formatCount(invalidGoldenPaths)} with an invalid contract</span>{/if}
        </a>
      {/if}
      <div class="ov-org-grid">
        <DistributionBar
          title="Declared ownership"
          description="Authored on each contract revision, so a service is cleanly owned only when its revisions agree."
          scopeNote={`All ${formatCount(totalServices)} ${totalServices === 1 ? 'service' : 'services'} in the snapshot.`}
          segments={ownership}
          total={totalServices}
          emptyLabel="No declared services yet, so there is no ownership picture."
        />
        <DistributionBar
          title="Contract revision readiness"
          description="Declared preparedness of each revision, judged against its own threshold. Readiness is separate from compliance: a passing revision can still run on a target that violates its contract."
          scopeNote={`All ${formatCount(totalRevisions)} contract ${totalRevisions === 1 ? 'revision' : 'revisions'} in the snapshot.`}
          segments={readiness}
          total={totalRevisions}
          emptyLabel="No contract revisions are tracked yet, so there is no readiness picture."
        />
      </div>
    </section>

    <section class="band" id="sec-knowledge" data-toc="Knowledge" aria-labelledby="ov-knowledge">
      <div class="ov-head">
        <div class="ov-title"><h2 id="ov-knowledge" class="t-section-title">Knowledge</h2><HelpTip label="knowledge" text="What the contracts leave out: a summary, a domain, an interface document, a readiness assessment or a place to run. Unknown means Pacto could not read enough to tell." /></div>
      </div>
      <DistributionBar
        title="How much the contracts say"
        scopeNote={`All ${formatCount(contractKnowledge.services ?? totalServices)} ${(contractKnowledge.services ?? totalServices) === 1 ? 'service' : 'services'}, golden paths apart.`}
        segments={knowledgeSegs}
        total={contractKnowledge.services ?? totalServices}
        emptyLabel="No declared services yet, so there is no knowledge picture."
      />
      {#if knowledgeCaption(contractKnowledge)}<p class="ov-caption t-body-2" data-testid="knowledge-caption">{knowledgeCaption(contractKnowledge)}</p>{/if}
      {#if gapRows.length}
        <ul class="ov-gaps" data-testid="knowledge-gaps" aria-label="Services per knowledge gap">
          {#each gapRows as g (g.value)}
            <li><a href={fleetServicesUrl({ gap: g.value })}><span class="ov-gap-label">{g.label}</span><span class="ov-gap-count" data-nofilter>{formatCount(g.count)}</span></a></li>
          {/each}
        </ul>
      {/if}
      {#if coverage.reporting || coverage.notReporting}
        <p class="ov-caption t-body-2" data-testid="coverage-sentence">
          {formatCount(coverage.evaluated || 0)} of {formatCount(coverage.required || 0)} required assertions evaluated on {formatCount(coverage.reporting || 0)} {coverage.reporting === 1 ? 'target' : 'targets'}{#if coverage.notReporting}; {formatCount(coverage.notReporting)} {coverage.notReporting === 1 ? 'reports' : 'report'} no coverage{/if}.
        </p>
      {/if}
    </section>

    <!-- Data sources are SECONDARY but not a footnote: every count above this point is
         bounded by which sources answered. It appears ONCE on this page. -->
    <section class="band" id="sec-sources" data-toc="Data sources" aria-labelledby="ov-sources">
      <div class="ov-head">
        <div class="ov-title"><h2 id="ov-sources" class="t-section-title">Data sources</h2><HelpTip label="data sources" text="Where everything above came from. A degraded source makes the counts on this page incomplete." /></div>
        <a class="ov-viewall" href={fleetSourcesUrl()}>View all data sources</a>
      </div>
      <!-- The trust figure at the top of the page counts the sources by state (one
           carrier per fact, L3); this section names them, least healthy first. -->
      <SourceHealth sources={overview.meta?.sources || []} truncated={overview.meta?.sourcesTruncated} limitations={overview.meta?.limitations || []} />
    </section>

    <section class="ov-section" id="sec-attention" data-toc="Needs attention" aria-labelledby="ov-attention">
      <div class="ov-head">
        <h2 id="ov-attention" class="t-section-title">Needs attention</h2>
        <a class="ov-viewall" href={fleetAttentionUrl()}>View all ({formatCount(attentionTotal)})</a>
      </div>
      {#if overview.attention.items.length === 0}
        <ProductEmptyState state={decideViewState({ loading: false, itemCount: 0, knowledge })} noun="attention items" />
      {:else}
        <ul class="attn-list pl-list">
          {#each topAttention as it}
            <li class="attn-item pl-row">
              <SeverityBadge severity={it.severity} />
              <EntityLink ref={it.entity} showStatus={false} />
              <span class="attn-reason">{it.summary || it.reason || it.label}</span>
              {#if it.caller}<span class="attn-caller t-meta" data-testid="attention-caller">caller <EntityLink ref={it.caller} showStatus={false} showKind={false} /></span>{/if}
            </li>
          {/each}
        </ul>
        {#if attentionTotal > topAttention.length}
          <p class="ov-more">The worst {formatCount(topAttention.length)} of {formatCount(attentionTotal)}. <a href={fleetAttentionUrl()}>See all</a></p>
        {/if}
      {/if}
    </section>

    <section class="ov-section" id="sec-evidence" data-toc="Recent evidence" aria-labelledby="ov-evidence">
      <h2 id="ov-evidence" class="t-section-title">Recent evidence</h2>
      {#if overview.recentEvidence.items.length}
        <EvidenceList items={overview.recentEvidence.items} when={formatDateTime} />
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
  .ov-knows { display: flex; justify-content: flex-end; }
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
  .ov-scopes td { font-variant-numeric: tabular-nums; }
  .ov-scopes .ov-bad { color: var(--c-err); font-weight: 600; }
  .ov-scopes .ov-warn { font-weight: 600; }
  /* Two header rows: the whole head sticks, so the unit row never slides under the group
     row. Only while it is a table: as cards (dtable-wide, components.css) the head is hidden. */
  @container dtable-wide (width > 56rem) {
    .ov-domains thead { position: sticky; top: var(--navbar-h); z-index: 2; }
    .ov-domains thead th { position: static; }
  }
  .ov-domains thead th[scope='colgroup'] { text-align: center; }
  .ov-caption { margin: 0; color: var(--c-text-2); }
  .ov-gaps { list-style: none; margin: 0; padding: 0; display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 18rem), 1fr)); gap: var(--sp-1) var(--sp-4); }
  .ov-gaps a { display: flex; justify-content: space-between; align-items: baseline; gap: var(--sp-2); padding: var(--sp-2) var(--sp-3); border-radius: var(--radius-sm); color: var(--c-text); text-decoration: none; border: 1px solid var(--c-border); }
  .ov-gaps a:hover, .ov-gaps a:focus-visible { border-color: var(--c-accent); }
  .ov-gap-count { font-variant-numeric: tabular-nums; font-weight: 600; }
  .ov-scopes a { color: var(--c-accent); }
  /* No domain is a neutral fact: muted, never the accent or an ok tone. */
  .ov-scopes a.ov-none { color: var(--c-text-2); font-style: italic; }
  /* The degraded-knowledge caveat is KnowledgeBanner's, styled once there. */
  /* An all-clear is a green confirmation and an empty fleet a neutral fact. */
  .all-clear, .empty-fleet { padding: var(--sp-2) var(--sp-3); border-radius: var(--radius-md); }
  .all-clear { background: var(--c-ok-bg); border: 1px solid var(--c-ok-border); }
  .empty-fleet { background: var(--c-surface-inset); border: 1px solid var(--c-border); color: var(--c-text-2); }
  /* Shape only. The frame, the divider and the hover come from the shared .pl-row in
     styles/components.css; these rows keep their own padding because the entity link is
     a middle child, so handing it the padding would leave the badge flush to the edge. */
  .attn-item {
    gap: var(--sp-2); flex-wrap: wrap;
    padding: var(--sp-2) var(--sp-3);
  }
  .attn-reason { color: var(--c-text-3); font-size: var(--text-sm); }
  .ov-none, .ov-more { color: var(--c-text-3); font-size: var(--text-sm); }
  .ov-gp {
    display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--sp-1) var(--sp-2); align-self: flex-start;
    padding: var(--sp-2) var(--sp-3); border-radius: var(--radius-md); text-decoration: none; color: var(--c-text);
    background: var(--c-surface); border: 1px solid var(--c-border); border-left: 3px solid var(--c-info);
  }
  .ov-gp:hover, .ov-gp:focus-visible { border-color: var(--c-accent); }
  .ov-gp-label { font-size: var(--text-sm); color: var(--c-text-2); }
  .ov-gp-sub { font-size: var(--text-xs); color: var(--c-err); }
  .ov-gp-sub.ov-gp-note { color: var(--c-text-3); }
</style>
