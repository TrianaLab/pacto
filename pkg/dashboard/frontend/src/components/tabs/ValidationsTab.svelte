<script>
  import { lookupValidation } from '../../lib/helpers.js';

  let { detail: d } = $props();

  let conditions = $derived(d.conditions || []);
  let validation = $derived(d.validation);

  let conditionGroups = $derived.by(() => {
    const groups = {};
    for (const c of conditions) {
      const entry = lookupValidation(c.type);
      const cat = entry.category;
      if (!groups[cat]) groups[cat] = [];
      groups[cat].push({ condition: c, entry });
    }
    return groups;
  });

  const catOrder = ['contract', 'infrastructure', 'networking', 'workload', 'state', 'lifecycle', 'image', 'health', 'other'];

  function condBadgeClass(status) {
    if (status === 'True') return 'badge-ok';
    if (status === 'False') return 'badge-critical';
    return 'badge-neutral';
  }
</script>

<!-- Compliance summary -->
{#if d.compliance}
  <div class="compliance-summary-card">
    <div class="compliance-summary-header">
      <span class="badge {({'OK':'badge-ok','WARNING':'badge-warning','ERROR':'badge-critical'})[d.compliance.status] || 'badge-neutral'}">{d.compliance.status}</span>
      {#if d.compliance.score != null}
        <span class="compliance-score {d.compliance.score < 50 ? 'compliance-score-error' : d.compliance.score < 80 ? 'compliance-score-warning' : 'compliance-score-ok'}">{d.compliance.score}%</span>
      {/if}
    </div>
    {#if d.compliance.summary}
      {@const s = d.compliance.summary}
      <div class="compliance-summary-counts">
        <span>{s.total} checks</span>
        <span class="text-dim">&bull;</span>
        <span style="color:var(--ok)">{s.passed} passed</span>
        {#if s.errors > 0}<span style="color:var(--critical)">{s.errors} errors</span>{/if}
        {#if s.warnings > 0}<span style="color:var(--warning)">{s.warnings} warnings</span>{/if}
      </div>
    {/if}
  </div>
{/if}

<!-- Conditions grouped by category -->
{#if conditions.length}
  {#each catOrder as cat}
    {#if conditionGroups[cat]}
      <div class="card">
        <div class="section-label" style="text-transform:capitalize">{cat}</div>
        <div class="conditions-grid">
          {#each conditionGroups[cat] as { condition: c, entry }}
            {@const sev = c.severity || entry.severity}
            <div class="condition-card">
              <div class="condition-type">
                <span class="badge {condBadgeClass(c.status)}">{c.status}</span>
                {entry.label}
                {#if sev === 'warning'}
                  <span class="pill pill-warning" style="font-size:9px;padding:1px 5px">warn</span>
                {/if}
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
      </div>
    {/if}
  {/each}
{/if}

<!-- Validation issues -->
{#if validation}
  {@const errs = validation.errors || []}
  {@const warns = validation.warnings || []}
  {#if errs.length || warns.length}
    <div class="card">
      <div class="section-label">Contract Validation Issues</div>
      <div class="table-wrapper">
        <table>
          <thead><tr><th>Severity</th><th>Code</th><th>Path</th><th>Message</th></tr></thead>
          <tbody>
            {#each errs as e}
              <tr><td><span class="badge badge-critical">error</span></td><td><code>{e.code}</code></td><td><code>{e.path}</code></td><td>{e.message}</td></tr>
            {/each}
            {#each warns as w}
              <tr><td><span class="badge badge-warning">warning</span></td><td><code>{w.code}</code></td><td><code>{w.path}</code></td><td>{w.message}</td></tr>
            {/each}
          </tbody>
        </table>
      </div>
    </div>
  {/if}
{/if}

{#if !d.compliance && !conditions.length && !(validation?.errors?.length || validation?.warnings?.length)}
  <div class="card">
    <div style="color:var(--text-dim);font-size:var(--text-sm);text-align:center;padding:24px">No validation data available</div>
  </div>
{/if}
