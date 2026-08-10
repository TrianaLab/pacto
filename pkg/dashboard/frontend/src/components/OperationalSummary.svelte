<script>
  import { hashForHref, fleetAttentionUrl } from '../lib/router.ts';
  import { severityTone } from '../lib/entityLabels.ts';
  import PostureBars from './viz/PostureBars.svelte';

  // The overview summary tiles. Every tile is a backend entry point, rendered with the
  // backend's own label, grade and href -- nothing is re-worded or re-derived here. The
  // revision-match breakdown below is the one informational block (no entry point).
  let { summary = {}, entryPoints = [], attentionTotal = 0 } = $props();

  // A tile has to take you somewhere you are not. Two entry points do not:
  // the uncategorised attention one is the lead tile's own number under a second
  // wording, and the overview one links back to this page, where the source health
  // strip a few lines below already shows exactly what it counts.
  const tiles = $derived(entryPoints.filter(
    (ep) => ep.view !== 'overview' && !(ep.view === 'attention' && !ep.category),
  ));

  // A count of zero is a clean state whatever the category; above zero the backend
  // grades the category (EntryPoint.severity) exactly as it grades the attention items
  // inside it. Painting every non-zero tile amber said a confirmed drift and an
  // undeclared call were equally urgent, and the list right below it disagreed.
  function tone(count, severity) { return count > 0 ? severityTone(severity) : 'ok'; }
  // The two tiles that are not rendered from an entry point still carry one, so their
  // grade is read from the same place rather than guessed here.
  const severityOf = (view, category) =>
    entryPoints.find((ep) => ep.view === view && (ep.category || '') === category)?.severity;

  // Exact/Inferred/Ambiguous/Unresolved is precise and load-bearing, but it is also the
  // first taxonomy a novice hits on the landing page. The precision is kept; only its
  // cost is reduced -- the headline is the one sentence anyone can read, and the
  // four-way breakdown is one disclosure away.
  const exactLinks = $derived(summary.exactTargetLinks || 0);
  // Targets is the backend's own population count, not a sum of the buckets: if the two
  // ever disagree, DistributionBar shows the gap as "Unclassified" instead of silently
  // rescaling a proportion to whatever happened to add up.
  const targets = $derived(summary.targets || 0);

  // The fleet posture is drawn by the SAME component a service page and an owner page
  // use, so the three surfaces cannot drift in wording, ordering or colour. The flat
  // OverviewSummary counters are reshaped into the shared tally shape here rather than
  // in the component: the overview is the one surface whose aggregate predates the
  // shared shape, and translating it once at the edge beats teaching the component
  // about a second field layout.
  const posture = $derived({
    targets,
    compliance: {
      compliant: summary.compliantTargets,
      nonCompliant: summary.nonCompliantTargets,
      unknown: summary.unknownTargets,
      invalid: summary.invalidTargets,
      other: summary.otherComplianceTargets,
    },
    links: {
      exact: summary.exactTargetLinks,
      inferred: summary.inferredTargetLinks,
      ambiguous: summary.ambiguousTargetLinks,
      unresolved: summary.unresolvedTargetLinks,
    },
    evidence: summary.evidence,
  });
  // Fleet scope: no service/owner filter, so a drill-down lands on the whole backlog
  // for that category -- which is exactly the population this chart drew.
  const attentionUrl = (category) => fleetAttentionUrl({ category });
</script>

<div class="op-summary">
  <a class="tile tile-lead tone-{tone(summary.servicesNeedingAttention, severityOf('attention', ''))}" href={fleetAttentionUrl()}>
    <span class="tile-count">{summary.servicesNeedingAttention || 0}</span>
    <!-- Sentence case, like every tile beside it. A lowercase caption on the biggest
         tile and Title-leading labels on its four siblings read as two kinds of thing. -->
    <span class="tile-label">Services need attention</span>
    <span class="tile-sub">{attentionTotal} attention item{attentionTotal === 1 ? '' : 's'}</span>
  </a>

  <div class="tile-grid">
    <!-- The observed-only tile used to be hand-written here, beside its own backend entry
         point -- so the same count reached the screen under two labels ("undeclared
         runtime call observed" against "Undeclared runtime dependencies observed"), in
         two cases, with a tone guessed locally instead of the grade its own items carry. -->
    {#each tiles as ep}
      <a class="tile tone-{tone(ep.count, ep.severity)}" href={hashForHref(ep.href)}>
        <span class="tile-count">{ep.count || 0}</span>
        <span class="tile-label">{ep.label}</span>
      </a>
    {/each}
  </div>

  {#if targets > 0}
    <!-- Overall posture: the two orthogonal questions, over the COMPLETE target
         population the backend counted. They are kept apart on purpose -- a target can
         be compliant against a revision we only guessed at.

         "Fleet" is an internal word (the snapshot package, the /fleet routes), never a
         product one: the heading said "Fleet posture" above a page whose whole
         vocabulary is services, revisions and operational targets. -->
    <section class="ov-posture" aria-labelledby="ov-posture-h">
      <h2 id="ov-posture-h" class="ov-posture-h">Overall posture</h2>
      <p class="ov-posture-sub">
        {summary.services || 0} {(summary.services || 0) === 1 ? 'service' : 'services'} ·
        {summary.revisions || 0} {(summary.revisions || 0) === 1 ? 'revision' : 'revisions'} ·
        {targets} operational {targets === 1 ? 'target' : 'targets'}
      </p>
      <PostureBars summary={posture} {attentionUrl} />
      <p class="ov-posture-note">We know exactly which revision is running on {exactLinks} of {targets} operational targets{(summary.staleTargets || 0) > 0 ? `, and ${summary.staleTargets} of them were last observed too long ago to trust` : ''}.</p>
    </section>
  {/if}
</div>

<style>
  .op-summary { display: flex; flex-direction: column; gap: var(--sp-4); }
  .tile-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(170px, 1fr)); gap: var(--sp-3); }
  .tile {
    display: flex; flex-direction: column; gap: 2px; text-decoration: none;
    padding: var(--sp-3); border-radius: var(--radius-md);
    background: var(--c-surface); border: 1px solid var(--c-border); color: var(--c-text);
    border-left: 3px solid var(--tone-c, var(--c-neutral));
  }
  .tile:hover { border-color: var(--c-accent); }
  .tile-lead { border-left-width: 4px; }
  .tile-lead .tile-count { font-size: var(--text-xl); }
  .tile-count { font-size: var(--text-lg); font-weight: 700; color: var(--tone-c, var(--c-text)); }
  .tile-label { font-size: var(--text-sm); color: var(--c-text-2); }
  .tile-sub { font-size: var(--text-xs); color: var(--c-text-3); }
  .ov-posture { display: flex; flex-direction: column; gap: var(--sp-3); }
  .ov-posture-h { margin: 0; font-size: var(--text-md); }
  .ov-posture-sub, .ov-posture-note { margin: 0; font-size: var(--text-sm); color: var(--c-text-3); }
  .tone-ok { --tone-c: var(--c-ok); }
  .tone-warn { --tone-c: var(--c-warn); }
  .tone-err { --tone-c: var(--c-err); }
  .tone-info { --tone-c: var(--c-info); }
  .tone-neutral { --tone-c: var(--c-neutral); }
</style>
