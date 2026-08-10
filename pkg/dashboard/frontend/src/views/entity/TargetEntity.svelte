<script>
  import {
    linkStateLabel, linkStateTone, retrievabilityLabel, retrievabilityTone,
  } from '../../lib/entityLabels.ts';
  import { formatDate } from '../../lib/dateFormat.ts';
  import EntityLink from '../../components/EntityLink.svelte';
  import IdentityBadge from '../../components/IdentityBadge.svelte';
  import PreviewSection from '../../components/PreviewSection.svelte';
  import FindingList from '../../components/FindingList.svelte';
  import LimitationsList from '../../components/LimitationsList.svelte';
  import RelationshipList from '../../components/RelationshipList.svelte';
  import { fleetGraphFocusUrl, fleetEntityListUrl } from '../../lib/router.ts';

  // The deployment/target page (requirement F). It makes the TWO identity dimensions
  // immediately understandable and independent:
  //   1. revision-match certainty (linkState: exact / inferred / ambiguous / unresolved)
  //   2. content retrievability (identity: exact retrievable / mutable / no-ref / local /
  //      malformed / digest-mismatch)
  // An exact match to non-retrievable content is VALID and never rendered as
  // contradictory (they are separate badges with independent tones). An
  // ambiguous/unresolved match NEVER presents a specific revision as authoritative.
  let { detail } = $props();
  const d = $derived(detail.target ?? {});
  const id = $derived(d.identity ?? {});
  // A specific revision is authoritative ONLY when the match is exact or inferred.
  const authoritativeRevision = $derived(
    (d.linkState === 'exact' || d.linkState === 'inferred') ? d.revision : null,
  );
  const unresolvedMatch = $derived(d.linkState === 'ambiguous' || d.linkState === 'unresolved');
  // "Contributing data sources" only earns a row when it says something the primary
  // "Data source" row did not. On the common single-source target the two rows print
  // the identical value under two headings, which reads as two different facts.
  const extraSources = $derived(
    (d.sources?.count ?? 0) > 0 && !(d.sources.count === 1 && !d.sources.truncated && d.sources.items[0] === d.source),
  );
  const r = $derived(d.readiness ?? null);
  // The relationships below are attributed to the SERVICE, not to this target: nothing
  // in the snapshot observes traffic per instance. The heading and the note say so,
  // because presenting service-scoped observation under a target heading would quietly
  // upgrade "somewhere in this service" into "here".
  const serviceGraphHref = $derived(d.service?.key ? fleetGraphFocusUrl('service', d.service.key) : '');
  // "Where else is this service running" is a question a single target page cannot
  // answer on its own -- the scoped target inventory can, from the same canonical key.
  const siblingTargetsHref = $derived(d.service?.key ? fleetEntityListUrl('target', { service: d.service.key }) : '');
</script>

<div class="tgt-entity">
  <!-- The two orthogonal identity dimensions, side by side. -->
  <section class="te-identity" aria-label="Operational target identity">
    <div class="te-dim">
      <span class="te-k">Revision match</span>
      <IdentityBadge label={linkStateLabel(d.linkState)} tone={linkStateTone(d.linkState)} />
    </div>
    <div class="te-dim">
      <span class="te-k">Content</span>
      <IdentityBadge label={retrievabilityLabel(id.identityClass, id.retrievable)} tone={retrievabilityTone(id.identityClass, id.retrievable)} />
    </div>
  </section>

  {#if authoritativeRevision}
    <div class="te-rev">
      <span class="te-k">{d.linkState === 'exact' ? 'Running revision' : 'Inferred revision'}</span>
      <EntityLink ref={authoritativeRevision} showStatus={false} showKind={false} />
      {#if d.linkState === 'inferred'}<span class="te-note">match inferred, not certain</span>{/if}
    </div>
  {:else if unresolvedMatch}
    <!-- The badge above already says WHICH state this is. Restating it here, then
         restating it again in different words, said one fact three times and never got
         to the part a first-time user actually needs: why we cannot name the revision,
         and what that stops us concluding. Each state explains its own reason. -->
    <div class="te-rev te-rev-unresolved" role="status">
      {#if d.linkState === 'ambiguous'}
        More than one known revision matches what we observed here, so we cannot say which one is running.
      {:else}
        Nothing we observed here ties back to a known revision, so we cannot say which one is running.
      {/if}
      Something IS running — this is a gap in what we can see, not an empty target.
    </div>
  {/if}

  <section class="te-facts">
    <div class="te-fact"><span class="te-k">Service</span><EntityLink ref={d.service} showStatus={false} showKind={false} /></div>
    {#if siblingTargetsHref}<div class="te-fact"><span class="te-k">Elsewhere</span><a href={siblingTargetsHref} data-testid="sibling-targets-link">All targets of this service</a></div>{/if}
    {#if d.scope}<div class="te-fact"><span class="te-k">Scope</span><span>{d.scope}</span></div>{/if}
    {#if d.kind}<div class="te-fact"><span class="te-k">Kind</span><span>{d.kind}</span></div>{/if}
    <!-- No Compliance row: the page header already badges this exact state in the same
         words. Evaluation coverage below is the fact this section adds -- how much of the
         contract was actually checked to reach it. -->
    {#if d.coverage}<div class="te-fact"><span class="te-k">Evaluation coverage</span><span>{d.coverage.evaluated} of {d.coverage.required} evaluated</span></div>{/if}
    {#if d.source}<div class="te-fact"><span class="te-k">Data source</span><span>{d.source}</span></div>{/if}
    {#if extraSources}<div class="te-fact"><span class="te-k">Contributing data sources</span><span>{d.sources.items.join(', ')}{d.sources.truncated ? ` (+${d.sources.total - d.sources.count})` : ''}</span></div>{/if}
    {#if d.evidenceAt}<div class="te-fact"><span class="te-k">Evidence at</span><span>{formatDate(d.evidenceAt)}</span></div>{/if}
    {#if d.reconciledAt}<div class="te-fact"><span class="te-k">Reconciled at</span><span>{formatDate(d.reconciledAt)}</span></div>{/if}
    {#if d.stale}<div class="te-fact"><IdentityBadge label="Evidence stale" tone="warn" /></div>{/if}
    {#if d.quarantined}<div class="te-fact"><IdentityBadge label="Quarantined" tone="err" /></div>{/if}
    <!-- The owner used to be a section of its own at the foot of the page: one row, one
         border and a heading-less block a reader had to scroll past everything else to
         reach. It is a fact about this target like the scope and the data source, so it
         belongs in the one facts strip (requirement 11). -->
    {#if d.ownership}
      <div class="te-fact">
        <span class="te-k">Owner</span>
        {#if d.ownership.ref}<EntityLink ref={d.ownership.ref} showStatus={false} showKind={false} />{:else}<span>{d.ownership.owner || 'Unowned'}</span>{/if}
      </div>
    {/if}
  </section>

  {#if (d.findings?.count ?? 0) > 0}
    <PreviewSection title="Findings" tone="err" total={d.findings?.total ?? 0} count={d.findings?.count ?? 0} truncated={d.findings?.truncated}>
      <FindingList items={d.findings?.items ?? []} />
    </PreviewSection>
  {/if}

  {#if (d.observedRuntime?.count ?? 0) > 0}
    <PreviewSection title="Observed runtime" collapsible open={false} summary="Raw fields as the source reported them" total={d.observedRuntime?.total ?? null} count={d.observedRuntime?.count ?? 0} truncated={d.observedRuntime?.truncated}>
      <ul class="te-runtime">
        {#each d.observedRuntime.items as f, i (i)}
          <li><span class="rt-key">{f.key}</span><span class="rt-val">{f.value}</span></li>
        {/each}
      </ul>
    </PreviewSection>
  {/if}

  {#if (d.labels?.count ?? 0) > 0}
    <!-- Workload metadata as the platform reported it: the namespace/team/version
         labels an operator recognises the deployment by, and the fastest way to tell
         two same-named targets apart. -->
    <PreviewSection title="Workload labels" collapsible open={false} summary="How the platform labels this workload" total={d.labels?.total ?? null} count={d.labels?.count ?? 0} truncated={d.labels?.truncated}>
      <ul class="te-runtime">
        {#each d.labels.items as f, i (i)}
          <li><span class="rt-key">{f.key}</span><span class="rt-val">{f.value}</span></li>
        {/each}
      </ul>
    </PreviewSection>
  {/if}

  {#if r}
    <!-- Readiness reported BY the runtime source for this target, which is a different
         statement from the readiness the revision's authors declared. When both exist
         they can legitimately disagree, so they are never merged. -->
    <section class="te-readiness">
      <div class="te-rr-head">
        <h2 class="t-section-title">Reported readiness</h2>
        <IdentityBadge label={r.passing ? 'Passing' : 'Not passing'} tone={r.passing ? 'ok' : 'warn'} />
      </div>
      <p class="te-note t-body-2">Reported by the source observing this target — not the revision's declared gate.</p>
      <p class="te-rr-line t-body">Score {r.score} / {r.minScore} required · {r.doneCount} done · {r.partialCount} partial · {r.notDoneCount} not done{r.expired ? ' · expired' : ''}</p>
    </section>
  {/if}

  {#if (d.serviceRelationships?.count ?? 0) > 0}
    <PreviewSection
      title="Service traffic and differences"
      total={d.serviceRelationships?.total ?? null}
      count={d.serviceRelationships?.count ?? 0}
      truncated={d.serviceRelationships?.truncated}
      viewAllHref={serviceGraphHref}
      viewAllLabel="Explore in graph"
    >
      <p class="te-note t-body-2">Observed for the whole service. Nothing we collect attributes traffic to one target, so these edges are not evidence about this instance specifically.</p>
      <RelationshipList items={d.serviceRelationships?.items ?? []} selfKey={d.service?.key ?? ''} />
    </PreviewSection>
  {/if}

  {#if (d.limitations?.count ?? 0) > 0}
    <PreviewSection title="Limitations" tone="warn" collapsible open={false} summary="What Pacto could not determine" total={d.limitations?.total ?? 0} count={d.limitations?.count ?? 0} truncated={d.limitations?.truncated}>
      <LimitationsList items={d.limitations?.items ?? []} />
    </PreviewSection>
  {/if}
</div>

<style>
  .tgt-entity { display: flex; flex-direction: column; gap: var(--sp-4); }
  .te-identity { display: flex; gap: var(--sp-6); flex-wrap: wrap; padding: var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-md); background: var(--c-surface); }
  .te-dim { display: flex; flex-direction: column; gap: var(--sp-1); }
  .te-facts { display: flex; gap: var(--sp-5); flex-wrap: wrap; }
  .te-fact, .te-rev { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .te-k { font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.04em; color: var(--c-text-3); }
  .te-rev-unresolved { padding: var(--sp-2) var(--sp-3); border-radius: var(--radius-sm); background: var(--c-surface-inset); border: 1px solid var(--c-border); color: var(--c-text-2); font-size: var(--text-sm); }
  .te-runtime { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-1); }
  .te-runtime li { display: flex; gap: var(--sp-3); font-size: var(--text-sm); }
  .rt-key { color: var(--c-text-3); font-family: var(--font-mono, monospace); min-width: 40%; overflow-wrap: anywhere; }
  .rt-val { color: var(--c-text); overflow-wrap: anywhere; }
  .te-readiness { border: 1px solid var(--c-border); border-radius: var(--radius-md); padding: var(--sp-4); background: var(--c-surface); display: flex; flex-direction: column; gap: var(--sp-2); }
  .te-rr-head { display: flex; align-items: baseline; gap: var(--sp-3); }
  .te-rr-head h2 { margin: 0; }
  .te-rr-line, .te-note { margin: 0; }
</style>
