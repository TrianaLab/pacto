<script>
  import CollapsibleSection from '../CollapsibleSection.svelte';
  import GraphCanvas from '../GraphCanvas.svelte';
  import { phaseClass } from '../lib/format.js';
  import { navigate, serviceUrl } from '../lib/router.js';

  let {
    name, dependencies = [], dependents = [], crossRefs = null,
    graphData = null, services = [],
    open = $bindable(true), id = '',
  } = $props();

  let totalCount = $derived((dependencies.length || 0) + (dependents.length || 0));

  function svcExists(svcName) {
    return services.some((s) => s.name === svcName);
  }
</script>

{#if dependencies.length > 0 || dependents.length > 0 || crossRefs}
  <CollapsibleSection title="Dependencies" count={totalCount} bind:open {id}>
    {#if graphData}
      <div class="dep-graph-box">
        <GraphCanvas {graphData} focusId={name} height={300} onNavigate={(n) => navigate('detail', { name: n })} />
      </div>
    {/if}

    {#if dependencies.length > 0}
      <div class="subsection">
        <h3>Depends on</h3>
        <div class="table-wrap">
          <table>
            <thead><tr><th data-tip="Dependency service name">Service</th><th data-tip="OCI or version reference">Ref</th><th data-tip="Is this dependency required?">Required</th><th data-tip="Version compatibility constraint">Compatibility</th></tr></thead>
            <tbody>
              {#each dependencies as dep}
                <tr>
                  <td>
                    {#if svcExists(dep.name)}
                      <a href={serviceUrl(dep.name)}>{dep.name}</a>
                    {:else}
                      {dep.name} <span class="badge badge-neutral">external</span>
                    {/if}
                  </td>
                  <td><code class="text-3">{dep.ref}</code></td>
                  <td>{dep.required ? 'Yes' : 'No'}</td>
                  <td>{dep.compatibility || '—'}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      </div>
    {/if}

    {#if dependents.length > 0}
      <div class="subsection">
        <h3>Depended on by</h3>
        <div class="table-wrap">
          <table>
            <thead><tr><th data-tip="Service that depends on this one">Service</th><th data-tip="Service health phase">Phase</th><th data-tip="Is this a required dependency?">Required</th></tr></thead>
            <tbody>
              {#each dependents as dep}
                <tr>
                  <td><a href={serviceUrl(dep.name)}>{dep.name}</a></td>
                  <td><span class="badge badge-{phaseClass(dep.phase)}"><span class="badge-dot"></span>{dep.phase}</span></td>
                  <td>{dep.required ? 'Yes' : 'No'}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      </div>
    {/if}

    {#if crossRefs?.references?.length > 0 || crossRefs?.referencedBy?.length > 0}
      <div class="subsection">
        <h3>Cross-references</h3>
        {#if crossRefs.references?.length > 0}
          <p class="text-2" style="margin-bottom:8px">References:</p>
          <div class="table-wrap">
            <table>
              <thead><tr><th>Service</th><th>Type</th><th>Phase</th></tr></thead>
              <tbody>
                {#each crossRefs.references as ref}
                  <tr>
                    <td><a href={serviceUrl(ref.name)}>{ref.name}</a></td>
                    <td><span class="pill">{ref.refType}</span></td>
                    <td><span class="badge badge-{phaseClass(ref.phase)}"><span class="badge-dot"></span>{ref.phase || 'Unknown'}</span></td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
        {#if crossRefs.referencedBy?.length > 0}
          <p class="text-2" style="margin: 12px 0 8px">Referenced by:</p>
          <div class="table-wrap">
            <table>
              <thead><tr><th>Service</th><th>Type</th><th>Phase</th></tr></thead>
              <tbody>
                {#each crossRefs.referencedBy as ref}
                  <tr>
                    <td><a href={serviceUrl(ref.name)}>{ref.name}</a></td>
                    <td><span class="pill">{ref.refType}</span></td>
                    <td><span class="badge badge-{phaseClass(ref.phase)}"><span class="badge-dot"></span>{ref.phase || 'Unknown'}</span></td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
      </div>
    {/if}
  </CollapsibleSection>
{/if}

<style>
  .subsection { margin-top: var(--sp-4); }
  .subsection h3 { margin-bottom: var(--sp-2); }
  .dep-graph-box {
    border: 1px solid var(--c-border);
    border-radius: var(--radius-sm);
    margin-bottom: var(--sp-4);
    overflow: hidden;
  }
  .text-2 { color: var(--c-text-2); }
  .text-3 { color: var(--c-text-3); }
</style>
