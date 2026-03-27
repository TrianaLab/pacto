<script>
  import { methodBadgeClass } from '../../lib/helpers.js';

  let { interfaces: ifaces = [] } = $props();
</script>

{#if !ifaces.length}
  <div class="card">
    <div style="color:var(--text-dim);font-size:var(--text-sm);text-align:center;padding:24px">No interfaces declared in contract</div>
  </div>
{:else}
  <!-- Summary table -->
  <div class="card">
    <div class="section-label">Declared Interfaces</div>
    <div class="table-wrapper">
      <table>
        <thead><tr><th>Name</th><th>Type</th><th>Port</th><th>Visibility</th><th class="hide-narrow">Contract File</th></tr></thead>
        <tbody>
          {#each ifaces as f}
            <tr>
              <td><strong>{f.name}</strong></td>
              <td><span class="badge badge-info">{f.type || 'http'}</span></td>
              <td><code>{f.port != null ? f.port : '-'}</code></td>
              <td>
                {#if f.visibility}
                  <span class="pill {f.visibility === 'public' ? 'pill-warning' : 'pill-dim'}">{f.visibility}</span>
                {:else}
                  <span class="text-dim">-</span>
                {/if}
              </td>
              <td class="hide-narrow">
                {#if f.contractFile}<code>{f.contractFile}</code>{:else}<span class="text-dim">-</span>{/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  </div>

  <!-- Per-interface detail cards -->
  {#each ifaces as f}
    <div class="card">
      <div class="card-header">
        <div class="section-label">{f.name}</div>
        <div>
          <span class="badge badge-info">{f.type || 'http'}</span>
          {#if f.visibility}
            <span class="pill {f.visibility === 'public' ? 'pill-warning' : 'pill-dim'}" style="margin-left:6px">{f.visibility}</span>
          {/if}
        </div>
      </div>
      <table>
        <tbody>
        <tr><td class="text-dim" style="width:120px">Port</td><td><code>{f.port != null ? f.port : '-'}</code></td></tr>
        {#if f.contractFile}
          <tr><td class="text-dim">Contract File</td><td><code>{f.contractFile}</code></td></tr>
        {/if}
        </tbody>
      </table>

      {#if f.endpoints?.length}
        <div style="margin-top:8px;border-top:1px solid var(--border);padding:8px 0 0">
          <div class="text-dim" style="font-size:var(--text-xs);padding:0 12px 4px">{f.contractFile || ''} &mdash; {f.endpoints.length} endpoint{f.endpoints.length !== 1 ? 's' : ''}</div>
          <div class="table-wrapper">
            <table>
              <thead><tr><th style="width:80px">Method</th><th>Path</th><th class="hide-narrow">Summary</th></tr></thead>
              <tbody>
                {#each f.endpoints as ep}
                  {@const meth = (ep.method || '').toUpperCase()}
                  <tr>
                    <td><span class="badge {methodBadgeClass(meth)}" style="font-size:10px;font-family:var(--font-mono)">{meth}</span></td>
                    <td><code style="font-size:var(--text-xs)">{ep.path}</code></td>
                    <td class="hide-narrow"><span class="text-dim" style="font-size:var(--text-xs)">{ep.summary || ''}</span></td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        </div>
      {:else if f.contractContent}
        <div style="margin-top:8px;border-top:1px solid var(--border)">
          <div class="text-dim" style="font-size:var(--text-xs);padding:8px 12px 4px">{f.contractFile || ''}</div>
          <pre style="margin:0;padding:4px 12px 12px;font-size:var(--text-xs);overflow-x:auto;max-height:400px;overflow-y:auto">{f.contractContent}</pre>
        </div>
      {/if}
    </div>
  {/each}
{/if}
