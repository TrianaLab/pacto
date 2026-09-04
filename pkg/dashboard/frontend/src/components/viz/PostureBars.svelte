<script>
  import { complianceSegments, linkSegments, evidenceSegments, severitySegments, segmentTotal } from '../../lib/distributions.ts';
  import { formatDate } from '../../lib/dateFormat.ts';
  import DistributionBar from './DistributionBar.svelte';

  // The operational posture of a target population, drawn the SAME way everywhere.
  //
  // Compliance, revision-match certainty and evidence freshness are three orthogonal
  // questions, and the whole reason they are three bars rather than one health score
  // is that they disagree in useful ways: a target can be compliant against a revision
  // we only guessed at, and a fleet of "unknown" is not a fleet of failures. Rolling
  // them into a single number would delete exactly the information the user came for.
  //
  // The fleet, a service and an owner all ask these questions of different
  // populations, so this component owns the wording, the ordering and the drill-down
  // rule once. Three surfaces each writing their own copy is how "Unknown" ends up
  // amber on one page and grey on the next.
  //
  // `summary` MUST be a backend aggregate over a COMPLETE population (OverviewSummary,
  // ServiceSummary, OwnerSummary) — never counted from the bounded previews beside it.
  // `attentionUrl(category)` scopes the drill-down to the population being drawn: a
  // service page must not send the user to the fleet-wide backlog. Omit it and the
  // buckets render as plain text rather than as links that lie about their scope.
  let {
    summary = {},
    level = 3,
    attentionUrl = null,
    empty = 'Nothing running has been observed here yet, so there is no compliance, identity or freshness picture.',
  } = $props();

  const targets = $derived(summary.targets || 0);
  const ev = $derived(summary.evidence ?? {});
  const href = (category) => (attentionUrl ? attentionUrl(category) : undefined);

  // Every bucket with a triage destination gets one, so a proportion is a way IN
  // rather than a picture. Compliant and Exact have none by design: there is no "list
  // of things that are fine" workspace, and inventing one would be a dead link.
  const compliance = $derived(complianceSegments(summary.compliance, {
    nonCompliant: href('non-compliant'),
    unknown: href('unknown'),
    invalid: href('invalid'),
  }));
  const links = $derived(linkSegments(summary.links).map((s) => (
    s.label === 'Ambiguous' || s.label === 'Unresolved' ? { ...s, href: href('unresolved') } : s
  )));
  const evidence = $derived(evidenceSegments(ev).map((s) => (
    s.label === 'Stale evidence' ? { ...s, href: href('stale') } : s
  )));
  const findings = $derived(severitySegments(summary.findings));
  const findingsTotal = $derived(segmentTotal(findings));
</script>

{#if targets > 0}
  <div class="pb-dists">
    <DistributionBar
      title="Compliance"
      {level}
      description="Whether each running target is observed to obey its contract."
      segments={compliance}
      total={targets}
    />
    <DistributionBar
      title="Revision-match certainty"
      {level}
      description="How confidently each target is matched to the exact revision it runs. Short of exact still means something is running."
      segments={links}
      total={targets}
    />
    <DistributionBar
      title="Evidence freshness"
      {level}
      description="How recently each target was observed. Never observed is its own state, not stale."
      segments={evidence}
      total={targets}
    />
    {#if findingsTotal > 0}
      <DistributionBar
        title="Findings by severity"
        {level}
        description="Every finding attributed to these targets."
        segments={findings}
      />
    {/if}
  </div>
  {#if ev.oldest || ev.newest}
    <p class="pb-hint">
      Evidence spans {ev.oldest ? formatDate(ev.oldest) : 'unknown'} to {ev.newest ? formatDate(ev.newest) : 'unknown'}{(ev.quarantined ?? 0) > 0 ? ` · ${ev.quarantined} quarantined` : ''}.
    </p>
  {/if}
{:else}
  <!-- No targets is a real, common state (a contract published before anything runs it)
       and it is NOT a compliance failure. Saying so beats three empty bars. -->
  <p class="pb-hint">{empty}</p>
{/if}

<style>
  /* Three columns where there is room, so the three questions the copy promises arrive
     as three things side by side rather than as two and an orphan with an empty half-row
     beside it. Narrower than the two-up grids elsewhere on purpose: these bars carry the
     shortest legends on the page. auto-fit keeps a lone bar from stretching to an
     unreadable width, and one column on a phone. */
  .pb-dists { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 260px), 1fr)); gap: var(--sp-4); }
  .pb-hint { margin: 0; font-size: var(--text-sm); color: var(--c-text-3); }
</style>
