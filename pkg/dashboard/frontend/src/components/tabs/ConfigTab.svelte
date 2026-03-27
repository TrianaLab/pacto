<script>
  import { navigateTo } from '../../lib/stores.js';
  import { extractServiceName } from '../../lib/helpers.js';

  let { config: cfg } = $props();

  function refName(ref) {
    return extractServiceName(ref);
  }
</script>

{#if !cfg}
  <div class="card"><div style="color:var(--text-dim);font-size:var(--text-sm);text-align:center;padding:24px">No configuration declared in contract</div></div>
{:else}
  <div class="card">
    <div class="section-label">Configuration</div>

    {#if cfg.schema}
      <div style="margin-bottom:16px"><span class="text-dim">Schema:</span> <code>{cfg.schema}</code></div>
    {/if}

    {#if cfg.ref}
      <div style="margin-bottom:16px">
        <span class="text-dim">Ref:</span>
        <button type="button" class="dep-link" onclick={() => navigateTo('detail', refName(cfg.ref))}>{refName(cfg.ref)}</button>
        <code class="text-dim" style="font-size:var(--text-xs)">{cfg.ref}</code>
      </div>
    {/if}

    {#if cfg.values?.length}
      <div class="table-wrapper">
        <table>
          <thead><tr><th>Key</th><th>Value</th><th>Type</th></tr></thead>
          <tbody>
            {#each cfg.values as v}
              <tr>
                <td><code>{v.key}</code></td>
                <td>{#if v.value === '(any)'}<span class="text-dim">any</span>{:else}<code>{v.value}</code>{/if}</td>
                <td><span class="pill pill-dim">{v.type}</span></td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {:else if cfg.valueKeys?.length}
      <div class="table-wrapper">
        <table>
          <thead><tr><th>Key</th><th>Value</th><th>Type</th></tr></thead>
          <tbody>
            {#each cfg.valueKeys as key}
              <tr><td><code>{key}</code></td><td><span class="text-dim">-</span></td><td><span class="pill pill-dim">value</span></td></tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}

    {#if cfg.secretKeys?.length}
      <div style={cfg.values?.length ? 'margin-top:16px;border-top:1px solid var(--border);padding-top:12px' : ''}>
        {#if cfg.values?.length}
          <div class="text-dim" style="font-size:var(--text-xs);margin-bottom:8px">Secret Keys</div>
        {/if}
        <div class="table-wrapper">
          <table>
            <thead><tr><th>Key</th><th>Value</th><th>Type</th></tr></thead>
            <tbody>
              {#each cfg.secretKeys as key}
                <tr><td><code>{key}</code></td><td><span class="text-dim">&bull;&bull;&bull;&bull;&bull;&bull;</span></td><td><span class="pill pill-warning">secret</span></td></tr>
              {/each}
            </tbody>
          </table>
        </div>
      </div>
    {/if}

    {#if !cfg.values && !cfg.valueKeys?.length && !cfg.schema && !cfg.ref && !cfg.secretKeys?.length}
      <div style="color:var(--text-dim);font-size:var(--text-sm)">Configuration section is empty</div>
    {/if}
  </div>
{/if}
