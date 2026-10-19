<script>
  import CollapsibleSection from '../CollapsibleSection.svelte';

  let { runtime, scaling, metadata, open = $bindable(true), id = '', source = '' } = $props();

  let hasRuntime = $derived(!!runtime);
  let hasCards = $derived(hasRuntime || !!scaling || (metadata && Object.keys(metadata).length > 0));
</script>

<CollapsibleSection title="Overview" bind:open {id} {source}>
  {#if hasCards}
    <div class="cards-row">
      {#if hasRuntime}
        <div class="card">
          <h3>Runtime</h3>
          <dl class="kv-grid">
            {#if runtime.workload}<dt>Workload</dt><dd>{runtime.workload}</dd>{/if}
            {#if runtime.stateType}<dt>State</dt><dd>{runtime.stateType}</dd>{/if}
            {#if runtime.upgradeStrategy}<dt>Upgrade</dt><dd>{runtime.upgradeStrategy}</dd>{/if}
            {#if runtime.gracefulShutdownSeconds != null}<dt>Graceful shutdown</dt><dd>{runtime.gracefulShutdownSeconds}s</dd>{/if}
            {#if runtime.healthPath}<dt>Health</dt><dd>{runtime.healthInterface}:{runtime.healthPath}</dd>{/if}
            {#if runtime.metricsPath}<dt>Metrics</dt><dd>{runtime.metricsInterface}:{runtime.metricsPath}</dd>{/if}
            {#if runtime.persistenceScope}<dt>Persistence</dt><dd>{runtime.persistenceScope} / {runtime.persistenceDurability || '—'}</dd>{/if}
            {#if runtime.dataCriticality}<dt>Data criticality</dt><dd>{runtime.dataCriticality}</dd>{/if}
          </dl>
        </div>
      {/if}
      {#if scaling}
        <div class="card">
          <h3>Scaling</h3>
          <dl class="kv-grid">
            {#if scaling.replicas != null}<dt>Replicas</dt><dd>{scaling.replicas}</dd>{/if}
            {#if scaling.min != null}<dt>Min</dt><dd>{scaling.min}</dd>{/if}
            {#if scaling.max != null}<dt>Max</dt><dd>{scaling.max}</dd>{/if}
          </dl>
        </div>
      {/if}
      {#if metadata && Object.keys(metadata).length > 0}
        <div class="card">
          <h3>Metadata</h3>
          <dl class="kv-grid">
            {#each Object.entries(metadata) as [k, v]}
              <dt>{k}</dt><dd>{v}</dd>
            {/each}
          </dl>
        </div>
      {/if}
    </div>
  {/if}
</CollapsibleSection>

<style>
  .cards-row { display: flex; flex-wrap: wrap; gap: var(--sp-3); margin-top: var(--sp-3); }
  .cards-row .card { flex: 1; min-width: 240px; }
  .cards-row .card h3 { margin-bottom: var(--sp-2); }

  @media (max-width: 768px) {
    .cards-row { flex-direction: column; }
    .cards-row .card { min-width: 0; }
  }
</style>
