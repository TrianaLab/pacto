<script>
  import CollapsibleSection from '../CollapsibleSection.svelte';

  let { observed, open = $bindable(false), id = '' } = $props();
</script>

{#if observed}
  <CollapsibleSection title="Observed Runtime" bind:open {id}>
    <div class="card">
      <dl class="kv-grid">
        {#if observed.workloadKind}<dt>Workload kind</dt><dd>{observed.workloadKind}</dd>{/if}
        {#if observed.deploymentStrategy}<dt>Strategy</dt><dd>{observed.deploymentStrategy}</dd>{/if}
        {#if observed.containerImages?.length > 0}<dt>Images</dt><dd>{observed.containerImages.join(', ')}</dd>{/if}
        {#if observed.hasPVC != null}<dt>Has PVC</dt><dd>{observed.hasPVC ? 'Yes' : 'No'}</dd>{/if}
        {#if observed.hasEmptyDir != null}<dt>Has EmptyDir</dt><dd>{observed.hasEmptyDir ? 'Yes' : 'No'}</dd>{/if}
        {#if observed.terminationGracePeriodSeconds != null}<dt>Termination grace</dt><dd>{observed.terminationGracePeriodSeconds}s</dd>{/if}
        {#if observed.healthProbeInitialDelaySeconds != null}<dt>Health probe delay</dt><dd>{observed.healthProbeInitialDelaySeconds}s</dd>{/if}
      </dl>
    </div>
  </CollapsibleSection>
{/if}
