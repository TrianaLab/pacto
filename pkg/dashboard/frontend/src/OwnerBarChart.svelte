<script>
  import { ownerUrl } from './lib/router.ts';
  import { complianceClass, computeTooltipPosition } from './lib/format.ts';

  let { owners = [], sortBy = 'services' } = $props();

  // Determine chart mode from current sort
  const STATUS_SEGMENTS = [
    { key: 'compliant', label: 'Compliant', color: 'var(--c-ok)' },
    { key: 'warning', label: 'Warning', color: 'var(--c-warn)' },
    { key: 'nonCompliant', label: 'Non-Compliant', color: 'var(--c-err)' },
    { key: 'reference', label: 'Reference', color: 'var(--c-neutral)' },
  ];

  const METRIC_CONFIG = {
    services: { label: 'Services by Status', getValue: (o) => o.services, segments: true },
    blast: { label: 'Blast Radius', getValue: (o) => o.totalBlast, color: 'var(--c-warn)', unit: '' },
    compliance: { label: '% Compliant', getValue: (o) => o.compliancePercent >= 0 ? o.compliancePercent : 0, color: 'var(--c-ok)', unit: '%', max: 100 },
    warning: { label: 'Warnings', getValue: (o) => o.warning, color: 'var(--c-warn)', unit: '' },
    nonCompliant: { label: 'Non-Compliant', getValue: (o) => o.nonCompliant, color: 'var(--c-err)', unit: '' },
    key: { label: 'Services by Status', getValue: (o) => o.services, segments: true },
  };

  let metric = $derived(METRIC_CONFIG[sortBy] || METRIC_CONFIG.services);
  let maxValue = $derived.by(() => {
    if (metric.max) return metric.max;
    if (metric.segments) return Math.max(1, ...owners.map((o) => o.services));
    return Math.max(1, ...owners.map((o) => metric.getValue(o)));
  });

  // Tooltip uses fixed positioning to avoid clipping by container overflow
  let tip = $state({ visible: false, left: 0, top: 0, owner: null });
  let tipEl = $state(null);

  function showTooltip(e, owner) {
    const w = tipEl?.offsetWidth || 180;
    const h = tipEl?.offsetHeight || 100;
    const pos = computeTooltipPosition(e.clientX, e.clientY, w, h);
    tip = { visible: true, left: pos.left, top: pos.top, owner };
  }

  function hideTooltip() {
    tip = { ...tip, visible: false };
  }

  function navigate(key) {
    location.hash = ownerUrl(key);
  }
</script>

<div class="chart-card fade-in-up">
  <div class="chart-header">
    <span class="chart-title">{metric.label}</span>
    {#if metric.segments}
      <div class="chart-legend">
        {#each STATUS_SEGMENTS as seg}
          <span class="legend-item">
            <span class="legend-dot" style="background:{seg.color}"></span>
            {seg.label}
          </span>
        {/each}
      </div>
    {/if}
  </div>

  <div class="chart-body">
    {#each owners as owner}
      <div
        class="chart-row"
        role="button"
        tabindex="0"
        onclick={() => navigate(owner.key)}
        onkeydown={(e) => { if (e.key === 'Enter') navigate(owner.key); }}
        onmouseenter={(e) => showTooltip(e, owner)}
        onmousemove={(e) => showTooltip(e, owner)}
        onmouseleave={hideTooltip}
      >
        <span class="row-label" title={owner.key}>{owner.key}</span>
        <div class="row-bar">
          <div class="bar-track">
            {#if metric.segments}
              {#each STATUS_SEGMENTS as seg}
                {#if owner[seg.key] > 0}
                  <div
                    class="bar-seg"
                    style="width:{(owner[seg.key] / maxValue) * 100}%; background:{seg.color}"
                  ></div>
                {/if}
              {/each}
            {:else}
              {@const val = metric.getValue(owner)}
              {#if val > 0}
                <div
                  class="bar-seg bar-seg-single"
                  style="width:{(val / maxValue) * 100}%; background:{metric.color}"
                ></div>
              {/if}
            {/if}
          </div>
          <span class="row-count">
            {#if metric.segments}
              {owner.services}
            {:else}
              {metric.getValue(owner)}{metric.unit}
            {/if}
          </span>
        </div>
      </div>
    {/each}
  </div>
</div>

<!-- Tooltip rendered with fixed positioning to avoid container clipping -->
{#if tip.visible && tip.owner}
  {@const o = tip.owner}
  <div
    class="chart-tooltip"
    bind:this={tipEl}
    style="left:{tip.left}px; top:{tip.top}px"
  >
    <div class="tt-header">
      <span class="tt-title">{o.key}</span>
      <span class="tt-count">{o.services} service{o.services !== 1 ? 's' : ''}</span>
    </div>
    <div class="tt-segments">
      {#if o.compliant > 0}<div class="tt-seg"><span class="tt-dot" style="background:var(--c-ok)"></span><span class="tt-label">Compliant</span><span class="tt-val">{o.compliant}</span></div>{/if}
      {#if o.warning > 0}<div class="tt-seg"><span class="tt-dot" style="background:var(--c-warn)"></span><span class="tt-label">Warning</span><span class="tt-val">{o.warning}</span></div>{/if}
      {#if o.nonCompliant > 0}<div class="tt-seg"><span class="tt-dot" style="background:var(--c-err)"></span><span class="tt-label">Non-Compliant</span><span class="tt-val">{o.nonCompliant}</span></div>{/if}
      {#if o.reference > 0}<div class="tt-seg"><span class="tt-dot" style="background:var(--c-neutral)"></span><span class="tt-label">Reference</span><span class="tt-val">{o.reference}</span></div>{/if}
    </div>
    {#if o.compliancePercent >= 0}
      <div class="tt-footer">
        <span class="score {complianceClass(o.compliancePercent)}">{o.compliancePercent}%</span> compliant
      </div>
    {/if}
    {#if o.totalBlast > 0}
      <div class="tt-footer">Blast radius: {o.totalBlast}</div>
    {/if}
  </div>
{/if}

<style>
  .chart-card {
    background: var(--c-surface);
    border: 1px solid var(--c-border);
    border-radius: var(--radius-sm);
    padding: var(--sp-4);
    margin-bottom: var(--sp-5);
  }

  .chart-header {
    display: flex; align-items: baseline; justify-content: space-between;
    gap: var(--sp-3); flex-wrap: wrap;
    margin-bottom: var(--sp-3); padding-bottom: var(--sp-2);
    border-bottom: 1px solid var(--c-border);
  }
  .chart-title {
    font-size: var(--text-sm); font-weight: 600; color: var(--c-text);
  }
  .chart-legend {
    display: flex; gap: var(--sp-3); flex-wrap: wrap;
    font-size: var(--text-xs); color: var(--c-text-3);
  }
  .legend-item { display: inline-flex; align-items: center; gap: 4px; }
  .legend-dot { width: 8px; height: 8px; border-radius: 2px; flex-shrink: 0; }

  .chart-body {
    display: flex; flex-direction: column; gap: 2px;
  }

  .chart-row {
    display: flex; align-items: center; gap: var(--sp-3);
    padding: 5px var(--sp-2);
    cursor: pointer;
    border-radius: var(--radius-xs);
    transition: background var(--transition);
  }
  .chart-row:hover {
    background: var(--c-surface-inset);
  }

  .row-label {
    width: 140px; min-width: 140px;
    font-size: var(--text-xs); font-weight: 500;
    color: var(--c-text-2);
    text-align: right;
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .chart-row:hover .row-label { color: var(--c-text); }

  .row-bar {
    flex: 1;
    display: flex; align-items: center; gap: var(--sp-2);
    min-width: 0;
  }

  .bar-track {
    flex: 1;
    display: flex;
    height: 20px;
    border-radius: 3px;
    overflow: hidden;
    background: var(--c-surface-inset);
  }

  .bar-seg {
    height: 100%;
    transition: width 0.3s ease;
    min-width: 0;
  }
  .bar-seg:first-child { border-radius: 3px 0 0 3px; }
  .bar-seg:last-child { border-radius: 0 3px 3px 0; }
  .bar-seg:only-child { border-radius: 3px; }
  .bar-seg-single { border-radius: 3px; }

  .row-count {
    font-size: var(--text-xs); font-weight: 600; color: var(--c-text-2);
    min-width: 28px; white-space: nowrap;
  }

  /* ── Tooltip (fixed position, outside container) ── */
  .chart-tooltip {
    position: fixed;
    pointer-events: none;
    background: var(--c-surface);
    border: 1px solid var(--c-border);
    border-radius: var(--radius-sm);
    padding: 10px 14px;
    font-size: var(--text-xs);
    box-shadow: var(--shadow-md);
    z-index: 1000;
    white-space: nowrap;
    min-width: 160px;
  }
  .tt-header {
    display: flex; align-items: baseline; justify-content: space-between;
    gap: var(--sp-3); margin-bottom: 6px;
    padding-bottom: 4px; border-bottom: 1px solid var(--c-border);
  }
  .tt-title { font-weight: 600; color: var(--c-text); }
  .tt-count { color: var(--c-text-3); font-size: 10px; }
  .tt-segments { display: flex; flex-direction: column; gap: 3px; }
  .tt-seg {
    display: flex; align-items: center; gap: 6px; color: var(--c-text);
  }
  .tt-dot { width: 7px; height: 7px; border-radius: 2px; flex-shrink: 0; }
  .tt-label { flex: 1; }
  .tt-val { font-weight: 600; }
  .tt-footer {
    margin-top: 6px; padding-top: 4px; border-top: 1px solid var(--c-border);
    color: var(--c-text-2); font-size: 11px;
  }

  @media (max-width: 768px) {
    .chart-card { padding: var(--sp-3); }
    .row-label { width: 100px; min-width: 100px; font-size: 10px; }
    .bar-track { height: 16px; }
    .chart-header { flex-direction: column; gap: var(--sp-2); }
  }
</style>
