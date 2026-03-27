<script>
  import { services, dependents, crossRefs, graphData, navigateTo } from '../../lib/stores.js';
  import { extractServiceName } from '../../lib/helpers.js';
  import PhaseBadge from '../PhaseBadge.svelte';
  import ServiceGraph from '../ServiceGraph.svelte';

  let { detail: d } = $props();

  let deps = $derived(d.dependencies || []);
  let depList = $derived($dependents || []);
  let refs = $derived($crossRefs?.references || []);
  let referencedBy = $derived($crossRefs?.referencedBy || []);

  function svcExists(name) {
    return $services.some((s) => s.name === name);
  }

  function findSvc(name) {
    return $services.find((s) => s.name === name);
  }

  let blastRadius = $derived.by(() => {
    const svc = findSvc(d.name);
    return svc?.blastRadius || 0;
  });
</script>

<!-- Service dependency graph -->
<div class="card" style="padding:0;overflow:hidden">
  <div class="card-header" style="padding:20px 20px 0">
    <div class="section-label">Dependency Graph</div>
  </div>
  <ServiceGraph graphData={$graphData} focusId={d.name} />
</div>

<!-- Depends On -->
<div class="card">
  <div class="card-header">
    <div class="section-label">Depends On</div>
    {#if deps.length}<span class="text-dim">{deps.length} dependenc{deps.length > 1 ? 'ies' : 'y'}</span>{/if}
  </div>
  {#if deps.length}
    <div class="table-wrapper">
      <table>
        <thead><tr><th>Service</th><th>Required</th><th class="hide-narrow">Compatibility</th><th>Status</th></tr></thead>
        <tbody>
          {#each deps as dep}
            {@const depName = dep.name || extractServiceName(dep.ref)}
            {@const exists = svcExists(depName)}
            <tr>
              <td>
                {#if exists}
                  <button type="button" class="dep-link" onclick={() => navigateTo('detail', depName)}>{depName}</button>
                {:else}
                  <button type="button" class="dep-link" onclick={() => navigateTo('detail', depName, dep.ref, dep.compatibility || '')}>{depName}</button>
                  <span class="badge badge-neutral">external</span>
                {/if}
                {#if dep.ref !== depName}
                  <br><code class="text-dim" style="font-size:var(--text-xs)">{dep.ref}</code>
                {/if}
              </td>
              <td>
                {#if dep.required}<span class="badge badge-info">required</span>{:else}<span class="badge badge-neutral">optional</span>{/if}
              </td>
              <td class="hide-narrow"><span class="text-dim">{dep.compatibility || '\u2014'}</span></td>
              <td>
                {#if exists}
                  {@const depSvc = findSvc(depName)}
                  {@const depInvalid = depSvc && (depSvc.phase === 'Invalid' || depSvc.phase === 'Degraded')}
                  {#if dep.required && depInvalid}
                    <span class="badge badge-warning">resolved</span>
                    <span class="text-dim" style="color:var(--warning);font-size:11px">Required dependency is {depSvc.phase.toLowerCase()}</span>
                  {:else}
                    <span class="badge badge-ok">resolved</span>
                  {/if}
                {:else}
                  <span class="badge badge-neutral">external</span>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {:else}
    <div style="color:var(--text-dim);font-size:var(--text-sm)">No dependencies declared</div>
  {/if}
</div>

<!-- Dependents -->
<div class="card">
  <div class="card-header">
    <div class="section-label">Dependents</div>
    <div>
      {#if depList.length}
        <span class="pill pill-accent">{depList.length} service{depList.length > 1 ? 's' : ''} depend on this</span>
      {/if}
      {#if blastRadius > depList.length}
        <span class="blast-radius">{blastRadius} total affected</span>
      {/if}
    </div>
  </div>
  {#if depList.length}
    <div class="table-wrapper">
      <table>
        <thead><tr><th>Service</th><th>Status</th><th>Required</th></tr></thead>
        <tbody>
          {#each depList as dep}
            <tr>
              <td>
                {#if svcExists(dep.name)}
                  <button type="button" class="dep-link" onclick={() => navigateTo('detail', dep.name)}>{dep.name}</button>
                {:else}
                  {dep.name}
                {/if}
              </td>
              <td><PhaseBadge phase={dep.phase} /></td>
              <td>{#if dep.required}<span class="badge badge-info">required</span>{:else}<span class="badge badge-neutral">optional</span>{/if}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {:else}
    <div style="color:var(--text-dim);font-size:var(--text-sm)">No services depend on this one</div>
  {/if}
</div>

<!-- Cross-references -->
{#if refs.length}
  <div class="card">
    <div class="card-header">
      <div class="section-label">References</div>
      <span class="text-dim">{refs.length} reference{refs.length > 1 ? 's' : ''}</span>
    </div>
    <div class="table-wrapper">
      <table>
        <thead><tr><th>Contract</th><th>Type</th><th>Status</th><th class="hide-narrow">Reference</th></tr></thead>
        <tbody>
          {#each refs as ref}
            <tr>
              <td>
                <button type="button" class="dep-link" onclick={() => navigateTo('detail', ref.name)}>{ref.name}</button>
                {#if !svcExists(ref.name)} <span class="badge badge-neutral">external</span>{/if}
              </td>
              <td><span class="pill pill-dim">{ref.refType}</span></td>
              <td>
                {#if svcExists(ref.name) && ref.phase}
                  <PhaseBadge phase={ref.phase} />
                {:else}
                  <span class="badge badge-neutral">untracked</span>
                {/if}
              </td>
              <td class="hide-narrow">{#if ref.ref}<code>{ref.ref}</code>{:else}&mdash;{/if}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  </div>
{/if}

{#if referencedBy.length}
  <div class="card">
    <div class="card-header">
      <div class="section-label">Referenced By</div>
      <span class="text-dim">{referencedBy.length} service{referencedBy.length > 1 ? 's' : ''}</span>
    </div>
    <div class="table-wrapper">
      <table>
        <thead><tr><th>Service</th><th>Uses</th><th>Status</th></tr></thead>
        <tbody>
          {#each referencedBy as rb}
            <tr>
              <td><button type="button" class="dep-link" onclick={() => navigateTo('detail', rb.name)}>{rb.name}</button></td>
              <td><span class="pill pill-dim">{rb.refType}</span></td>
              <td><PhaseBadge phase={rb.phase} /></td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  </div>
{/if}
