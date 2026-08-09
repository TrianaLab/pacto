<script>
  import { hashForHref, fleetAttentionUrl } from '../lib/router.ts';
  import { severityTone } from '../lib/entityLabels.ts';

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

  const revisionMatch = $derived([
    { label: 'Exact', count: summary.exactTargetLinks || 0, tone: 'ok' },
    { label: 'Inferred', count: summary.inferredTargetLinks || 0, tone: 'info' },
    { label: 'Ambiguous', count: summary.ambiguousTargetLinks || 0, tone: (summary.ambiguousTargetLinks || 0) > 0 ? 'warn' : 'neutral' },
    { label: 'Unresolved', count: summary.unresolvedTargetLinks || 0, tone: (summary.unresolvedTargetLinks || 0) > 0 ? 'warn' : 'neutral' },
  ]);
  // Exact/Inferred/Ambiguous/Unresolved is precise and load-bearing, but it is also the
  // first taxonomy a novice hits on the landing page. The precision is kept; only its
  // cost is reduced -- the headline is the one sentence anyone can read, and the
  // four-way breakdown is one disclosure away.
  const exactLinks = $derived(summary.exactTargetLinks || 0);
  const totalLinks = $derived(revisionMatch.reduce((n, rm) => n + rm.count, 0));
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

  <details class="rev-match disclosure">
    <summary>
      <span class="disclosure-caret" aria-hidden="true">&#9656;</span>
      {#if totalLinks === 0}
        <span class="rm-lead">Nothing running has been matched to a revision yet.</span>
      {:else}
        <span class="rm-lead">We know exactly which revision is running on <b>{exactLinks} of {totalLinks}</b> operational targets.</span>
      {/if}
      <span class="rm-toggle">Revision match detail</span>
    </summary>
    <p class="rm-help">How confidently each running target was tied to one revision. Anything short of an exact match still means something is running — only that we are less sure which revision it is.</p>
    <div class="rm-chips">
      {#each revisionMatch as rm}
        <span class="rm-chip tone-{rm.tone}"><b>{rm.count}</b> {rm.label}</span>
      {/each}
    </div>
  </details>
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
  .rev-match { display: flex; flex-direction: column; gap: var(--sp-2); }
  /* Layout only -- everything about how a disclosure looks and opens is the shared
     .disclosure class. This summary carries a sentence AND a toggle label, so it needs
     the wider gap; nothing else here is specific to it. */
  .rev-match summary { gap: var(--sp-2); }
  .rm-lead b { color: var(--c-text); }
  .rm-toggle { color: var(--c-text-3); }
  .rm-help { margin: 0; font-size: var(--text-sm); color: var(--c-text-3); }
  .rm-chips { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .rm-chip {
    font-size: var(--text-xs); padding: 2px 8px; border-radius: var(--radius-xs);
    background: var(--c-surface-inset); color: var(--c-text-2);
    border: 1px solid var(--c-border); border-color: var(--tone-c, var(--c-border));
  }
  .rm-chip b { color: var(--tone-c, var(--c-text)); }
  .tone-ok { --tone-c: var(--c-ok); }
  .tone-warn { --tone-c: var(--c-warn); }
  .tone-err { --tone-c: var(--c-err); }
  .tone-info { --tone-c: var(--c-info); }
  .tone-neutral { --tone-c: var(--c-neutral); }
</style>
