<script>
  import { hashForHref, fleetUrl, fleetAttentionUrl } from '../lib/router.ts';

  // The overview summary tiles. Actionable counts navigate to an exact filtered view.
  // Attention tiles are driven by the backend entry points (each carries an
  // authoritative href built from the exact category), so we never re-derive a
  // filter URL. The revision-match breakdown is informational (no backend entry
  // point), and observed-only relationships link to the graph's observed layer.
  let { summary = {}, entryPoints = [], attentionTotal = 0 } = $props();

  function tone(count) { return count > 0 ? 'warn' : 'ok'; }

  const revisionMatch = $derived([
    { label: 'Exact', count: summary.exactTargetLinks || 0, tone: 'ok' },
    { label: 'Inferred', count: summary.inferredTargetLinks || 0, tone: 'info' },
    { label: 'Ambiguous', count: summary.ambiguousTargetLinks || 0, tone: (summary.ambiguousTargetLinks || 0) > 0 ? 'warn' : 'neutral' },
    { label: 'Unresolved', count: summary.unresolvedTargetLinks || 0, tone: (summary.unresolvedTargetLinks || 0) > 0 ? 'warn' : 'neutral' },
  ]);
</script>

<div class="op-summary">
  <a class="tile tile-lead tone-{tone(summary.servicesNeedingAttention)}" href={fleetAttentionUrl()}>
    <span class="tile-count">{summary.servicesNeedingAttention || 0}</span>
    <span class="tile-label">services need attention</span>
    <span class="tile-sub">{attentionTotal} attention item{attentionTotal === 1 ? '' : 's'}</span>
  </a>

  <div class="tile-grid">
    {#each entryPoints as ep}
      {#if ep.view === 'attention'}
        <a class="tile tone-{tone(ep.count)}" href={hashForHref(ep.href)}>
          <span class="tile-count">{ep.count || 0}</span>
          <span class="tile-label">{ep.label}</span>
        </a>
      {/if}
    {/each}
    <a class="tile tone-{tone(summary.observedOnlyRelationships)}" href={fleetUrl({ layer: 'observed' })}>
      <span class="tile-count">{summary.observedOnlyRelationships || 0}</span>
      <span class="tile-label">observed-only relationships</span>
    </a>
  </div>

  <div class="rev-match" aria-label="Revision-match certainty across deployments">
    <span class="rm-title">Revision match</span>
    {#each revisionMatch as rm}
      <span class="rm-chip tone-{rm.tone}"><b>{rm.count}</b> {rm.label}</span>
    {/each}
  </div>
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
  .rev-match { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .rm-title { font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.04em; color: var(--c-text-3); }
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
