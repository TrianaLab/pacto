<script>
  let { data } = $props();
</script>

{#if data}
  <details class="debug-panel">
    <summary class="debug-summary">Source Diagnostics</summary>
    <div class="debug-content">
      {#if data.diagnostics}
        {@const d = data.diagnostics}
        {#if d.k8s != null}
          <div class="debug-section">
            <h4>Kubernetes</h4>
            <table class="debug-table">
              <tbody>
              <tr><td>Client configured</td><td>{d.k8s?.clientConfigured ? 'Yes' : 'No'}</td></tr>
              {#if d.k8s?.kubeconfigPath}<tr><td>kubeconfig</td><td>{d.k8s.kubeconfigPath}</td></tr>{/if}
              <tr><td>Cluster reachable</td><td>{d.k8s?.clusterReachable ? 'Yes' : 'No'}</td></tr>
              <tr><td>CRD exists</td><td>{d.k8s?.crdExists ? 'Yes' : 'No'}</td></tr>
              {#if d.k8s}<tr><td>Namespace</td><td>{d.k8s.allNamespaces ? 'all namespaces' : d.k8s.namespace || 'default'}</td></tr>{/if}
              {#if d.k8s}<tr><td>Resources found</td><td>{d.k8s.resourceCount || 0}</td></tr>{/if}
              {#if d.k8s?.error}<tr><td>Error</td><td class="text-critical">{d.k8s.error}</td></tr>{/if}
              </tbody>
            </table>
          </div>
        {/if}
        {#if d.cache}
          <div class="debug-section">
            <h4>OCI Cache</h4>
            <table class="debug-table">
              <tbody>
              <tr><td>Cache dir</td><td>{d.cache.cacheDir}</td></tr>
              <tr><td>Exists</td><td>{d.cache.exists ? 'Yes' : 'No'}</td></tr>
              <tr><td>Services</td><td>{d.cache.serviceCount || 0}</td></tr>
              <tr><td>Versions</td><td>{d.cache.versionCount || 0}</td></tr>
              {#if d.cache.error}<tr><td>Error</td><td class="text-critical">{d.cache.error}</td></tr>{/if}
              </tbody>
            </table>
          </div>
        {/if}
        {#if d.oci}
          <div class="debug-section">
            <h4>OCI Registry</h4>
            <table class="debug-table">
              <tbody>
              <tr><td>Store configured</td><td>{d.oci.storeConfigured ? 'Yes' : 'No'}</td></tr>
              {#if d.oci.repos?.length}<tr><td>Repos</td><td>{d.oci.repos.join(', ')}</td></tr>{/if}
              {#if d.oci.error}<tr><td>Error</td><td class="text-critical">{d.oci.error}</td></tr>{/if}
              </tbody>
            </table>
          </div>
        {/if}
        {#if d.local}
          <div class="debug-section">
            <h4>Local</h4>
            <table class="debug-table">
              <tbody>
              <tr><td>Directory</td><td>{d.local.dir}</td></tr>
              <tr><td>pacto.yaml found</td><td>{d.local.pactoYamlFound ? 'Yes' : 'No'}</td></tr>
              {#if d.local.foundIn}<tr><td>Found in</td><td>{d.local.foundIn}</td></tr>{/if}
              {#if d.local.error}<tr><td>Error</td><td class="text-critical">{d.local.error}</td></tr>{/if}
              </tbody>
            </table>
          </div>
        {/if}
      {/if}
      {#if data.live}
        <div class="debug-section">
          <h4>Live API</h4>
          <table class="debug-table">
            <tbody>
            <tr><td>Service count</td><td>{data.live.serviceCount}</td></tr>
            {#if data.live.error}<tr><td>Error</td><td class="text-critical">{data.live.error}</td></tr>{/if}
            </tbody>
          </table>
        </div>
      {/if}
    </div>
  </details>
{/if}
