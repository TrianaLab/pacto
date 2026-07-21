<script>
  import GraphCanvas from './GraphCanvas.svelte';

  let {
    graphData = null,
    focusId = null,
    focusNodes = null,
    height = 400,
    onNavigate,
    filterFn = null,
    layout = 'force',
    groups = null,
    showZoom = true,
    showLegend = true,
    showDirectionDepth = false,
    initialDirection = 'down',
  } = $props();

  // Seed the toolbar direction once from the prop; it is user-controlled state
  // thereafter, so capturing only the initial value is intended.
  // svelte-ignore state_referenced_locally
  let direction = $state(initialDirection);
  let depth = $state(1);
  let graphRef = $state(null);

  function setDirection(d) {
    direction = d;
    graphRef?.reset();
  }

  function setDepth(d) {
    depth = Math.max(1, Math.min(6, d));
    graphRef?.reset();
  }

  // Push filter changes imperatively — GraphCanvas ignores filterFn prop changes
  // for re-render (avoids thrashing the D3 layout), so apply it via the instance.
  $effect(() => {
    graphRef?.applyFilter(filterFn ?? null);
  });
</script>

<div class="graph-panel">
  {#if showDirectionDepth}
    <div class="dep-graph-toolbar">
      <div class="seg" role="group" aria-label="Dependency direction">
        <button type="button" class="seg-btn" class:active={direction === 'both'} aria-pressed={direction === 'both'} onclick={() => setDirection('both')}>Both</button>
        <button type="button" class="seg-btn" class:active={direction === 'down'} aria-pressed={direction === 'down'} onclick={() => setDirection('down')}>Depends on</button>
        <button type="button" class="seg-btn" class:active={direction === 'up'} aria-pressed={direction === 'up'} onclick={() => setDirection('up')}>Depended on by</button>
      </div>
      <div class="depth-ctrl">
        <span class="depth-label">Depth</span>
        <button type="button" class="btn btn-sm" aria-label="Less depth" disabled={depth <= 1} onclick={() => setDepth(depth - 1)}>−</button>
        <span class="depth-val" aria-live="polite">{depth}</span>
        <button type="button" class="btn btn-sm" aria-label="More depth" disabled={depth >= 6} onclick={() => setDepth(depth + 1)}>+</button>
      </div>
      <button type="button" class="btn btn-sm btn-ghost" onclick={() => graphRef?.reset()}>Reset</button>
    </div>
  {/if}

  <div class="graph-canvas-wrapper">
    {#if showZoom}
      <div class="graph-controls">
        <button type="button" class="btn btn-sm" onclick={() => graphRef?.zoomIn()} title="Zoom in">+</button>
        <button type="button" class="btn btn-sm" onclick={() => graphRef?.zoomOut()} title="Zoom out">−</button>
        <button type="button" class="btn btn-sm" onclick={() => graphRef?.resetView()} title="Reset view">↻</button>
      </div>
    {/if}

    <GraphCanvas
      bind:this={graphRef}
      {graphData}
      {focusId}
      {focusNodes}
      {layout}
      {groups}
      {direction}
      {depth}
      {height}
      {onNavigate}
      {filterFn}
    />
  </div>

  {#if showLegend}
    <div class="graph-legend">
      <span class="legend-item" data-tip="All contract checks pass"><span class="legend-dot" style="background:var(--c-ok)"></span> Compliant</span>
      <span class="legend-item" data-tip="Some contract checks fail with warnings"><span class="legend-dot" style="background:var(--c-warn)"></span> Warning</span>
      <span class="legend-item" data-tip="The contract has validation errors"><span class="legend-dot" style="background:var(--c-err)"></span> Non-Compliant</span>
      <span class="legend-item" data-tip="Contract status could not be determined"><span class="legend-dot" style="background:var(--c-neutral)"></span> Unknown</span>
      <span class="legend-item" data-tip="Shared contract definition with no deployed workload"><span class="legend-dot" style="background:#60a5fa"></span> Reference</span>
      <span class="legend-sep">|</span>
      <span class="legend-item" data-tip="Non-OCI dependency — not a contract-backed service"><span class="legend-dot" style="background:var(--c-text-3)"></span> External</span>
      <span class="legend-item" data-tip="Registry authentication failed"><span class="legend-dot" style="background:var(--c-err)"></span> Auth required</span>
      <span class="legend-item" data-tip="OCI repo found but no valid semver tags, or registry unreachable"><span class="legend-dot" style="background:var(--c-warn)"></span> Not found / No versions</span>
      <span class="legend-item" data-tip="Background OCI discovery still running"><span class="legend-dot legend-dot-pulse" style="background:var(--c-accent)"></span> Discovering</span>
      <span class="legend-sep">|</span>
      <span class="legend-item" data-tip="On focus: what this service depends on"><span class="legend-line dep"></span> depends on</span>
      <span class="legend-item" data-tip="On focus: what depends on this service (blast radius)"><span class="legend-line dependent"></span> depended on by</span>
    </div>
  {/if}
</div>

<style>
  .graph-panel {
    position: relative;
  }

  .dep-graph-toolbar {
    display: flex;
    align-items: center;
    gap: var(--sp-3);
    flex-wrap: wrap;
    margin-bottom: var(--sp-2);
  }

  .seg {
    display: inline-flex;
    border: 1px solid var(--c-border);
    border-radius: var(--radius-sm);
    overflow: hidden;
  }

  .seg-btn {
    padding: 4px 10px;
    font-size: var(--text-xs);
    background: var(--c-surface);
    color: var(--c-text-3);
    border: 0;
    cursor: pointer;
  }

  .seg-btn.active {
    background: var(--c-accent);
    color: #fff;
  }

  .depth-ctrl {
    display: inline-flex;
    align-items: center;
    gap: var(--sp-2);
  }

  .depth-label {
    font-size: var(--text-xs);
    color: var(--c-text-3);
  }

  .depth-val {
    font-size: var(--text-sm);
    font-weight: 600;
    min-width: 1ch;
    text-align: center;
  }

  .graph-canvas-wrapper {
    position: relative;
  }

  .graph-controls {
    position: absolute;
    top: 12px;
    right: 12px;
    z-index: 10;
    display: flex;
    gap: 6px;
  }

  .graph-legend {
    display: flex;
    align-items: center;
    gap: var(--sp-3);
    flex-wrap: wrap;
    padding: var(--sp-3) var(--sp-3);
    font-size: var(--text-xs);
    color: var(--c-text-3);
  }

  .legend-item {
    display: flex;
    align-items: center;
    gap: 5px;
  }
  /* The legend sits at the bottom of the graph box, which clips overflow (rounded
     corners). Render its tooltips upward, into the graph area, so they aren't cut
     off. Higher specificity than the global [data-tip]::after rule. */
  .graph-legend .legend-item[data-tip]::after {
    top: auto;
    bottom: calc(100% + 6px);
  }

  .legend-dot {
    width: 9px;
    height: 9px;
    border-radius: 50%;
    flex-shrink: 0;
  }

  .legend-dot-pulse {
    animation: legend-pulse 1.6s ease-in-out infinite;
  }

  @keyframes legend-pulse {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.3; }
  }

  .legend-sep {
    color: var(--c-border);
  }

  .legend-line {
    display: inline-block;
    width: 18px;
    height: 0;
  }

  .legend-line.dep {
    border-top: 2.5px solid var(--c-accent);
  }

  .legend-line.dependent {
    border-top: 2.5px solid var(--c-warn);
  }

  @media (max-width: 768px) {
    .graph-legend {
      gap: var(--sp-2);
      font-size: var(--text-xs);
    }
    .legend-sep {
      display: none;
    }
  }
</style>
