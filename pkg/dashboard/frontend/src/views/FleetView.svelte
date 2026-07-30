<script>
  import { onMount } from 'svelte';
  import { api, ApiError } from '../lib/api.ts';
  import { fleetUrl, impactUrl } from '../lib/router.ts';
  import { completenessClass, completenessLabel } from '../lib/format.ts';
  import { formatDate } from '../lib/dateFormat.ts';
  import { buildFleetGraph, layerAvailability, distinctValues } from '../lib/fleetGraph.ts';
  import GraphCanvas from '../GraphCanvas.svelte';
  import StatusBadge from '../components/StatusBadge.svelte';
  import EmptyState from '../components/EmptyState.svelte';

  let { params = {}, refreshTick = 0 } = $props();

  let snapshot = $state(null);
  let statusItems = $state([]);
  let statusError = $state(null); // a status FAILURE is distinct from "all clear"
  let loading = $state(true);
  let error = $state(null);

  // Graph controls — the Operational Graph's perspective, relationship layer and
  // filters. Initialized from the URL (so the view is deep-linkable) and pushed
  // back to it on change (so refresh/reload preserves it).
  let perspective = $state(params.perspective || 'service');
  let layer = $state(params.layer || 'all');
  let filters = $state({
    domain: params.domain || '', scope: params.scope || '', owner: params.owner || '',
    status: params.status || '', source: params.source || '', freshness: params.freshness || '',
  });
  let selected = $state(params.sel || '');

  // Selection detail (bounded lazy load — never ships the whole snapshot).
  let detail = $state(null);
  let detailLoading = $state(false);
  let detailError = $state(null);

  async function load() {
    loading = true;
    error = null;
    try {
      const snap = await api.fleetSnapshot();
      snapshot = snap;
      // The attention report is loaded separately: a status FAILURE must surface
      // as "unavailable", never be silently swallowed into a false "all clear".
      statusError = null;
      try {
        const status = await api.fleetStatus();
        statusItems = status?.items || [];
      } catch (e) {
        statusError = e instanceof ApiError ? e.message : 'The attention report is unavailable.';
        statusItems = [];
      }
    } catch (e) {
      error = e instanceof ApiError ? e.message : 'Couldn’t load the operational graph.';
    }
    loading = false;
  }

  onMount(() => { load(); });

  // section 1.3: global refresh + auto-reload must refresh THIS view — not just onMount.
  // The first effect run records the initial tick (onMount does the first load);
  // every later tick change triggers a reload without resetting the selection.
  let lastTick = -1;
  $effect(() => {
    const t = refreshTick;
    if (lastTick === -1) { lastTick = t; return; }
    if (t !== lastTick) {
      lastTick = t;
      if (!loading) load();
    }
  });

  const avail = $derived(layerAvailability(snapshot));
  const opts = $derived(distinctValues(snapshot));
  // If the selected layer became unavailable (e.g. observed with no source), fall
  // back to 'all' so the graph is never silently blank.
  const effectiveLayer = $derived(
    (layer === 'observed' && !avail.observed) || (layer === 'reconciled' && !avail.reconciled) ? 'all' : layer,
  );
  const graphData = $derived(buildFleetGraph(snapshot, perspective, effectiveLayer, filters));

  const completeness = $derived(snapshot?.completeness || '');
  const isPartial = $derived(!!completeness && completeness !== 'complete');
  const limitations = $derived(snapshot?.limitations || []);
  const sources = $derived(snapshot?.sources || []);
  const degradedSources = $derived(sources.filter((s) => s.status && s.status !== 'available'));

  const counts = $derived({
    services: Object.keys(snapshot?.services || {}).length,
    revisions: Object.keys(snapshot?.revisions || {}).length,
    targets: Object.keys(snapshot?.targets || {}).length,
    relationships: (snapshot?.relationships || []).length,
  });

  // Keep the URL in sync with the controls so the view is deep-linkable and
  // survives reload. Only writes when the hash actually changed (no loop).
  function syncUrl() {
    const url = fleetUrl({
      perspective, layer,
      domain: filters.domain, scope: filters.scope, owner: filters.owner,
      status: filters.status, source: filters.source, freshness: filters.freshness,
      sel: selected,
    });
    if (location.hash !== url) location.hash = url;
  }

  function setPerspective(p) {
    if (p === perspective) return;
    perspective = p;
    selected = ''; detail = null; // keys differ across perspectives
    syncUrl();
  }
  function setLayer(l) { layer = l; syncUrl(); }
  function setFilter(k, v) { filters = { ...filters, [k]: v }; syncUrl(); }
  function clearFilters() {
    filters = { domain: '', scope: '', owner: '', status: '', source: '', freshness: '' };
    syncUrl();
  }

  // section 1.2: selecting a node loads its bounded detail. The node id is a
  // domain-qualified key; a revision key (serviceKey@content) resolves to its
  // service for detail.
  async function selectNode(id) {
    selected = id;
    syncUrl();
    detail = null; detailError = null; detailLoading = true;
    try {
      if (perspective === 'target') {
        detail = { kind: 'target', view: await api.fleetTarget(id) };
      } else {
        const serviceKey = perspective === 'revision' ? id.split('@')[0] : id;
        detail = { kind: 'service', view: await api.fleetService(serviceKey), revision: perspective === 'revision' ? id : '' };
      }
    } catch (e) {
      detailError = e instanceof ApiError ? e.message : 'Couldn’t load detail.';
    }
    detailLoading = false;
  }

  // Load the initial deep-linked selection once the snapshot is present.
  let didInitSel = false;
  $effect(() => {
    if (snapshot && selected && !detail && !detailLoading && !didInitSel) {
      didInitSel = true;
      selectNode(selected);
    }
  });

  const activeFilterCount = $derived(Object.values(filters).filter(Boolean).length);
  const PERSPECTIVES = [
    { id: 'service', label: 'Services' },
    { id: 'revision', label: 'Revisions' },
    { id: 'target', label: 'Targets' },
  ];
  const LAYERS = [
    { id: 'all', label: 'All', enabled: true },
    { id: 'declared', label: 'Declared', enabled: true },
    { id: 'observed', label: 'Observed' },
    { id: 'reconciled', label: 'Reconciled' },
  ];
  function layerEnabled(id) {
    if (id === 'observed') return avail.observed;
    if (id === 'reconciled') return avail.reconciled;
    return true;
  }

  function impactFor() {
    // Launch the impact workflow preconfigured for the selected service: pass its
    // domain-qualified key so the impact page preselects it and defaults to its two
    // most recent revisions.
    const sv = detail?.view?.service;
    if (!sv) return impactUrl();
    return impactUrl({ svc: sv.key });
  }
</script>

<div class="page-header">
  <h1>Operational Graph</h1>
  {#if snapshot}
    <span class="badge {completenessClass(completeness)}" data-testid="completeness"><span class="badge-dot"></span>{completenessLabel(completeness)}</span>
    {#if snapshot.generatedAt}<span class="as-of">as of {formatDate(snapshot.generatedAt)}</span>{/if}
    {#if snapshot.snapshotId}<span class="snap-id" title={snapshot.snapshotId}>snapshot {snapshot.snapshotId.slice(0, 19)}…</span>{/if}
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

  <!-- section 1.3: source states are shown explicitly; unavailable/partial/stale are
       never rendered as "all clear". -->
  {#if sources.length > 0}
    <div class="sources-row" data-testid="source-states">
      {#each sources as src}
        <span class="src-chip src-{src.status || 'available'}" title="{src.kind || ''} — {src.status || 'available'}">
          <span class="src-dot"></span>{src.id}<span class="src-status">{src.status || 'available'}</span>
        </span>
      {/each}
    </div>
  {/if}

  {#if isPartial}
    <div class="partial-banner" role="status" data-testid="partial-banner">
      <strong>Partial answer.</strong>
      <span>
        {#if degradedSources.length > 0}
          {degradedSources.length} source{degradedSources.length === 1 ? ' is' : 's are'} unavailable, partial or stale, so this view is incomplete knowledge — not evidence of absence.
        {:else}
          Some knowledge is incomplete — treat this view as partial, not evidence of absence.
        {/if}
      </span>
      {#if limitations.length > 0}
        <ul class="limitations">
          {#each limitations as lim}
            <li><code>{lim.code}</code>{#if lim.source} <span class="text-dim">({lim.source})</span>{/if} — {lim.message}</li>
          {/each}
        </ul>
      {/if}
    </div>
  {/if}

  <!-- Controls: perspective, relationship layer, filters. -->
  <div class="controls">
    <div class="control-group" role="group" aria-label="Perspective">
      <span class="control-label">Perspective</span>
      {#each PERSPECTIVES as p}
        <button type="button" class="seg" class:active={perspective === p.id} aria-pressed={perspective === p.id} onclick={() => setPerspective(p.id)}>{p.label}</button>
      {/each}
    </div>
    <div class="control-group" role="group" aria-label="Relationship layer">
      <span class="control-label">Layer</span>
      {#each LAYERS as l}
        <button
          type="button"
          class="seg"
          class:active={layer === l.id}
          aria-pressed={layer === l.id}
          disabled={!layerEnabled(l.id)}
          title={layerEnabled(l.id) ? '' : (l.id === 'observed' ? 'No observed relationship source is configured' : 'No reconciled relationships in this snapshot')}
          onclick={() => setLayer(l.id)}
        >{l.label}{#if !layerEnabled(l.id)} <span class="seg-note">·none</span>{/if}</button>
      {/each}
    </div>
  </div>

  <div class="filters" data-testid="filters">
    <select aria-label="Domain" value={filters.domain} onchange={(e) => setFilter('domain', e.currentTarget.value)}>
      <option value="">All domains</option>
      {#each opts.domains as d}<option value={d}>{d}</option>{/each}
    </select>
    <select aria-label="Scope" value={filters.scope} onchange={(e) => setFilter('scope', e.currentTarget.value)}>
      <option value="">All scopes</option>
      {#each opts.scopes as s}<option value={s}>{s}</option>{/each}
    </select>
    <select aria-label="Owner" value={filters.owner} onchange={(e) => setFilter('owner', e.currentTarget.value)}>
      <option value="">All owners</option>
      {#each opts.owners as o}<option value={o}>{o}</option>{/each}
    </select>
    <select aria-label="Status" value={filters.status} onchange={(e) => setFilter('status', e.currentTarget.value)}>
      <option value="">Any status</option>
      {#each opts.statuses as s}<option value={s}>{s}</option>{/each}
    </select>
    <select aria-label="Source" value={filters.source} onchange={(e) => setFilter('source', e.currentTarget.value)}>
      <option value="">Any source</option>
      {#each opts.sources as s}<option value={s}>{s}</option>{/each}
    </select>
    <select aria-label="Freshness" value={filters.freshness} onchange={(e) => setFilter('freshness', e.currentTarget.value)}>
      <option value="">Any freshness</option>
      <option value="fresh">Fresh</option>
      <option value="stale">Stale</option>
    </select>
    {#if activeFilterCount > 0}
      <button type="button" class="clear-filters" onclick={clearFilters}>Clear ({activeFilterCount})</button>
    {/if}
  </div>

  {#if perspective === 'target'}
    <p class="perspective-note" data-testid="target-note">
      Ellipses are deployed instances; rounded boxes are the logical services they depend on.
      An instance links to the dependency <em>service</em>, not to specific peer instances —
      runtime instance-to-instance routing is not observed, so it is never drawn.
    </p>
  {/if}

  <div class="graph-layout">
    <div class="graph-main">
      {#if graphData.nodes.length === 0}
        <EmptyState title="No matching nodes" message="No {perspective} matches the current filters and layer." />
      {:else}
        <GraphCanvas
          {graphData}
          height={520}
          layout="layered"
          onSelect={selectNode}
          onNavigate={selectNode}
          tapToOpen={true}
        />
      {/if}
    </div>

    <aside class="detail-panel" data-testid="detail-panel">
      {#if !selected}
        <div class="detail-empty">Select a node to inspect it.</div>
      {:else if detailLoading}
        <div class="detail-empty">Loading detail…</div>
      {:else if detailError}
        <div class="detail-empty error">{detailError}</div>
      {:else if detail?.kind === 'service' && detail.view?.service}
        {@const sv = detail.view}
        <div class="detail-head">
          <h2>{sv.service.name}</h2>
          {#if sv.service.domain}<span class="chip">domain: {sv.service.domain}</span>{/if}
          {#if sv.service.status}<StatusBadge status={sv.service.status} />{/if}
        </div>
        <div class="detail-key" title={sv.service.key}>key: {sv.service.key}</div>
        <a class="impact-link" href={impactFor()}>Analyze impact →</a>

        <h3>Revisions <span class="count">{(sv.revisions || []).length}</span></h3>
        <ul class="detail-list">
          {#each sv.revisions || [] as rev}
            <li class:sel={detail.revision === rev.key}>
              <code>{rev.version || '—'}</code>
              {#if rev.digest}<span class="mono-dim">{rev.digest.slice(0, 19)}…</span>{/if}
              {#if rev.valid === false}<span class="tag err">invalid</span>{/if}
            </li>
          {/each}
        </ul>

        <h3>Targets <span class="count">{(sv.targets || []).length}</span></h3>
        <ul class="detail-list">
          {#each sv.targets || [] as t}
            <li>
              <span class="mono-dim">{t.scope || ''}/{t.kind || ''}</span> {t.name}
              {#if t.compliance}<StatusBadge status={t.compliance} />{/if}
            </li>
          {/each}
          {#if (sv.targets || []).length === 0}<li class="text-dim">No operational targets.</li>{/if}
        </ul>

        <h3>Dependencies <span class="count">{(sv.dependencies || []).length}</span></h3>
        <!-- section 1.2 edge details: each declared edge with type/provenance/
             required/compatibility, its NAME-RESOLUTION state and, separately, the
             backend RECONCILIATION state (never conflated: resolution is not
             reconciliation). -->
        {#if (sv.dependencies || []).length > 0}
          <table class="edge-table">
            <thead><tr><th>To</th><th>Type</th><th>Prov.</th><th>Req.</th><th>Compat.</th><th>Resolution</th><th>Reconciliation</th></tr></thead>
            <tbody>
              {#each sv.dependencies as rel}
                <tr>
                  <td>{rel.toService || rel.to}</td>
                  <td>{rel.type?.replace('_', ' ')}</td>
                  <td>{rel.provenance || 'declared'}</td>
                  <td>{rel.required ? 'yes' : 'no'}</td>
                  <td>{rel.compatibility || '—'}</td>
                  <td>{rel.resolved ? 'resolved' : 'unresolved'}</td>
                  <td>{rel.reconciliation || '—'}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        {:else}
          <p class="text-dim">No declared dependencies.</p>
        {/if}

        <h3>Dependents <span class="count">{(sv.dependents || []).length}</span></h3>
        <ul class="detail-list">
          {#each sv.dependents || [] as dep}<li><code>{dep}</code></li>{/each}
          {#if (sv.dependents || []).length === 0}<li class="text-dim">No known dependents.</li>{/if}
        </ul>
      {:else if detail?.kind === 'target' && detail.view?.target}
        {@const tv = detail.view}
        <div class="detail-head">
          <h2>{tv.target.name}</h2>
          {#if tv.target.compliance}<StatusBadge status={tv.target.compliance} />{/if}
        </div>
        <div class="detail-key" title={tv.target.key}>key: {tv.target.key}</div>
        <dl class="kv">
          <dt>Service</dt><dd>{tv.target.service}{#if tv.target.domain} <span class="text-dim">({tv.target.domain})</span>{/if}</dd>
          <dt>Scope</dt><dd>{tv.target.scope || '—'} / {tv.target.kind || '—'}</dd>
          <dt>Linked revision</dt><dd><code>{tv.target.contractRevision || '—'}</code></dd>
          <dt>Requested ref</dt><dd class="mono-dim">{tv.target.requestedRef || '—'}</dd>
          <dt>Resolved ref</dt><dd class="mono-dim">{tv.target.resolvedRef || '—'}</dd>
          <dt>Digest</dt><dd class="mono-dim">{tv.target.digest || '—'}</dd>
          <dt>Evidence at</dt><dd>{tv.target.evidenceAt ? formatDate(tv.target.evidenceAt) : '—'}</dd>
          <dt>Reconciled at</dt><dd>{tv.target.reconciledAt ? formatDate(tv.target.reconciledAt) : '—'}</dd>
          <dt>Source</dt><dd>{(tv.target.sources || [tv.target.source]).filter(Boolean).join(', ') || '—'}{#if tv.target.stale} <span class="tag warn">stale</span>{/if}</dd>
          {#if tv.target.coverage}<dt>Coverage</dt><dd>{tv.target.coverage.evaluated}/{tv.target.coverage.required}</dd>{/if}
        </dl>
        {#if (tv.target.findings || []).length > 0}
          <h3>Findings <span class="count">{tv.target.findings.length}</span></h3>
          <ul class="detail-list">
            {#each tv.target.findings as f}<li><span class="tag {f.severity}">{f.severity}</span> {f.message || f.code}</li>{/each}
          </ul>
        {/if}
        {#if (tv.target.limitations || []).length > 0}
          <h3>Limitations</h3>
          <ul class="detail-list">
            {#each tv.target.limitations as lim}<li><code>{lim.code}</code> — {lim.message}</li>{/each}
          </ul>
        {/if}
      {/if}
    </aside>
  </div>

  <div class="section" style="margin-top:var(--sp-6)">
    <div class="section-title">Needs attention <span class="tab-count">{statusItems.length}</span></div>
    {#if statusError}
      <EmptyState error={statusError} onRetry={load} title="Attention report unavailable" message="The attention report could not be loaded — this is NOT 'all clear'." />
    {:else if statusItems.length === 0}
      <EmptyState title="All clear" message="Nothing in the fleet needs attention right now." />
    {:else}
      <div class="table-wrap">
        <table>
          <thead><tr><th>Kind</th><th>Name</th><th>Code</th><th>Reason</th></tr></thead>
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
  .page-header { display: flex; align-items: center; gap: var(--sp-3); margin-bottom: var(--sp-4); flex-wrap: wrap; }
  .as-of, .snap-id { font-size: var(--text-sm); color: var(--c-text-3); }
  .snap-id { font-family: var(--font-mono, monospace); }

  .summary-row { display: grid; grid-template-columns: repeat(auto-fit, minmax(120px, 1fr)); gap: var(--sp-3); margin-bottom: var(--sp-3); }
  .metric-tile { display: flex; flex-direction: column; gap: 3px; padding: var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); }
  .metric-head { font-size: var(--text-xs); font-weight: 600; text-transform: uppercase; letter-spacing: 0.05em; color: var(--c-text-3); }
  .metric-value { font-size: 1.7rem; font-weight: 700; line-height: 1.1; color: var(--c-text); }

  .sources-row { display: flex; flex-wrap: wrap; gap: var(--sp-2); margin-bottom: var(--sp-3); }
  .src-chip { display: inline-flex; align-items: center; gap: 6px; padding: 3px 8px; border-radius: 999px; border: 1px solid var(--c-border); font-size: var(--text-xs); font-weight: 600; }
  .src-dot { width: 8px; height: 8px; border-radius: 50%; background: var(--c-ok, green); }
  .src-status { color: var(--c-text-3); font-weight: 500; text-transform: uppercase; letter-spacing: 0.03em; }
  .src-partial .src-dot, .src-stale .src-dot { background: var(--c-warn, orange); }
  .src-unavailable .src-dot { background: var(--c-err, red); }
  .src-partial, .src-stale { border-color: var(--c-warn-border, orange); }
  .src-unavailable { border-color: var(--c-err-border, red); }

  .perspective-note { margin: 0 0 var(--sp-3); font-size: var(--text-xs); color: var(--c-text-3); max-width: 70ch; }
  .perspective-note em { font-style: italic; color: var(--c-text-2); }
  .partial-banner { padding: var(--sp-3) var(--sp-4); margin-bottom: var(--sp-3); border: 1px solid var(--c-warn-border); border-radius: var(--radius-sm); background: var(--c-warn-bg); color: var(--c-text-2); font-size: var(--text-sm); }
  .partial-banner strong { color: var(--c-warn); }
  .limitations { margin: var(--sp-2) 0 0; padding-left: var(--sp-4); display: flex; flex-direction: column; gap: 4px; }
  .limitations code { font-size: var(--text-xs); }

  .controls { display: flex; flex-wrap: wrap; gap: var(--sp-4); margin-bottom: var(--sp-3); }
  .control-group { display: inline-flex; align-items: center; gap: 4px; }
  .control-label { font-size: var(--text-xs); font-weight: 600; text-transform: uppercase; letter-spacing: 0.05em; color: var(--c-text-3); margin-right: 4px; }
  .seg { padding: 4px 10px; border: 1px solid var(--c-border); background: var(--c-surface); border-radius: var(--radius-sm); font-size: var(--text-sm); cursor: pointer; color: var(--c-text-2); }
  .seg.active { background: var(--c-accent-bg, var(--c-accent)); color: var(--c-accent-fg, white); border-color: var(--c-accent); }
  .seg:disabled { opacity: 0.5; cursor: not-allowed; }
  .seg-note { font-size: var(--text-xs); color: var(--c-text-3); }

  .filters { display: flex; flex-wrap: wrap; gap: var(--sp-2); margin-bottom: var(--sp-3); }
  .filters select { padding: 5px 8px; border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); font-size: var(--text-sm); color: var(--c-text); }
  .clear-filters { padding: 5px 10px; border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); font-size: var(--text-sm); cursor: pointer; }

  .graph-layout { display: grid; grid-template-columns: 1fr 340px; gap: var(--sp-4); }
  .graph-main { min-width: 0; }
  .detail-panel { border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); padding: var(--sp-4); max-height: 620px; overflow: auto; }
  .detail-empty { color: var(--c-text-3); font-size: var(--text-sm); padding: var(--sp-3) 0; }
  .detail-empty.error { color: var(--c-err); }
  .detail-head { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .detail-head h2 { margin: 0; font-size: var(--text-lg); }
  .detail-key { font-family: var(--font-mono, monospace); font-size: var(--text-xs); color: var(--c-text-3); margin: 4px 0 var(--sp-2); word-break: break-all; }
  .impact-link { display: inline-block; margin-bottom: var(--sp-3); font-size: var(--text-sm); font-weight: 600; }
  .detail-panel h3 { font-size: var(--text-sm); text-transform: uppercase; letter-spacing: 0.04em; color: var(--c-text-3); margin: var(--sp-3) 0 var(--sp-2); }
  .count { color: var(--c-text-3); font-weight: 500; }
  .detail-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 4px; font-size: var(--text-sm); }
  .detail-list li.sel { background: var(--c-accent-bg-soft, var(--c-surface-inset)); border-radius: var(--radius-sm); padding: 2px 4px; }
  .chip { font-size: var(--text-xs); padding: 2px 6px; border: 1px solid var(--c-border); border-radius: 999px; color: var(--c-text-3); }
  .mono-dim { font-family: var(--font-mono, monospace); font-size: var(--text-xs); color: var(--c-text-3); }
  .kv { display: grid; grid-template-columns: auto 1fr; gap: 4px var(--sp-3); font-size: var(--text-sm); margin: var(--sp-2) 0; }
  .kv dt { color: var(--c-text-3); font-weight: 600; }
  .kv dd { margin: 0; word-break: break-all; }
  .edge-table { width: 100%; border-collapse: collapse; font-size: var(--text-xs); }
  .edge-table th, .edge-table td { text-align: left; padding: 3px 5px; border-bottom: 1px solid var(--c-border); }
  .tag { font-size: var(--text-xs); padding: 1px 5px; border-radius: var(--radius-sm); font-weight: 600; }
  .tag.err, .tag.error { background: var(--c-err-bg); color: var(--c-err); }
  .tag.warn, .tag.warning { background: var(--c-warn-bg); color: var(--c-warn); }
  .text-dim { color: var(--c-text-3); }
  .text-2 { color: var(--c-text-2); }

  @media (max-width: 900px) {
    .graph-layout { grid-template-columns: 1fr; }
  }
</style>
