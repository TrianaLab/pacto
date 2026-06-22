<script>
  import { setFilter, toggleFilter } from '../lib/filters.svelte.ts';
  import { summarize, complianceClass, readinessBucketClass } from '../lib/format.ts';

  let { services = [] } = $props();

  // Compute metrics from the ALREADY-FILTERED service list
  const metrics = $derived(summarize(services));
</script>

<div class="summary-bar">
  <!-- Compliance % -->
  <button
    type="button"
    class="metric-tile"
    data-tip="Share of assessed services that pass all contract checks"
    onclick={() => setFilter('contractStatus', 'Compliant')}
  >
    <span class="metric-head">Compliant</span>
    {#if metrics.compliancePercent >= 0}
      <span class="metric-value {complianceClass(metrics.compliancePercent)}">{metrics.compliancePercent}<span class="metric-unit">%</span></span>
    {:else}
      <span class="metric-value text-dim">—</span>
    {/if}
    <span class="metric-sub">{metrics.compliant} of {metrics.assessed} assessed</span>
  </button>

  <!-- Needs attention -->
  <button
    type="button"
    class="metric-tile"
    class:tile-alert={metrics.needsAttention > 0}
    class:tile-clear={metrics.needsAttention === 0}
    data-tip="Services with warnings or validation errors"
    onclick={() => setFilter('contractStatus', metrics.needsAttention > 0 ? 'NonCompliant' : '')}
  >
    <span class="metric-head">Needs attention</span>
    <span class="metric-value">{metrics.needsAttention}</span>
    <span class="metric-sub">
      {#if metrics.needsAttention === 0}all clear{:else}{metrics.nonCompliant} error{metrics.nonCompliant !== 1 ? 's' : ''} · {metrics.warning} warning{metrics.warning !== 1 ? 's' : ''}{/if}
    </span>
  </button>

  <!-- Readiness avg — colored by the GATE (services passing minScore), not the
       absolute average, with an explicit ✓ when every configured service passes. -->
  <button
    type="button"
    class="metric-tile"
    data-tip={metrics.readiness.configured > 0
      ? `${metrics.readiness.ready} of ${metrics.readiness.configured} pass minScore (avg ${metrics.readiness.avgScore}%)`
      : 'No service declares a readiness gate'}
    onclick={() => toggleFilter('readinessStatus', 'ready')}
  >
    <span class="metric-head">Readiness</span>
    {#if metrics.readiness.configured > 0}
      {@const allPass = metrics.readiness.ready === metrics.readiness.configured}
      <span class="metric-value {allPass ? 'score-ok' : metrics.readiness.ready > 0 ? 'score-warn' : 'score-err'}">
        {metrics.readiness.avgScore}<span class="metric-unit">%</span>{#if allPass}<span class="gate-check" aria-label="all pass minScore" title="all pass minScore">&#10003;</span>{/if}
      </span>
      <span class="metric-sub">{metrics.readiness.ready} of {metrics.readiness.configured} pass gate</span>
    {:else}
      <span class="metric-value text-dim">—</span>
      <span class="metric-sub">not configured</span>
    {/if}
  </button>

  <!-- High impact -->
  <button
    type="button"
    class="metric-tile"
    class:tile-warn={metrics.highImpact > 0}
    data-tip="Services whose failure impacts 3 or more others"
  >
    <span class="metric-head">High impact</span>
    <span class="metric-value">{metrics.highImpact}</span>
    <span class="metric-sub">blast radius ≥ 3</span>
  </button>

  <!-- Check status totals -->
  <div class="metric-tile" data-tip="Total validation checks across all services">
    <span class="metric-head">Checks</span>
    <span class="metric-value text-dim">{services.reduce((sum, s) => sum + (s.checksTotal || 0), 0)}</span>
    <span class="metric-sub">{services.reduce((sum, s) => sum + (s.checksPassed || 0), 0)} passed · {services.reduce((sum, s) => sum + (s.checksFailed || 0), 0)} failed</span>
  </div>
</div>

<style>
  .summary-bar {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
    gap: var(--sp-3);
    margin-bottom: var(--sp-4);
    position: relative;
    z-index: 2;
  }

  .metric-tile {
    display: flex;
    flex-direction: column;
    gap: 3px;
    padding: var(--sp-4);
    border: 1px solid var(--c-border);
    border-radius: var(--radius-sm);
    background: var(--c-surface);
    text-decoration: none;
    color: inherit;
    transition: border-color var(--transition), box-shadow var(--transition), transform var(--transition);
    cursor: default;
    font: inherit;
    text-align: left;
  }

  button.metric-tile {
    cursor: pointer;
  }

  button.metric-tile:hover {
    border-color: var(--c-accent);
    box-shadow: var(--shadow-md);
    text-decoration: none;
    transform: translateY(-1px);
  }

  .metric-head {
    font-size: var(--text-xs);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--c-text-3);
  }

  .metric-value {
    font-size: 2rem;
    font-weight: 700;
    line-height: 1.1;
    color: var(--c-text);
  }

  .metric-value.score-ok {
    color: var(--c-ok);
  }

  .metric-value.score-warn {
    color: var(--c-warn);
  }

  .metric-value.score-err {
    color: var(--c-err);
  }

  .metric-value.text-dim {
    color: var(--c-text-3);
  }

  .metric-value.badge-ok {
    color: var(--c-ok);
  }

  .metric-value.badge-warn {
    color: var(--c-warn);
  }

  .metric-value.badge-err {
    color: var(--c-err);
  }

  .metric-value.badge-neutral {
    color: var(--c-text-3);
  }

  .metric-unit {
    font-size: 1rem;
    font-weight: 600;
    color: var(--c-text-3);
    margin-left: 1px;
  }

  .gate-check {
    font-size: 1rem;
    font-weight: 700;
    margin-left: 4px;
    color: var(--c-ok);
    vertical-align: super;
  }

  .metric-sub {
    font-size: var(--text-xs);
    color: var(--c-text-3);
  }

  .tile-clear .metric-value {
    color: var(--c-ok);
  }

  .tile-alert {
    border-color: var(--c-err-border);
    background: var(--c-err-bg);
  }

  .tile-alert .metric-value {
    color: var(--c-err);
  }

  .tile-warn .metric-value {
    color: var(--c-warn);
  }
</style>
