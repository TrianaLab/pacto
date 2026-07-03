<script>
  import GraphCanvas from './GraphCanvas.svelte';

  // Full-screen dependency graph. Mirrors DocModal's chrome (backdrop, dialog,
  // Escape + close). Renders a large layered GraphCanvas; when `showControls` is
  // set (service detail) it also surfaces the direction/depth controls, bound
  // back to the caller so the inline and full-screen views stay in sync.
  let {
    open = false, graphData = null, focusId = null,
    direction = $bindable('down'), depth = $bindable(2),
    showControls = false, filterFn, onNavigate, onClose = () => {},
  } = $props();

  let graphRef = $state(null);
  // Fill the viewport minus the modal chrome. Recomputed each time it opens.
  let bodyH = $state(600);

  function onKeydown(e) { if (open && e.key === 'Escape') onClose(); }
  function setDirection(d) { direction = d; graphRef?.reset(); }
  function setDepth(d) { depth = Math.max(1, Math.min(6, d)); graphRef?.reset(); }

  $effect(() => {
    if (open) bodyH = Math.max(400, window.innerHeight - 120);
  });
</script>

<svelte:window onkeydown={onKeydown} />

{#if open}
  <!-- Backdrop is presentational; Escape + the close button drive keyboard users. -->
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <div class="graph-modal-backdrop" role="presentation" onclick={onClose}>
    <div class="graph-modal" role="dialog" aria-modal="true" aria-label="Dependency graph" tabindex="-1"
      onclick={(e) => e.stopPropagation()}>
      <div class="graph-modal-header">
        <span class="graph-modal-title">Dependency graph{#if focusId} — {focusId}{/if}</span>
        <div class="modal-tools">
          {#if showControls}
            <div class="seg" role="group" aria-label="Tree direction">
              <button type="button" class="seg-btn" class:active={direction === 'down'} aria-pressed={direction === 'down'} onclick={() => setDirection('down')}>Depends on</button>
              <button type="button" class="seg-btn" class:active={direction === 'up'} aria-pressed={direction === 'up'} onclick={() => setDirection('up')}>Depended on by</button>
            </div>
            <div class="depth-ctrl">
              <span class="depth-label">Depth</span>
              <button type="button" class="btn btn-sm" aria-label="Less depth" disabled={depth <= 1} onclick={() => setDepth(depth - 1)}>−</button>
              <span class="depth-val" aria-live="polite">{depth}</span>
              <button type="button" class="btn btn-sm" aria-label="More depth" disabled={depth >= 6} onclick={() => setDepth(depth + 1)}>+</button>
            </div>
          {/if}
          <button type="button" class="btn btn-sm" onclick={() => graphRef?.zoomIn()} title="Zoom in">+</button>
          <button type="button" class="btn btn-sm" onclick={() => graphRef?.zoomOut()} title="Zoom out">−</button>
          <button type="button" class="btn btn-sm" onclick={() => graphRef?.reset()} title="Reset view">↻</button>
          <button type="button" class="graph-modal-close" onclick={onClose} aria-label="Close full screen">✕</button>
        </div>
      </div>
      <div class="graph-modal-body">
        <GraphCanvas
          bind:this={graphRef}
          {graphData}
          {focusId}
          layout="layered"
          {direction}
          {depth}
          {filterFn}
          maxFitScale={3}
          height={bodyH}
          onNavigate={(n) => { onClose(); onNavigate?.(n); }}
        />
      </div>
    </div>
  </div>
{/if}

<style>
  .graph-modal-backdrop {
    position: fixed;
    inset: 0;
    z-index: 1000;
    background: rgba(0, 0, 0, 0.55);
    display: flex;
    align-items: stretch;
    justify-content: center;
    padding: 16px;
    animation: fadeIn 120ms ease-out;
  }
  .graph-modal {
    display: flex;
    flex-direction: column;
    width: 100%;
    background: var(--c-surface);
    border: 1px solid var(--c-border);
    border-radius: var(--radius-sm);
    box-shadow: 0 12px 48px rgba(0, 0, 0, 0.35);
    overflow: hidden;
  }
  .graph-modal-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--sp-3);
    padding: var(--sp-3) var(--sp-4);
    border-bottom: 1px solid var(--c-border);
    background: var(--c-surface-inset);
    flex-shrink: 0;
    flex-wrap: wrap;
  }
  .graph-modal-title { font-weight: 600; font-size: var(--text-md, 1rem); min-width: 0; }
  .modal-tools { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .seg { display: inline-flex; border: 1px solid var(--c-border); border-radius: var(--radius-sm); overflow: hidden; }
  .seg-btn {
    padding: 4px 10px; font-size: var(--text-xs); background: var(--c-surface);
    color: var(--c-text-3); border: 0; cursor: pointer;
  }
  .seg-btn.active { background: var(--c-accent); color: #fff; }
  .depth-ctrl { display: inline-flex; align-items: center; gap: var(--sp-2); }
  .depth-label { font-size: var(--text-xs); color: var(--c-text-3); }
  .depth-val { font-size: var(--text-sm); font-weight: 600; min-width: 1ch; text-align: center; }
  .graph-modal-close {
    flex-shrink: 0;
    background: none;
    border: 1px solid var(--c-border);
    border-radius: var(--radius-xs);
    color: var(--c-text-2);
    font: inherit;
    line-height: 1;
    padding: 6px 10px;
    cursor: pointer;
  }
  .graph-modal-close:hover { background: var(--c-surface-hover, var(--c-surface-inset)); color: var(--c-text); }
  .graph-modal-body {
    flex: 1 1 auto;
    padding: var(--sp-3);
    overflow: hidden;
  }

  @keyframes fadeIn {
    from { opacity: 0; }
    to { opacity: 1; }
  }
</style>
