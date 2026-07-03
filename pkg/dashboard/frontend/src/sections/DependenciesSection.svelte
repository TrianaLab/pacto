<script>
  import CollapsibleSection from '../CollapsibleSection.svelte';
  import GraphCanvas from '../GraphCanvas.svelte';
  import StatusBadge from '../components/StatusBadge.svelte';
  import { statusClass, reasonLabel, reasonTooltip, reasonBadgeClass, shortDigest, driftBadgeClass, driftBadgeLabel } from '../lib/format.ts';
  import { navigate, serviceUrl } from '../lib/router.ts';

  let {
    name, dependencies = [], dependents = [], crossRefs = null,
    graphData = null, services = [], isHistorical = false,
    open = $bindable(true), id = '', source = '',
  } = $props();

  let totalCount = $derived((dependencies?.length || 0) + (dependents?.length || 0));

  let direction = $state('down');
  let depth = $state(2);
  let graphRef = $state(null);

  function setDirection(d) { direction = d; graphRef?.resetExpand(); }
  function setDepth(d) { depth = Math.max(1, Math.min(6, d)); graphRef?.resetExpand(); }

  function svcExists(svcName) {
    return services.some((s) => s.name === svcName);
  }

  /** Look up the resolution reason for a dependency from graph nodes. */
  function depReason(depName) {
    if (!graphData?.nodes) return undefined;
    const node = graphData.nodes.find((n) => n.id === depName || n.serviceName === depName);
    return node?.status === 'external' ? node.reason : undefined;
  }
</script>

{#if dependencies?.length > 0 || dependents?.length > 0 || crossRefs}
  <CollapsibleSection title="Dependencies" count={totalCount} bind:open {id} {source}>
    {#if graphData}
      <div class="dep-graph-toolbar">
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
        <button type="button" class="btn btn-sm btn-ghost" onclick={() => graphRef?.resetExpand()}>Reset</button>
      </div>
      <div class="dep-graph-box">
        <GraphCanvas
          bind:this={graphRef}
          {graphData}
          focusId={name}
          layout="layered"
          {direction}
          {depth}
          height={420}
          onNavigate={(n) => navigate('detail', { name: n })}
        />
      </div>
    {/if}

    {#if dependencies?.length > 0}
      <div class="subsection">
        <h3>Depends on</h3>
        <div class="table-wrap">
          <table class="deps-table deps-table--depends">
            <colgroup>
              <col class="dt-service" />
              <col class="dt-ref" />
              <col class="dt-required" />
              <col class="dt-compat" />
              <col class="dt-pinned" />
            </colgroup>
            <thead><tr><th data-tip="Dependency service name">Service</th><th data-tip="OCI or version reference">Ref</th><th data-tip="Is this dependency required?">Required</th><th data-tip="Version compatibility constraint">Compatibility</th><th data-tip="Pinned version and digest from pacto.lock">Pinned</th></tr></thead>
            <tbody>
              {#each dependencies as dep}
                {@const reason = svcExists(dep.name) ? undefined : depReason(dep.name)}
                <tr>
                  <td>
                    {#if svcExists(dep.name)}
                      <a href={serviceUrl(dep.name)}>{dep.name}</a>
                    {:else}
                      {dep.name} <span class="badge {reasonBadgeClass(reason)}" data-tip={reasonTooltip(reason)}>{reasonLabel(reason)}</span>
                    {/if}
                  </td>
                  <td><code class="text-3">{dep.ref}</code></td>
                  <td>{dep.required ? 'Yes' : 'No'}</td>
                  <td>{dep.compatibility || '—'}</td>
                  <td>
                    {#if dep.lockedVersion || dep.lockedDigest}
                      <div class="lock-cell">
                        <span class="lock-glyph" data-tip="Pinned by pacto.lock">📌</span>
                        {#if dep.lockedVersion}
                          <code class="text-3">{dep.lockedVersion}</code>
                        {/if}
                        {#if dep.lockedDigest}
                          <code class="text-3" data-tip={dep.lockedDigest}>@{shortDigest(dep.lockedDigest)}</code>
                        {/if}
                        {#if dep.driftStatus === 'drift'}
                          <span class="badge {driftBadgeClass('drift')}" data-tip="Resolved version differs from locked version">{driftBadgeLabel('drift')}</span>
                        {/if}
                      </div>
                    {:else}
                      —
                    {/if}
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      </div>
    {/if}

    {#if dependents?.length > 0}
      <div class="subsection">
        <h3>Depended on by {#if isHistorical}<span class="current-badge" data-tip="Reflects the current dependency graph, not the selected historical version">current</span>{/if}</h3>
        <div class="table-wrap">
          <table class="deps-table deps-table--triple">
            <colgroup>
              <col class="dt3-service" />
              <col class="dt3-status" />
              <col class="dt3-required" />
            </colgroup>
            <thead><tr><th data-tip="Service that depends on this one">Service</th><th data-tip="Contract compliance status">Status</th><th data-tip="Is this a required dependency?">Required</th></tr></thead>
            <tbody>
              {#each dependents as dep}
                <tr>
                  <td><a href={serviceUrl(dep.name)}>{dep.name}</a></td>
                  <td><StatusBadge status={dep.contractStatus} /></td>
                  <td>{dep.required ? 'Yes' : 'No'}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      </div>
    {/if}

    {#if crossRefs?.references?.length > 0}
      <div class="subsection">
        <h3>References {#if isHistorical}<span class="current-badge" data-tip="Reflects current cross-references, not the selected historical version">current</span>{/if}</h3>
        <div class="table-wrap">
          <table class="deps-table deps-table--triple">
            <colgroup>
              <col class="dt3-service" />
              <col class="dt3-status" />
              <col class="dt3-required" />
            </colgroup>
            <thead><tr><th>Service</th><th>Type</th><th>Status</th></tr></thead>
            <tbody>
              {#each crossRefs.references as ref}
                <tr>
                  <td><a href={serviceUrl(ref.name)}>{ref.name}</a></td>
                  <td><span class="pill">{ref.refType}</span></td>
                  <td><span class="badge badge-{statusClass(ref.contractStatus)}"><span class="badge-dot"></span>{ref.contractStatus || 'Unknown'}</span></td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      </div>
    {/if}

    {#if crossRefs?.referencedBy?.length > 0}
      <div class="subsection">
        <h3>Referenced by {#if isHistorical}<span class="current-badge" data-tip="Reflects current cross-references, not the selected historical version">current</span>{/if}</h3>
        <div class="table-wrap">
          <table class="deps-table deps-table--triple">
            <colgroup>
              <col class="dt3-service" />
              <col class="dt3-status" />
              <col class="dt3-required" />
            </colgroup>
            <thead><tr><th>Service</th><th>Type</th><th>Status</th></tr></thead>
            <tbody>
              {#each crossRefs.referencedBy as ref}
                <tr>
                  <td><a href={serviceUrl(ref.name)}>{ref.name}</a></td>
                  <td><span class="pill">{ref.refType}</span></td>
                  <td><span class="badge badge-{statusClass(ref.contractStatus)}"><span class="badge-dot"></span>{ref.contractStatus || 'Unknown'}</span></td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      </div>
    {/if}
  </CollapsibleSection>
{/if}

<style>
  .subsection { margin-top: var(--sp-4); }
  .subsection h3 { margin-bottom: var(--sp-2); }
  .current-badge {
    font-size: var(--text-xs); font-weight: 500;
    padding: 1px 6px; border-radius: var(--radius-xs);
    background: var(--c-neutral-bg); color: var(--c-text-3);
    vertical-align: middle;
  }
  .dep-graph-toolbar {
    display: flex; align-items: center; gap: var(--sp-3); flex-wrap: wrap;
    margin-bottom: var(--sp-2);
  }
  .seg { display: inline-flex; border: 1px solid var(--c-border); border-radius: var(--radius-sm); overflow: hidden; }
  .seg-btn {
    padding: 4px 10px; font-size: var(--text-xs); background: var(--c-surface);
    color: var(--c-text-3); border: 0; cursor: pointer;
  }
  .seg-btn.active { background: var(--c-accent); color: #fff; }
  .depth-ctrl { display: inline-flex; align-items: center; gap: var(--sp-2); }
  .depth-label { font-size: var(--text-xs); color: var(--c-text-3); }
  .depth-val { font-size: var(--text-sm); font-weight: 600; min-width: 1ch; text-align: center; }
  .dep-graph-box {
    border: 1px solid var(--c-border);
    border-radius: var(--radius-sm);
    margin-bottom: var(--sp-4);
    overflow: hidden;
  }
  /* Fixed layout so the long <code> Ref / Pinned cells can't over-grow the table
     and flash a spurious horizontal scrollbar. The Service column is left
     flexible to absorb %-rounding; the Pinned cell wraps (it stacks chips). */
  .deps-table { table-layout: fixed; }
  .deps-table th { white-space: normal; }
  /* Cells wrap (the default) rather than nowrap: with overflow:visible (so cell
     tooltips can escape) a nowrap long ref/digest would spill over the next
     column. Short tokens (Yes/No, ^16.0.0) have no break points so they never
     wrap; long refs break via the `td code { word-break: break-all }` rule. */
  .deps-table td { overflow: visible; word-break: break-word; }
  .dt-service { width: auto; }
  .dt-ref { width: 26%; }
  .dt-required { width: 10%; }
  .dt-compat { width: 16%; }
  .dt-pinned { width: 22%; }
  .dt3-service { width: auto; }
  .dt3-status { width: 24%; }
  .dt3-required { width: 18%; }
  .deps-table td code { word-break: break-all; }

  .text-2 { color: var(--c-text-2); }
  .text-3 { color: var(--c-text-3); }
  .lock-cell {
    display: flex;
    align-items: center;
    gap: var(--sp-1);
    flex-wrap: wrap;
  }
  .lock-glyph {
    font-size: var(--text-xs);
    flex-shrink: 0;
  }
</style>
