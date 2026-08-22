<script>
  import { fleetGraphFocusUrl, fleetEntityListUrl, fleetAttentionUrl } from '../../lib/router.ts';
  import { abbreviateDigests } from '../../lib/format.ts';
  import { formatDate } from '../../lib/dateFormat.ts';
  import EntityLink from '../../components/EntityLink.svelte';
  import OwnershipFact from '../../components/OwnershipFact.svelte';
  import PreviewSection from '../../components/PreviewSection.svelte';
  import EntityRefList from '../../components/EntityRefList.svelte';
  import RelationshipList from '../../components/RelationshipList.svelte';
  import FindingList from '../../components/FindingList.svelte';
  import EvidenceList from '../../components/EvidenceList.svelte';
  import LimitationsList from '../../components/LimitationsList.svelte';
  import PostureBars from '../../components/viz/PostureBars.svelte';

  // The principal operational service page. It renders ONLY the
  // product entity-detail payload (never the snapshot): owner + ownership conflicts,
  // bounded revisions / deployments / expected-dependencies / dependents previews, the
  // observed/differences relationship summary, findings attributed to exact entities,
  // recent evidence, and limitations. Every related entity uses EntityLink; every
  // preview is truncation-honest via PreviewSection.
  //
  // The operational summary below is drawn from `summary`, the backend aggregate over
  // the service's COMPLETE target / revision / edge populations. It is deliberately not
  // computed from the previews beside it: those are capped at 200 items, so counting
  // them would quietly turn "the first 200 targets" into "this service".
  let { detail } = $props();
  const d = $derived(detail.service ?? {});
  const key = $derived(detail.entity?.key ?? '');
  // The graph focus is a meaningful continuation for the neighborhood previews
  // (dependencies/dependents/differences), whose exact contents live in the graph.
  const graphHref = $derived(fleetGraphFocusUrl('service', key));
  // A capped preview must have somewhere complete to send the user: the scoped
  // inventory lists page the SAME bounded Entities endpoint by canonical ServiceKey,
  // so "5 of 47" is a real answer away rather than a dead end.
  const allRevisionsHref = $derived(fleetEntityListUrl('revision', { service: key }));
  // "All revisions" is the exhaustive variant of a list that is usually already on the
  // page, so it opens on request -- unless nothing is running this service, in which
  // case it is the ONLY place its revisions appear and collapsing it leaves the page
  // saying nothing (the default has to be sensible, not uniform).
  const noneInUse = $derived((d.activeRevisions?.count ?? 0) === 0);
  const allTargetsHref = $derived(fleetEntityListUrl('target', { service: key }));

  const sum = $derived(d.summary ?? {});
  const targets = $derived(sum.targets ?? 0);
  // Closed-state gist for the evidence disclosure: a reader deciding
  // whether to open "Recent evidence" wants to know how recently we looked, which is the
  // one fact the count cannot carry.
  const lastSeen = $derived(d.evidence?.items?.find((e) => e.at)?.at ?? '');
  // Drill-downs from the posture bars stay scoped to THIS service, by canonical key.
  // Sending a service page's "3 non-compliant" to the fleet-wide backlog would answer
  // a question the user did not ask and lose the three rows they clicked for.
  const attentionUrl = $derived((category) => fleetAttentionUrl({ service: key, category }));
  // Drift rows are only worth a line each when they are non-zero; a service with no
  // declared dependencies should not be told about four kinds of zero.
  const drift = $derived([
    { k: 'Declared dependencies', v: sum.declaredDependencies ?? 0, tone: '' },
    { k: 'Declared and observed', v: sum.reconciledMatched ?? 0, tone: 'ok' },
    { k: 'Declared, not observed', v: sum.declaredNotObserved ?? 0, tone: 'warn' },
    { k: 'Observed, not declared', v: sum.observedNotDeclared ?? 0, tone: 'warn' },
    { k: 'Unresolved declarations', v: sum.unresolvedDeclared ?? 0, tone: 'warn' },
  ].filter((x) => x.v > 0));
</script>

<div class="svc-entity">
  <!-- The domain used to be repeated here as a fact. It is the same string the page
       header already prints in its eyebrow (both come from Service.Domain), so showing
       it twice cost a line and told the reader nothing new (reduce
       text, not information). -->
  <section class="se-facts">
    {#if d.ownership}
      <div class="se-fact">
        <span class="se-k">Owner</span>
        <OwnershipFact ownership={d.ownership} />
      </div>
    {/if}
  </section>

  {#if d.ownership?.conflicts?.count}
    <!-- Each conflict carries a canonical revision key, so joining them into one prose
         sentence buried the owner names -- the actual point -- inside 71 characters of
         hex apiece. One conflict per line, digests abbreviated, exact key in the
         tooltip: the same facts, read as a list of revisions instead of a paragraph. -->
    <div class="se-conflicts" role="status">
      <div class="se-conflicts-head">
        <strong>Ownership conflict.</strong>
        <span>These revisions declare an owner other than {d.ownership.owner || 'the service owner'}:</span>
      </div>
      <ul class="se-conflict-list">
        {#each d.ownership.conflicts.items as c}<li title={c}>{abbreviateDigests(c)}</li>{/each}
        {#if d.ownership.conflicts.truncated}<li class="se-conflict-more">+{d.ownership.conflicts.total - d.ownership.conflicts.count} more</li>{/if}
      </ul>
    </div>
  {/if}

  <!-- ── operational summary ─────────────────────────────────────────────────
       Three orthogonal questions a service owner opens this page to answer, in the
       order they are usually asked: does the running system obey the contract, do we
       know WHICH contract each instance is running, and how recently did we look.
       Keeping them as three bars rather than one health score is the point: a target
       can be compliant against a revision we only guessed at, and a fleet of "unknown"
       is not a fleet of failures. -->
  <section class="se-summary" id="sec-operational-summary" data-toc="Operational summary" aria-labelledby="se-summary-h">
    <h2 id="se-summary-h" class="t-section-title">Operational summary</h2>
    <p class="se-lead t-body-2">
      {targets} operational {targets === 1 ? 'target' : 'targets'} ·
      {sum.revisionsInUse ?? 0} of {sum.revisions ?? 0} known {(sum.revisions ?? 0) === 1 ? 'revision' : 'revisions'} in use{(sum.invalidRevisions ?? 0) > 0 ? ` · ${sum.invalidRevisions} invalid` : ''}
    </p>

    <PostureBars
      summary={sum}
      {attentionUrl}
      empty="Nothing running has been observed for this service, so there is no compliance, identity or freshness picture yet."
    />

    {#if drift.length > 0}
      <h3 class="t-subsection-title">Dependency drift</h3>
      <ul class="se-drift">
        {#each drift as x (x.k)}
          <li class={x.tone ? `tone-${x.tone}` : ''}><b>{x.v}</b> {x.k}</li>
        {/each}
      </ul>
    {/if}
  </section>

  <!-- Findings are confirmed problems attributed to exact entities. They used to sit
       below five inventory previews, so on a service with revisions the first thing a
       reader saw was a list of names and the last was the reason they were called.
       Requirement 13: active failures are never collapsed and never below the
       inventory. -->
  {#if (d.findings?.count ?? 0) > 0}
    <PreviewSection title="Findings" tone="err" total={d.findings?.total ?? 0} count={d.findings?.count ?? 0} truncated={d.findings?.truncated}>
      <FindingList items={d.findings?.items ?? []} />
    </PreviewSection>
  {/if}

  {#if (d.relationships?.count ?? 0) > 0}
    <PreviewSection title="Observed traffic and differences" total={d.relationships?.total ?? null} count={d.relationships?.count ?? 0} truncated={d.relationships?.truncated} viewAllHref={graphHref} viewAllLabel="Open differences view" help="What Pacto has actually seen this service talk to, and where that disagrees with what its contract declares.">
      <RelationshipList items={d.relationships?.items ?? []} selfKey={key} />
    </PreviewSection>
  {/if}

  <!-- Inventory: WHAT this service is made of. It answers a navigation question, not an
       operational one, so it comes after the summary and the findings.
       The two lists a reader almost always wants -- what is running, and which revisions
       it is running -- stay open; the exhaustive variants of the same lists open on
       request. -->
  <div class="se-grid">
    <PreviewSection title="Revisions in use" total={d.activeRevisions?.total ?? 0} count={d.activeRevisions?.count ?? 0} truncated={d.activeRevisions?.truncated} viewAllHref={allRevisionsHref} viewAllLabel="View all revisions" empty="No revision is currently matched to a running target." help="The revisions at least one running target is matched to. Newest first.">
      <EntityRefList items={d.activeRevisions?.items ?? []} showStatus={false} />
    </PreviewSection>

    <PreviewSection title="Operational targets" total={d.deployments?.total ?? 0} count={d.deployments?.count ?? 0} truncated={d.deployments?.truncated} viewAllHref={allTargetsHref} viewAllLabel="View all targets" empty="No running target observed.">
      <EntityRefList items={d.deployments?.items ?? []} />
    </PreviewSection>

    <!-- A configuration or policy reference is NOT a dependency: it does not enter the
         dependency graph, so nothing above and nothing in the graph would ever mention
         it, and the referenced service had no way to learn who reaches into it. This is
         the reverse side of the reference the revision page links forward. -->
    <PreviewSection title="Referenced by" total={d.referencedBy?.total ?? 0} count={d.referencedBy?.count ?? 0} truncated={d.referencedBy?.truncated} empty="No service references this service's configuration or policy." help="These services reference this one's configuration or policy contract. That is not a runtime dependency, so it never appears in the graph.">
      <EntityRefList items={d.referencedBy?.items ?? []} showStatus={false} />
    </PreviewSection>

    <PreviewSection title="All revisions" collapsible open={noneInUse} summary="Every revision Pacto knows, in use or not" total={d.revisions?.total ?? 0} count={d.revisions?.count ?? 0} truncated={d.revisions?.truncated} viewAllHref={allRevisionsHref} viewAllLabel="View all revisions" empty="No known revisions."
      help="Newest first, whether or not anything is running them. Readiness is declared per revision — open one to see its gate.">
      <!-- Readiness is a DIMENSION of a revision, not a service-level score: it is
           declared per revision and gated per revision. Rolling it up here would invent
           a third definition of readiness, so the service page points at the one that
           already exists instead. -->
      <EntityRefList items={d.revisions?.items ?? []} showStatus={false} />
    </PreviewSection>

    <PreviewSection title="Expected dependencies" collapsible open={false} summary="Declared in the contract" total={d.dependencies?.total ?? 0} count={d.dependencies?.count ?? 0} truncated={d.dependencies?.truncated} viewAllHref={graphHref} viewAllLabel="Explore in graph" empty="No declared dependencies.">
      <EntityRefList items={d.dependencies?.items ?? []} showStatus={false} />
    </PreviewSection>

    <PreviewSection title="Dependents" collapsible open={false} summary="Services that declare this one" total={d.dependents?.total ?? 0} count={d.dependents?.count ?? 0} truncated={d.dependents?.truncated} viewAllHref={graphHref} viewAllLabel="Explore in graph" empty="Nothing depends on this service.">
      <EntityRefList items={d.dependents?.items ?? []} showStatus={false} />
    </PreviewSection>
  </div>

  <!-- Diagnostic layer: provenance and the honest list of what could
       not be determined. Both are kept in full and both are one click away. -->
  {#if (d.evidence?.count ?? 0) > 0}
    <PreviewSection title="Recent evidence" collapsible open={false} summary={lastSeen ? `Last observed ${formatDate(lastSeen)}` : 'Where the runtime picture came from'} total={d.evidence?.total ?? 0} count={d.evidence?.count ?? 0} truncated={d.evidence?.truncated}>
      <EvidenceList items={d.evidence?.items ?? []} />
    </PreviewSection>
  {/if}

  {#if (d.limitations?.count ?? 0) > 0}
    <!-- Limitations stay collapsible but NOT hidden: anything that makes this page's
         answer uncertain is already stated by the knowledge banner above, which is never
         collapsible. What is here is the per-entity detail behind that warning. -->
    <PreviewSection title="Limitations" tone="warn" collapsible open={false} summary="What Pacto could not determine" total={d.limitations?.total ?? 0} count={d.limitations?.count ?? 0} truncated={d.limitations?.truncated}>
      <LimitationsList items={d.limitations?.items ?? []} />
    </PreviewSection>
  {/if}
</div>

<style>
  .svc-entity { display: flex; flex-direction: column; gap: var(--sp-4); }
  .se-facts { display: flex; gap: var(--sp-5); flex-wrap: wrap; }
  .se-fact { display: flex; align-items: center; gap: var(--sp-2); }
  .se-k { font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.04em; color: var(--c-text-3); }
  .se-conflicts {
    display: flex; flex-direction: column; gap: var(--sp-2);
    padding: var(--sp-3); border-radius: var(--radius-md); font-size: var(--text-sm);
    background: var(--c-warn-bg); border: 1px solid var(--c-warn-border);
  }
  .se-conflicts-head { display: flex; gap: var(--sp-2); flex-wrap: wrap; align-items: baseline; }
  .se-conflict-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-1); }
  .se-conflict-list li { overflow-wrap: anywhere; }
  .se-conflict-more { color: var(--c-text-3); }
  .se-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(260px, 1fr)); gap: var(--sp-3); align-items: start; }
  /* Same frame as a PreviewSection -- this block is a section like any other, it just
     renders bars instead of a list. Its padding matches so the page has one rhythm. */
  .se-summary { border: 1px solid var(--c-border); border-radius: var(--radius-md); padding: var(--sp-4); background: var(--c-surface); display: flex; flex-direction: column; gap: var(--sp-3); }
  .se-summary h2, .se-summary h3 { margin: 0; }
  .se-lead { margin: 0; }
  .se-drift { list-style: none; margin: 0; padding: 0; display: flex; gap: var(--sp-2); flex-wrap: wrap; }
  .se-drift li { font-size: var(--text-sm); color: var(--c-text-2); background: var(--c-surface-inset); border: 1px solid var(--c-border); border-left: 3px solid var(--tone-c, var(--c-neutral)); padding: 2px 10px; border-radius: var(--radius-xs); }
  .se-drift li b { color: var(--c-text); font-variant-numeric: tabular-nums; }
  .tone-ok { --tone-c: var(--c-ok); }
  .tone-warn { --tone-c: var(--c-warn); }
</style>
