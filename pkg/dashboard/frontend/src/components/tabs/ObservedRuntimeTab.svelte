<script>
  let { observed: obs } = $props();
</script>

{#if !obs}
  <div class="card"><div style="color:var(--text-dim);font-size:var(--text-sm);text-align:center;padding:24px">No observed runtime data available. This data is populated by the Kubernetes operator.</div></div>
{:else}
  <div class="card">
    <div class="section-label">Observed Runtime State</div>
    <table>
      <tbody>
      {#if obs.workloadKind}<tr><td class="text-dim" style="width:200px">Workload Kind</td><td><span class="badge badge-info">{obs.workloadKind}</span></td></tr>{/if}
      {#if obs.deploymentStrategy}<tr><td class="text-dim">Deployment Strategy</td><td>{obs.deploymentStrategy}</td></tr>{/if}
      {#if obs.podManagementPolicy}<tr><td class="text-dim">Pod Management Policy</td><td>{obs.podManagementPolicy}</td></tr>{/if}
      {#if obs.terminationGracePeriodSeconds != null}<tr><td class="text-dim">Termination Grace Period</td><td><code>{obs.terminationGracePeriodSeconds}s</code></td></tr>{/if}
      {#if obs.containerImages?.length}
        <tr><td class="text-dim">Container Images</td><td>
          {#each obs.containerImages as img}
            <code style="display:block;margin-bottom:2px">{img}</code>
          {/each}
        </td></tr>
      {/if}
      {#if obs.hasPVC != null}<tr><td class="text-dim">Has PVC</td><td>{#if obs.hasPVC}<span class="badge badge-info">yes</span>{:else}<span class="badge badge-neutral">no</span>{/if}</td></tr>{/if}
      {#if obs.hasEmptyDir != null}<tr><td class="text-dim">Has EmptyDir</td><td>{#if obs.hasEmptyDir}<span class="badge badge-info">yes</span>{:else}<span class="badge badge-neutral">no</span>{/if}</td></tr>{/if}
      {#if obs.healthProbeInitialDelaySeconds != null}<tr><td class="text-dim">Health Probe Initial Delay</td><td><code>{obs.healthProbeInitialDelaySeconds}s</code></td></tr>{/if}
      </tbody>
    </table>
  </div>
{/if}
