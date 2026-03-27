<script>
  import { navigateTo } from '../../lib/stores.js';
  import { extractServiceName } from '../../lib/helpers.js';

  let { policy: pol } = $props();
</script>

{#if !pol}
  <div class="card"><div style="color:var(--text-dim);font-size:var(--text-sm);text-align:center;padding:24px">No policy declared in contract</div></div>
{:else}
  <div class="card">
    <div class="section-label">Policy</div>
    <table>
      <tbody>
      {#if pol.schema}
        <tr><td class="text-dim" style="width:160px">Schema</td><td><span class="badge badge-info">{pol.schema}</span></td></tr>
      {/if}
      {#if pol.ref}
        <tr><td class="text-dim">Reference</td><td>
          <button type="button" class="dep-link" onclick={() => navigateTo('detail', extractServiceName(pol.ref))}>{extractServiceName(pol.ref)}</button>
          <code class="text-dim" style="font-size:var(--text-xs)">{pol.ref}</code>
          {#if pol.content} <span class="badge badge-info" style="margin-left:4px">in-bundle</span>{/if}
        </td></tr>
      {/if}
      </tbody>
    </table>

    {#if pol.values?.length}
      <div class="table-wrapper" style="margin-top:16px">
        <table>
          <thead><tr><th>Key</th><th>Value</th><th>Type</th></tr></thead>
          <tbody>
            {#each pol.values as v}
              <tr>
                <td><code>{v.key}</code></td>
                <td>{#if v.value === '(any)'}<span class="text-dim">any</span>{:else}<code>{v.value}</code>{/if}</td>
                <td><span class="pill pill-dim">{v.type}</span></td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {:else if pol.content}
      <div style="margin-top:8px;border-top:1px solid var(--border)">
        {#if pol.ref}<div class="text-dim" style="font-size:var(--text-xs);padding:8px 12px 4px">{pol.ref}</div>{/if}
        <pre style="margin:0;padding:4px 12px 12px;font-size:var(--text-xs);overflow-x:auto;max-height:500px;overflow-y:auto">{pol.content}</pre>
      </div>
    {/if}
  </div>
{/if}
