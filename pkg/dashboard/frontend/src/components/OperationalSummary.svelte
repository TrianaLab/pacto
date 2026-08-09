<script>
  import { hashForHref, fleetUrl, fleetAttentionUrl } from '../lib/router.ts';

  // The overview summary tiles. Actionable counts navigate to an exact filtered view.
  // Attention tiles are driven by the backend entry points (each carries an
  // authoritative href built from the exact category), so we never re-derive a
  // filter URL. The revision-match breakdown is informational (no backend entry
  // point), and observed-only relationships link to the search-first graph (which is
  // never a whole-fleet render, so the tile opens the graph's discovery landing).
  let { summary = {}, entryPoints = [], attentionTotal = 0 } = $props();

  function tone(count) { return count > 0 ? 'warn' : 'ok'; }

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
  const observedOnly = $derived(summary.observedOnlyRelationships || 0);
</script>

<div class="op-summary">
  <a class="tile tile-lead tone-{tone(summary.servicesNeedingAttention)}" href={fleetAttentionUrl()}>
    <span class="tile-count">{summary.servicesNeedingAttention || 0}</span>
    <span class="tile-label">services need attention</span>
    <span class="tile-sub">{attentionTotal} attention item{attentionTotal === 1 ? '' : 's'}</span>
  </a>

  <div class="tile-grid">
    {#each entryPoints as ep}
      <!-- Only the CATEGORISED attention entry points become tiles. The uncategorised
           one is "all attention", which is already the lead tile above it -- rendering
           both put the same number on screen twice, which a first-time reader takes for
           two different measurements. -->
      {#if ep.view === 'attention' && ep.category}
        <a class="tile tone-{tone(ep.count)}" href={hashForHref(ep.href)}>
          <span class="tile-count">{ep.count || 0}</span>
          <span class="tile-label">{ep.label}</span>
        </a>
      {/if}
    {/each}
    <a class="tile tone-{tone(summary.observedOnlyRelationships)}" href={fleetUrl()}>
      <span class="tile-count">{observedOnly}</span>
      <span class="tile-label">undeclared runtime call{observedOnly === 1 ? '' : 's'} observed</span>
    </a>
  </div>

  <details class="rev-match">
    <summary>
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
  .rev-match summary {
    display: flex; align-items: baseline; gap: var(--sp-2); flex-wrap: wrap; cursor: pointer;
    font-size: var(--text-sm); color: var(--c-text-2); min-height: var(--touch-min);
  }
  .rev-match summary::marker { color: var(--c-text-3); }
  .rm-lead b { color: var(--c-text); }
  .rm-toggle { font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.04em; color: var(--c-text-3); }
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
