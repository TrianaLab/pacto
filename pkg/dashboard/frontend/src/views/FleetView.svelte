<script>
  import { onMount } from 'svelte';
  import { api, ApiError } from '../lib/api.ts';
  import { serviceUrl } from '../lib/router.ts';
  import { ownerKey, completenessClass, completenessLabel } from '../lib/format.ts';
  import { formatDate } from '../lib/dateFormat.ts';
  import StatusBadge from '../components/StatusBadge.svelte';
  import SourceDot from '../components/SourceDot.svelte';
  import EmptyState from '../components/EmptyState.svelte';

  let snapshot = $state(null);
  let statusItems = $state([]);
  let loading = $state(true);
  let error = $state(null);

  async function load() {
    loading = true;
    error = null;
    try {
      // The attention report is best-effort: the snapshot is the primary payload,
      // so a status hiccup must not blank the whole page.
      const [snap, status] = await Promise.all([
        api.fleetSnapshot(),
        api.fleetStatus().catch(() => null),
      ]);
      snapshot = snap;
      statusItems = status?.items || [];
    } catch (e) {
      error = e instanceof ApiError ? e.message : 'Couldn’t load the operational graph.';
    }
    loading = false;
  }

  const completeness = $derived(snapshot?.completeness || '');
  const isPartial = $derived(!!completeness && completeness !== 'complete');
  const limitations = $derived(snapshot?.limitations || []);

  const counts = $derived({
    services: Object.keys(snapshot?.services || {}).length,
    revisions: Object.keys(snapshot?.revisions || {}).length,
    targets: Object.keys(snapshot?.targets || {}).length,
    relationships: (snapshot?.relationships || []).length,
  });

  // Fleet services come from the snapshot's logical-service records, keyed by
  // service key. Each carries its owner, aggregate status and the revision/target
  // keys it references — counted here for the table.
  const rows = $derived(
    Object.values(snapshot?.services || {})
      .map((rec) => ({
        name: rec.name,
        owner: ownerKey(rec.owner) || '(unowned)',
        status: rec.status || '',
        revisionCount: (rec.revisions || []).length,
        targetCount: (rec.targets || []).length,
        sources: rec.sources || [],
      }))
      .sort((a, b) => a.name.localeCompare(b.name)),
  );

  onMount(() => { load(); });
</script>

<div class="page-header">
  <h1>Operational Graph</h1>
  {#if snapshot}
    <span class="badge {completenessClass(completeness)}"><span class="badge-dot"></span>{completenessLabel(completeness)}</span>
    {#if snapshot.generatedAt}<span class="as-of">as of {formatDate(snapshot.generatedAt)}</span>{/if}
  {/if}
</div>

{#if loading}
  <EmptyState loading message="Loading the operational graph…" />
{:else if error}
  <EmptyState error={error} onRetry={load} title="Couldn’t load the operational graph" />
{:else if snapshot}
  <div class="summary-row">
    <div class="metric-tile"><span class="metric-head">Services</span><span class="metric-value">{counts.services}</span></div>
    <div class="metric-tile"><span class="metric-head">Revisions</span><span class="metric-value">{counts.revisions}</span></div>
    <div class="metric-tile"><span class="metric-head">Targets</span><span class="metric-value">{counts.targets}</span></div>
    <div class="metric-tile"><span class="metric-head">Relationships</span><span class="metric-value">{counts.relationships}</span></div>
  </div>

  {#if isPartial}
    <div class="partial-banner" role="status">
      <strong>Partial answer.</strong>
      <span>Some sources were unavailable or stale, so this view is incomplete knowledge — not evidence of absence.</span>
      {#if limitations.length > 0}
        <ul class="limitations">
          {#each limitations as lim}
            <li><code>{lim.code}</code>{#if lim.source} <span class="text-dim">({lim.source})</span>{/if} — {lim.message}</li>
          {/each}
        </ul>
      {/if}
    </div>
  {/if}

  <div class="section">
    <div class="section-title">Services <span class="tab-count">{rows.length}</span></div>
    {#if rows.length === 0}
      <EmptyState title="No services" message="No source produced any logical service record." />
    {:else}
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Service</th>
              <th>Owner</th>
              <th>Status</th>
              <th data-tip="Distinct contract revisions known for this service">Revisions</th>
              <th data-tip="Operational targets running this service">Targets</th>
              <th>Sources</th>
            </tr>
          </thead>
          <tbody>
            {#each rows as row (row.name)}
              <tr>
                <td><a href={serviceUrl(row.name)}>{row.name}</a></td>
                <td>{#if row.owner === '(unowned)'}<span class="text-dim">{row.owner}</span>{:else}{row.owner}{/if}</td>
                <td>{#if row.status}<StatusBadge status={row.status} />{:else}<span class="text-dim">—</span>{/if}</td>
                <td>{row.revisionCount}</td>
                <td>{row.targetCount}</td>
                <td>
                  {#if row.sources.length > 0}
                    <span class="sources">{#each row.sources as src}<span class="source-tag"><SourceDot source={src} />{src}</span>{/each}</span>
                  {:else}
                    <span class="text-dim">—</span>
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </div>

  <div class="section" style="margin-top:var(--sp-6)">
    <div class="section-title">Needs attention <span class="tab-count">{statusItems.length}</span></div>
    {#if statusItems.length === 0}
      <EmptyState title="All clear" message="Nothing in the fleet needs attention right now." />
    {:else}
      <div class="table-wrap">
        <table>
          <thead>
            <tr><th>Kind</th><th>Name</th><th>Code</th><th>Reason</th></tr>
          </thead>
          <tbody>
            {#each statusItems as item}
              <tr>
                <td><span class="pill">{item.kind}</span></td>
                <td>{item.name}</td>
                <td><code>{item.code}</code></td>
                <td class="text-2">{item.reason}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </div>
{/if}

<style>
  .page-header {
    display: flex; align-items: center; gap: var(--sp-3);
    margin-bottom: var(--sp-5); flex-wrap: wrap;
  }
  .as-of { font-size: var(--text-sm); color: var(--c-text-3); }

  .summary-row {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
    gap: var(--sp-3); margin-bottom: var(--sp-4);
  }
  .metric-tile {
    display: flex; flex-direction: column; gap: 3px;
    padding: var(--sp-4);
    border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    background: var(--c-surface);
  }
  .metric-head {
    font-size: var(--text-xs); font-weight: 600; text-transform: uppercase;
    letter-spacing: 0.05em; color: var(--c-text-3);
  }
  .metric-value { font-size: 2rem; font-weight: 700; line-height: 1.1; color: var(--c-text); }

  .partial-banner {
    padding: var(--sp-3) var(--sp-4); margin-bottom: var(--sp-4);
    border: 1px solid var(--c-warn-border); border-radius: var(--radius-sm);
    background: var(--c-warn-bg); color: var(--c-text-2); font-size: var(--text-sm);
  }
  .partial-banner strong { color: var(--c-warn); }
  .limitations { margin: var(--sp-2) 0 0; padding-left: var(--sp-4); display: flex; flex-direction: column; gap: 4px; }
  .limitations code { font-size: var(--text-xs); }

  .sources { display: inline-flex; flex-wrap: wrap; gap: var(--sp-2); }
  .source-tag {
    display: inline-flex; align-items: center; gap: 5px;
    font-size: var(--text-xs); font-weight: 600; text-transform: uppercase; color: var(--c-text-3);
  }
  .text-dim { color: var(--c-text-3); }
  .text-2 { color: var(--c-text-2); }
</style>
