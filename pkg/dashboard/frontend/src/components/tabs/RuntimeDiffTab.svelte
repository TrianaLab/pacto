<script>
  let { rows = [] } = $props();
  let matches = $derived(rows.filter(r => r.status === 'match').length);
  let mismatches = $derived(rows.filter(r => r.status === 'mismatch').length);
  let skipped = $derived(rows.filter(r => r.status !== 'match' && r.status !== 'mismatch').length);
</script>

{#if !rows.length}
  <div class="card"><div style="color:var(--text-dim);font-size:var(--text-sm);text-align:center;padding:24px">No contract vs runtime comparison available</div></div>
{:else}
  <div class="card">
    <div class="card-header">
      <div class="section-label">Contract vs Runtime</div>
      <div>
        {#if matches}<span class="pill pill-ok">{matches} match</span>{/if}
        {#if mismatches}<span class="pill pill-critical">{mismatches} mismatch</span>{/if}
        {#if skipped}<span class="pill pill-dim">{skipped} skipped</span>{/if}
      </div>
    </div>
    <div class="table-wrapper">
      <table>
        <thead><tr><th>Field</th><th>Contract Path</th><th>Declared</th><th>Observed</th><th>Status</th></tr></thead>
        <tbody>
          {#each rows as r}
            <tr>
              <td><strong>{r.field}</strong></td>
              <td><code class="text-dim">{r.contractPath || ''}</code></td>
              <td>{#if r.declaredValue}<code>{r.declaredValue}</code>{:else}<span class="text-dim">&mdash;</span>{/if}</td>
              <td>{#if r.observedValue}<code>{r.observedValue}</code>{:else}<span class="text-dim">&mdash;</span>{/if}</td>
              <td>
                {#if r.status === 'match'}<span class="badge badge-ok">match</span>
                {:else if r.status === 'mismatch'}<span class="badge badge-critical">mismatch</span>
                {:else}<span class="badge badge-neutral">{r.status}</span>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  </div>
{/if}
