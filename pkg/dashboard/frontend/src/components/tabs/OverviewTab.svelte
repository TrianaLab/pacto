<script>
  let { detail: d } = $props();

  function insightClass(severity) {
    return { critical: 'insight-critical', warning: 'insight-warning', info: 'insight-info' }[severity] || 'insight-info';
  }

  function condBadgeClass(status) {
    if (status === 'True') return 'badge-ok';
    if (status === 'False') return 'badge-critical';
    return 'badge-neutral';
  }
</script>

<!-- Insights -->
{#if d.insights?.length}
  <div style="margin-bottom:24px">
    <div class="section-heading">Issues</div>
    {#each d.insights as ins}
      <div class="insight-card {insightClass(ins.severity)}">
        <div class="insight-icon">
          {#if ins.severity === 'critical'}
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>
          {:else if ins.severity === 'warning'}
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/></svg>
          {:else}
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/></svg>
          {/if}
        </div>
        <div class="insight-body">
          <div class="insight-title">{ins.title}</div>
          {#if ins.description}<div class="insight-desc">{ins.description}</div>{/if}
        </div>
      </div>
    {/each}
  </div>
{/if}

<!-- Runtime Probes -->
{#if d.endpoints?.length}
  {@const failing = d.endpoints.filter(e => e.healthy === false)}
  {@const healthy = d.endpoints.filter(e => e.healthy === true)}
  {@const unknown = d.endpoints.filter(e => e.healthy !== true && e.healthy !== false)}
  {@const allEp = [...failing, ...unknown, ...healthy]}
  <div class="card">
    <div class="card-header">
      <div class="section-label">Runtime Probes</div>
      <div>
        {#if failing.length}<span class="pill pill-critical">{failing.length} failing</span>{/if}
        {#if healthy.length}<span class="pill pill-ok">{healthy.length} reachable</span>{/if}
        {#if unknown.length}<span class="pill pill-neutral">{unknown.length} unknown</span>{/if}
      </div>
    </div>
    <div class="table-wrapper">
      <table>
        <thead><tr><th>Status</th><th>Probe</th><th>Interface</th><th>URL</th><th class="hide-narrow">Code</th><th class="hide-narrow">Latency</th><th class="hide-narrow">Error</th></tr></thead>
        <tbody>
          {#each allEp as ep}
            <tr>
              <td>
                {#if ep.healthy === true}<span class="badge badge-ok">reachable</span>
                {:else if ep.healthy === false}<span class="badge badge-critical">failing</span>
                {:else}<span class="badge badge-neutral">unknown</span>
                {/if}
              </td>
              <td>{#if ep.type}<span class="pill pill-dim">{ep.type}</span>{:else}&mdash;{/if}</td>
              <td>{ep.interface || ''}</td>
              <td><code>{ep.url || '\u2014'}</code></td>
              <td class="hide-narrow">{#if ep.statusCode != null}<code>{ep.statusCode}</code>{:else}&mdash;{/if}</td>
              <td class="hide-narrow">{ep.latencyMs != null ? ep.latencyMs + 'ms' : '\u2014'}</td>
              <td class="hide-narrow"><span class="text-dim">{ep.error || ep.message || ''}</span></td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  </div>
{/if}

<!-- Status + Conditions grid -->
<div class="detail-grid">
  <!-- Status card -->
  <div class="card">
    <div class="section-label">Status</div>
    <table>
      <tbody>
      {#if d.version}<tr><td class="text-dim">Version</td><td>{d.version}</td></tr>{/if}
      {#if d.imageRef}<tr><td class="text-dim">Image</td><td><code>{d.imageRef}</code></td></tr>{/if}
      {#if d.checksSummary}
        <tr><td class="text-dim">Checks</td><td><span class="count {d.checksSummary.failed > 0 ? 'count-error' : 'count-zero'}">{d.checksSummary.passed}/{d.checksSummary.total} passed</span></td></tr>
      {/if}
      {#if d.lastReconciledAt}
        <tr><td class="text-dim">Reconciled</td><td><span class="text-dim">{d.lastReconciledAt}</span></td></tr>
      {/if}
      </tbody>
    </table>

    {#if d.resources}
      <div class="section-label" style="margin-top:16px">Resources</div>
      <table>
        <tbody>
        {#if d.resources.serviceExists != null}
          <tr><td class="text-dim">Service</td><td>{#if d.resources.serviceExists}<span class="badge badge-ok">found</span>{:else}<span class="badge badge-critical">not found</span>{/if}</td></tr>
        {/if}
        {#if d.resources.workloadExists != null}
          <tr><td class="text-dim">Workload</td><td>{#if d.resources.workloadExists}<span class="badge badge-ok">found</span>{:else}<span class="badge badge-critical">not found</span>{/if}</td></tr>
        {/if}
        </tbody>
      </table>
    {/if}

    {#if d.ports}
      <div class="section-label" style="margin-top:16px">Ports</div>
      <table>
        <tbody>
        {#if d.ports.expected?.length}<tr><td class="text-dim">Expected</td><td>{#each d.ports.expected as p}<code>{p}</code>{' '}{/each}</td></tr>{/if}
        {#if d.ports.observed?.length}<tr><td class="text-dim">Observed</td><td>{#each d.ports.observed as p}<code>{p}</code>{' '}{/each}</td></tr>{/if}
        {#if d.ports.missing?.length}<tr><td class="text-dim">Missing</td><td>{#each d.ports.missing as p}<span class="count count-error"><code>{p}</code></span>{' '}{/each}</td></tr>{/if}
        {#if d.ports.unexpected?.length}<tr><td class="text-dim">Unexpected</td><td>{#each d.ports.unexpected as p}<span class="count count-warning"><code>{p}</code></span>{' '}{/each}</td></tr>{/if}
        </tbody>
      </table>
    {/if}
  </div>

  <!-- Conditions card -->
  <div class="card">
    <div class="section-label">Conditions</div>
    {#if d.conditions?.length}
      <div class="conditions-grid">
        {#each d.conditions as c}
          <div class="condition-card">
            <div class="condition-type">
              <span class="badge {condBadgeClass(c.status)}">{c.status}</span>
              {c.type}
            </div>
            {#if c.reason || c.lastTransitionAgo}
              <div class="condition-message" style="font-weight:500">
                {c.reason || ''}{c.reason && c.lastTransitionAgo ? ' \u00B7 ' : ''}{c.lastTransitionAgo || ''}
              </div>
            {/if}
            {#if c.message}<div class="condition-message">{c.message}</div>{/if}
          </div>
        {/each}
      </div>
    {:else}
      <div style="color:var(--text-dim);font-size:var(--text-sm)">No conditions reported</div>
    {/if}
  </div>
</div>

<!-- Runtime + Scaling -->
{#if d.runtime || d.scaling}
  <div class="detail-grid">
    {#if d.runtime}
      <div class="card">
        <div class="section-label">Runtime</div>
        <table>
          <tbody>
          {#if d.runtime.workload}<tr><td class="text-dim" style="width:160px">Workload</td><td><span class="badge badge-info">{d.runtime.workload}</span></td></tr>{/if}
          {#if d.runtime.stateType}<tr><td class="text-dim">State</td><td>{d.runtime.stateType}</td></tr>{/if}
          {#if d.runtime.dataCriticality}<tr><td class="text-dim">Data Criticality</td><td><span class="pill {d.runtime.dataCriticality === 'critical' ? 'pill-critical' : d.runtime.dataCriticality === 'high' ? 'pill-warning' : 'pill-dim'}">{d.runtime.dataCriticality}</span></td></tr>{/if}
          {#if d.runtime.upgradeStrategy}<tr><td class="text-dim">Upgrade Strategy</td><td>{d.runtime.upgradeStrategy}</td></tr>{/if}
          {#if d.runtime.healthInterface}<tr><td class="text-dim">Health Check</td><td><code>{d.runtime.healthInterface}</code>{#if d.runtime.healthPath} <span class="text-dim">{d.runtime.healthPath}</span>{/if}</td></tr>{/if}
          {#if d.runtime.metricsInterface}<tr><td class="text-dim">Metrics</td><td><code>{d.runtime.metricsInterface}</code>{#if d.runtime.metricsPath} <span class="text-dim">{d.runtime.metricsPath}</span>{/if}</td></tr>{/if}
          </tbody>
        </table>
      </div>
    {/if}
    {#if d.scaling}
      <div class="card">
        <div class="section-label">Scaling</div>
        <table>
          <tbody>
          {#if d.scaling.replicas != null}<tr><td class="text-dim" style="width:160px">Replicas</td><td><code>{d.scaling.replicas}</code></td></tr>{/if}
          {#if d.scaling.min != null}<tr><td class="text-dim">Min</td><td><code>{d.scaling.min}</code></td></tr>{/if}
          {#if d.scaling.max != null}<tr><td class="text-dim">Max</td><td><code>{d.scaling.max}</code></td></tr>{/if}
          </tbody>
        </table>
      </div>
    {/if}
  </div>
{/if}

<!-- Validation issues -->
{#if d.validation}
  {@const allIssues = (d.validation.errors || []).concat(d.validation.warnings || [])}
  {#if allIssues.length}
    <div class="card">
      <div class="section-label">Validation Issues</div>
      <div class="table-wrapper">
        <table>
          <thead><tr><th>Severity</th><th>Code</th><th>Path</th><th>Message</th></tr></thead>
          <tbody>
            {#each d.validation.errors || [] as e}
              <tr><td><span class="badge badge-critical">error</span></td><td><code>{e.code}</code></td><td><code>{e.path}</code></td><td>{e.message}</td></tr>
            {/each}
            {#each d.validation.warnings || [] as w}
              <tr><td><span class="badge badge-warning">warning</span></td><td><code>{w.code}</code></td><td><code>{w.path}</code></td><td>{w.message}</td></tr>
            {/each}
          </tbody>
        </table>
      </div>
    </div>
  {/if}
{/if}
