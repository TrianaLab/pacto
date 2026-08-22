<script>
  import { hashForHref, fleetAttentionUrl } from '../lib/router.ts';
  import { severityTone } from '../lib/entityLabels.ts';

  // The immediate situation: what requires action right now.
  //
  // Every entry is a backend entry point, rendered with the backend's own label, grade
  // and href -- nothing is re-worded or re-derived here.
  //
  // It used to render all eight entry points as identical tiles, so a confirmed
  // contract violation and an undeclared runtime call arrived at the same size, in the
  // same colour block, in a grid the eye reads left to right. Eight equal tiles is a
  // list with extra steps: it makes the reader do the ranking the backend already did.
  // Now the backend's own severity decides the treatment -- errors are tiles, everything
  // milder is one compact line -- so the first thing seen is the worst thing true.
  let { summary = {}, entryPoints = [], attentionTotal = 0 } = $props();

  // A tile has to take you somewhere you are not. Two entry points do not:
  // the uncategorised attention one is the lead tile's own number under a second
  // wording, and the overview one links back to this page, where the source health
  // strip a few lines below already shows exactly what it counts.
  const shown = $derived(entryPoints.filter(
    (ep) => ep.view !== 'overview' && !(ep.view === 'attention' && !ep.category),
  ));

  // An entry point whose severity this build does not recognise is promoted, not
  // demoted: a grade a newer engine spells differently must not quietly become a
  // footnote. Only the grades we can read as milder than an error are compacted.
  const MILDER = ['warning', 'info'];
  const leadTiles = $derived(shown.filter((ep) => !MILDER.includes(ep.severity)));
  const secondary = $derived(shown.filter((ep) => MILDER.includes(ep.severity)));

  // A count of zero is a clean state whatever the category; above zero the backend
  // grades the category (EntryPoint.severity) exactly as it grades the attention items
  // inside it. Painting every non-zero tile amber said a confirmed drift and an
  // undeclared call were equally urgent, and the list right below it disagreed.
  function tone(count, severity) { return count > 0 ? severityTone(severity) : 'ok'; }
  // The lead tile is not rendered from an entry point but still has one, so its grade is
  // read from the same place rather than guessed here.
  const leadSeverity = $derived(
    entryPoints.find((ep) => ep.view === 'attention' && !ep.category)?.severity,
  );
</script>

<div class="op-summary">
  <div class="tile-grid">
    <a class="tile tile-lead tone-{tone(summary.servicesNeedingAttention, leadSeverity)}" href={fleetAttentionUrl()}>
      <span class="tile-count t-metric">{summary.servicesNeedingAttention || 0}</span>
      <!-- Sentence case, like every tile beside it. A lowercase caption on the biggest
           tile and Title-leading labels on its siblings read as two kinds of thing. -->
      <span class="tile-label">Services need attention</span>
      <span class="tile-sub">{attentionTotal} attention item{attentionTotal === 1 ? '' : 's'}</span>
    </a>
    <!-- The observed-only tile used to be hand-written here, beside its own backend entry
         point -- so the same count reached the screen under two labels ("undeclared
         runtime call observed" against "Undeclared runtime dependencies observed"), in
         two cases, with a tone guessed locally instead of the grade its own items carry. -->
    {#each leadTiles as ep}
      <a class="tile tone-{tone(ep.count, ep.severity)}" href={hashForHref(ep.href)}>
        <span class="tile-count t-metric">{ep.count || 0}</span>
        <span class="tile-label">{ep.label}</span>
      </a>
    {/each}
  </div>

  {#if secondary.length}
    <!-- Present, exact and one click away, but not competing with the errors above it.
         The count is still shown: demoting the treatment must not delete the number. -->
    <ul class="op-secondary" aria-label="Also open">
      {#each secondary as ep}
        <li>
          <a class="op-sec tone-{tone(ep.count, ep.severity)}" href={hashForHref(ep.href)}>
            <span class="op-sec-dot" aria-hidden="true"></span>
            <span class="op-sec-count">{ep.count || 0}</span>
            <span class="op-sec-label">{ep.label}</span>
          </a>
        </li>
      {/each}
    </ul>
  {/if}
</div>

<style>
  .op-summary { display: flex; flex-direction: column; gap: var(--sp-3); }
  /* auto-fit, not auto-fill: with the grid holding a lead tile and only the ERROR
     entry points, a healthy fleet has one or two cells, and auto-fill would reserve
     empty tracks across a wide screen so two tiles sat in a row of ghosts. */
  .tile-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 190px), 1fr)); gap: var(--sp-3); }
  .tile {
    display: flex; flex-direction: column; gap: 2px; text-decoration: none;
    padding: var(--sp-3); border-radius: var(--radius-md);
    background: var(--c-surface); border: 1px solid var(--c-border); color: var(--c-text);
    border-left: 3px solid var(--tone-c, var(--c-neutral));
  }
  .tile:hover { border-color: var(--c-accent); }
  /* The lead tile is the one number the overview wants read first. It says so with a
     heavier tone edge and its position, NOT with a font-size of its own: a METRIC that
     is 18.75px in four tiles and 24.38px in the fifth is a second type scale, and a
     second type scale is what this whole pass exists to remove. */
  .tile-lead { border-left-width: 4px; }
  .tile-count { color: var(--tone-c, var(--c-text)); }
  .tile-label { font-size: var(--text-sm); color: var(--c-text-2); }
  .tile-sub { font-size: var(--text-xs); color: var(--c-text-3); }
  .op-secondary { list-style: none; margin: 0; padding: 0; display: flex; flex-wrap: wrap; gap: var(--sp-2) var(--sp-4); }
  .op-sec {
    display: inline-flex; align-items: center; gap: var(--sp-2); min-height: var(--touch-min);
    font-size: var(--text-sm); color: var(--c-text-2); text-decoration: none;
  }
  .op-sec:hover .op-sec-label, .op-sec:focus-visible .op-sec-label { text-decoration: underline; }
  /* Shape carries the grade too, not colour alone: the dot is a filled swatch of the
     tone beside a label that names the state in words (WCAG 1.4.1). */
  .op-sec-dot { width: 8px; height: 8px; border-radius: 50%; flex: none; background: var(--tone-c, var(--c-neutral)); }
  .op-sec-count { font-weight: 700; color: var(--c-text); font-variant-numeric: tabular-nums; }
  .tone-ok { --tone-c: var(--c-ok); }
  .tone-warn { --tone-c: var(--c-warn); }
  .tone-err { --tone-c: var(--c-err); }
  .tone-info { --tone-c: var(--c-info); }
  .tone-neutral { --tone-c: var(--c-neutral); }
</style>
