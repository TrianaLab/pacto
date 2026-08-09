<script>
  import { onMount, onDestroy } from 'svelte';
  import { api, ApiError } from '../lib/api.ts';
  import { classificationClass, completenessClass, completenessLabel } from '../lib/format.ts';
  import { formatDate } from '../lib/dateFormat.ts';
  import { fleetImpactUrl } from '../lib/router.ts';
  import EntityLink from '../components/EntityLink.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import PreviewSection from '../components/PreviewSection.svelte';
  import EntityRefList from '../components/EntityRefList.svelte';
  import LimitationsList from '../components/LimitationsList.svelte';

  // The Product Impact workspace (requirement A1). The product journey is:
  //   canonical ServiceKey -> bounded product service/revision data -> canonical
  //   RevisionKeys -> api.fleetImpactByIdentity(POST) -> ProductImpact.
  // It NEVER loads the raw FleetSnapshot and NEVER calls the legacy GET /api/fleet/impact:
  // the raw snapshot is a low-level/debug contract, not the product Impact contract.
  // The revision selectors are populated from the product service EntityDetail preview
  // and, when that preview truncates, from the bounded/pageable product entities API
  // scoped to the service -- so a truncated preview is never treated as the complete
  // revision universe.
  let { params = {} } = $props();
  const serviceKey = $derived(params.svc || '');

  const CONSUMER_PAGE = 100;
  // The revision selectors are populated for a two-way (old -> new) comparison, so they
  // are bounded to the most recent revisions rather than materializing an arbitrarily
  // large revision universe in the browser (requirement L1). When more exist, the
  // selector self-describes as incomplete instead of silently claiming completeness.
  const MAX_SELECTOR_REVISIONS = 500;
  const CONFIDENCE_EXPLAIN = {
    contractual: 'Declared dependency with a usable compatibility range.',
    declared: 'Declared dependency, but no usable compatibility range.',
    observed: 'Runtime use observed in a window.',
    corroborated: 'Declared and observed evidence agree.',
    inferred: 'Transitive effect reached through another affected service.',
    unknown: 'Required evidence is incomplete or stale.',
  };

  // Service + revision universe (product API only).
  let serviceDetail = $state(null);
  let revisions = $state([]);
  let loadingRevs = $state(true);
  let loadError = $state(null);
  let snapshotId = $state('');
  let revGen = 0;
  // revisionsComplete is false when the revision universe is larger than the selector's
  // bound (more revisions exist than are listed), so the UI can say so honestly.
  let revisionsComplete = $state(true);

  // Selection.
  let fromRevKey = $state('');
  let toRevKey = $state('');
  let includeObserved = $state(params.observed === '1');
  let observedAvailable = $state(false);

  // Analysis (the POST).
  let analyzing = $state(false);
  let analyzeError = $state(null);
  let staleSnapshot = $state(false);
  let result = $state(null);
  let consumerOffset = $state(0);
  let analyzeGen = 0;

  // Service picker (only when the route carries no service key): search-first, so a
  // service beyond the first page of results is still discoverable (requirement L2).
  let serviceQuery = $state('');
  let serviceResults = $state([]);
  let serviceTotal = $state(0);
  let serviceSearching = $state(false);
  let serviceSearchError = $state(null);
  let serviceSearchSeq = 0;

  function verdictClass(v) {
    if (v === 'compatible') return 'badge-ok';
    if (v === 'incompatible') return 'badge-err';
    return 'badge-neutral';
  }

  // pageRecentRevisions pages the service's revisions through the bounded product
  // entities API (scoped by canonical ServiceKey -- never the FleetSnapshot), stopping
  // at MAX_SELECTOR_REVISIONS so the browser never materializes an arbitrarily large
  // universe just to populate two <select>s. It reports `complete` truthfully: true
  // only when the API's own paging reached the end, false when the selector bound (or
  // the hard page bound) was hit while more remained (requirement L1).
  async function pageRecentRevisions(key) {
    const all = [];
    let offset = 0;
    let complete = false;
    for (let i = 0; i < 100; i++) { // hard page bound; the API is itself bounded per page
      const page = await api.fleetEntities({ kinds: ['revision'], service: key, limit: 200, offset });
      all.push(...(page.entities ?? []));
      if (page.nextOffset == null) { complete = true; break; }
      offset = page.nextOffset;
      if (all.length >= MAX_SELECTOR_REVISIONS) break; // more exist; stay honestly incomplete
    }
    return { items: all, complete };
  }

  async function loadRevisions(key) {
    const gen = ++revGen;
    loadingRevs = true;
    loadError = null;
    result = null;
    try {
      const detail = await api.fleetEntityDetail('service', key);
      let refs = (detail.service?.revisions?.items ?? []).slice();
      let complete = !detail.service?.revisions?.truncated; // the preview was the full set
      if (detail.service?.revisions?.truncated) {
        const paged = await pageRecentRevisions(key); // canonical, bounded per page + overall
        refs = paged.items;
        complete = paged.complete;
      }
      if (gen !== revGen) return; // a newer service superseded this load
      revisionsComplete = complete;
      // ponytail: the selector shows revisions newest-first via numeric-aware label
      // collation (1.9.0 < 1.10.0) with the immutable key as a tie-break. This is a
      // display nicety only -- the canonical RevisionKey is what is analyzed, so
      // ordering is never a correctness dependency (unlike the backend sibling order).
      refs.sort((a, b) =>
        String(b.label ?? '').localeCompare(String(a.label ?? ''), undefined, { numeric: true }) ||
        String(a.key).localeCompare(String(b.key)));
      serviceDetail = detail;
      snapshotId = detail.meta?.snapshotId || '';
      revisions = refs;
      // Default to the most recent change (second-newest -> newest); a single revision
      // preselects only the "new" side. The user can pick any pair.
      if (refs.length >= 2) { fromRevKey = refs[1].key; toRevKey = refs[0].key; }
      else if (refs.length === 1) { fromRevKey = ''; toRevKey = refs[0].key; }
      else { fromRevKey = ''; toRevKey = ''; }
    } catch (e) {
      if (gen !== revGen) return;
      loadError = e;
    } finally {
      if (gen === revGen) loadingRevs = false;
    }
  }

  async function runServiceSearch() {
    const q = serviceQuery.trim();
    const my = ++serviceSearchSeq;
    if (!q) { serviceResults = []; serviceTotal = 0; serviceSearching = false; serviceSearchError = null; return; }
    serviceSearching = true;
    serviceSearchError = null;
    try {
      const l = await api.fleetEntities({ kinds: ['service'], text: q, limit: 20 });
      if (my !== serviceSearchSeq) return; // a newer query supersedes this response
      serviceResults = l.entities ?? [];
      serviceTotal = l.total ?? serviceResults.length;
    } catch (e) {
      if (my !== serviceSearchSeq) return;
      serviceResults = []; serviceTotal = 0; serviceSearchError = e; // never rendered as "no matches"
    } finally {
      if (my === serviceSearchSeq) serviceSearching = false;
    }
  }

  $effect(() => {
    const key = serviceKey;
    if (key) loadRevisions(key);
  });
  onDestroy(() => { serviceSearchSeq++; });

  onMount(async () => {
    // The include-observed control is a placebo unless an observation source exists;
    // capabilities.observed reports it (the demo's embedded traces, a runtime source).
    try { const c = await api.capabilities(); observedAvailable = !!c?.observed; }
    catch { observedAvailable = false; }
    if (!observedAvailable) includeObserved = false;
  });

  async function analyze(offset = 0) {
    if (!serviceKey || !fromRevKey || !toRevKey) return;
    const gen = ++analyzeGen;
    analyzing = true;
    analyzeError = null;
    staleSnapshot = false;
    try {
      const res = await api.fleetImpactByIdentity({
        snapshotId: snapshotId || undefined,
        serviceKey,
        fromRevisionKey: fromRevKey,
        toRevisionKey: toRevKey,
        includeObserved,
        limit: CONSUMER_PAGE,
        offset,
      });
      if (gen !== analyzeGen) return;
      result = res;
      consumerOffset = offset;
    } catch (e) {
      if (gen !== analyzeGen) return;
      // A snapshot-id mismatch is a 409: the published snapshot changed, so the honest
      // response is "refetch and retry", never a silently wrong answer.
      if (e instanceof ApiError && e.status === 409) staleSnapshot = true;
      else analyzeError = e instanceof ApiError ? e.message : 'Couldn’t analyze the impact.';
    } finally {
      if (gen === analyzeGen) analyzing = false;
    }
  }

  async function refetchAndAnalyze() {
    await loadRevisions(serviceKey); // picks up the new snapshot id
    staleSnapshot = false;
    analyze(0);
  }

  function pickServiceOption(key) { if (key) location.hash = fleetImpactUrl(key); }

  const consumers = $derived(result?.consumers ?? { items: [], total: 0, count: 0, offset: 0 });
  const owners = $derived(result?.owners ?? { items: [], total: 0, count: 0 });
  const activeTargets = $derived(result?.activeTargets ?? { items: [], total: 0, count: 0 });
  const limitations = $derived(result?.limitations ?? { items: [], total: 0, count: 0 });
  const cShownFrom = $derived(consumers.total === 0 ? 0 : (consumers.offset ?? 0) + 1);
  const cShownTo = $derived((consumers.offset ?? 0) + (consumers.count ?? 0));
  const cHasPrev = $derived((consumers.offset ?? 0) > 0);
  const cHasNext = $derived(consumers.nextOffset != null);
  const canAnalyze = $derived(!!serviceKey && !!fromRevKey && !!toRevKey && !analyzing);
</script>

<div class="page-header">
  <h1>Impact</h1>
  <span class="subtitle">Blast radius of an old → new contract change over the current operational graph.</span>
</div>

{#if !serviceKey}
  <!-- No service in the route: search for one (product entities API, search-first so
       any service is discoverable), then navigate to /fleet/impact/:serviceKey. -->
  <div class="picker" data-testid="impact-service-picker">
    <label for="impact-pick">Search for a service to analyze</label>
    <form role="search" onsubmit={(e) => { e.preventDefault(); runServiceSearch(); }}>
      <input id="impact-pick" type="search" bind:value={serviceQuery} oninput={runServiceSearch} placeholder="Search services by name…" />
    </form>
    {#if serviceSearching}
      <p class="text-dim" role="status">Searching…</p>
    {:else if serviceSearchError}
      <div class="partial-banner" role="alert" data-testid="impact-picker-error">Search failed: {serviceSearchError instanceof ApiError ? serviceSearchError.message : 'service search is unavailable'}</div>
    {:else if serviceResults.length}
      <ul class="picker-results" data-testid="impact-picker-results">
        {#each serviceResults as s (s.key)}
          <li><button type="button" onclick={() => pickServiceOption(s.key)}>{s.label}{s.domain ? ` (${s.domain})` : ''}</button></li>
        {/each}
      </ul>
      {#if serviceTotal > serviceResults.length}<p class="text-dim" data-testid="impact-picker-truncated">Showing {serviceResults.length} of {serviceTotal}. Refine your search to narrow it.</p>{/if}
    {:else if serviceQuery.trim()}
      <p class="text-dim" data-testid="impact-picker-empty">No services match "{serviceQuery}".</p>
    {/if}
  </div>
{:else if loadingRevs}
  <EmptyState loading message="Loading service revisions…" />
{:else if loadError}
  <EmptyState error title="Couldn’t load this service" message={loadError instanceof ApiError ? loadError.message : String(loadError)} onRetry={() => loadRevisions(serviceKey)} />
{:else}
  <form class="impact-form" onsubmit={(e) => { e.preventDefault(); analyze(0); }}>
    <div class="svc-line">
      <span class="svc-k">Service</span>
      {#if serviceDetail?.entity}<EntityLink ref={serviceDetail.entity} showStatus={false} />{:else}<code>{serviceKey}</code>{/if}
    </div>
    <div class="selectors">
      <div class="field">
        <label for="impact-old-rev">Old revision</label>
        <select id="impact-old-rev" value={fromRevKey} onchange={(e) => (fromRevKey = e.currentTarget.value)} disabled={revisions.length === 0}>
          <option value="">Select…</option>
          {#each revisions as r}<option value={r.key}>{r.label}</option>{/each}
        </select>
      </div>
      <div class="field">
        <label for="impact-new-rev">New revision</label>
        <select id="impact-new-rev" value={toRevKey} onchange={(e) => (toRevKey = e.currentTarget.value)} disabled={revisions.length === 0}>
          <option value="">Select…</option>
          {#each revisions as r}<option value={r.key}>{r.label}</option>{/each}
        </select>
      </div>
    </div>
    {#if revisions.length < 2}
      <p class="text-dim">This service has fewer than two known revisions, so there is no old → new change to analyze yet.</p>
    {/if}
    {#if !revisionsComplete}
      <p class="text-dim" data-testid="impact-revisions-incomplete">This service has many revisions; only the most recent are listed here. To analyze an older revision, open it from its revision page.</p>
    {/if}
    <div class="form-actions">
      <label class="check-field" title={observedAvailable ? 'Let observed (runtime) relationships raise consumer confidence' : 'No observed relationship source is configured for this dashboard'}>
        <input type="checkbox" bind:checked={includeObserved} disabled={!observedAvailable} />
        Include observed{#if !observedAvailable} <span class="text-dim">(no observed source)</span>{/if}
      </label>
      <button type="submit" class="btn btn-primary" disabled={!canAnalyze}>
        {analyzing ? 'Analyzing…' : 'Analyze impact'}
      </button>
    </div>
  </form>

  {#if staleSnapshot}
    <div class="partial-banner" role="status">
      <strong>The published snapshot changed.</strong>
      <span>The operational graph was refreshed while you were analyzing, so this result would be stale.</span>
      <button type="button" class="btn" onclick={refetchAndAnalyze}>Refresh and retry</button>
    </div>
  {/if}

  {#if analyzeError}
    <EmptyState error title="Couldn’t analyze the impact" message={analyzeError} onRetry={() => analyze(consumerOffset)} />
  {:else if analyzing}
    <EmptyState loading message="Analyzing the change…" />
  {:else if result}
    <div class="impact-summary">
      <span class="badge {classificationClass(result.classification)}">{result.classification.replace(/_/g, ' ')}</span>
      {#if result.service}<EntityLink ref={result.service} showStatus={false} />{/if}
      {#if result.oldRevision || result.newRevision}
        <span class="text-2">{result.oldRevision?.label || '?'} → {result.newRevision?.label || '?'}</span>
      {/if}
      <span class="badge {completenessClass(result.meta?.completeness)}"><span class="badge-dot"></span>{completenessLabel(result.meta?.completeness)}</span>
      {#if result.meta?.asOf}<span class="text-3">as of {formatDate(result.meta.asOf)}</span>{/if}
      <span class="snap-id" class:match={result.snapshotMatch} title={result.snapshotMatch ? 'Analyzed the currently published snapshot' : result.snapshotId}>
        snapshot {(result.snapshotId || '').slice(0, 15)}…{#if result.snapshotMatch} ✓ current{/if}
      </span>
    </div>

    {#if limitations.count > 0}
      <PreviewSection title="Incomplete evidence" total={limitations.total} count={limitations.count} truncated={limitations.truncated}>
        <LimitationsList items={limitations.items} />
      </PreviewSection>
    {/if}

    <div class="section">
      <div class="section-title">Affected consumers <span class="tab-count">{consumers.total}</span></div>
      {#if (consumers.items?.length ?? 0) === 0}
        <EmptyState title="No affected consumers" message="No service in the operational graph consumes this change." />
      {:else}
        <div class="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Consumer</th>
                <th data-tip="Direct consumers depend on the changed service; transitive ones are reached through others">Reach</th>
                <th data-tip="The path from the consumer to the changed service">Path</th>
                <th>Verdict</th>
                <th data-tip="How strongly the impact is evidenced">Confidence</th>
                <th>Owner</th>
              </tr>
            </thead>
            <tbody>
              {#each consumers.items as c}
                <tr>
                  <td><EntityLink ref={c.service} showStatus={false} /></td>
                  <td>{#if c.direct}<span class="badge badge-info">Direct</span>{:else}<span class="badge badge-neutral">Transitive · depth {c.depth}</span>{/if}</td>
                  <td class="path-cell">
                    {#if (c.path?.length ?? 0) > 0}{c.path.map((p) => p.label).join(' → ')}{#if c.pathTruncated} …{/if}{:else}<span class="text-dim">—</span>{/if}
                  </td>
                  <td><span class="badge {verdictClass(c.compatibilityVerdict)}">{c.compatibilityVerdict}</span></td>
                  <td><span class="pill" title={CONFIDENCE_EXPLAIN[c.confidence] || ''}>{c.confidence}</span></td>
                  <td>{#if c.owner}{c.owner}{:else}<span class="text-dim">—</span>{/if}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
        <nav class="consumer-pager" aria-label="Consumer pages">
          <span class="text-3">Showing {cShownFrom}–{cShownTo} of {consumers.total}</span>
          <div class="pager-btns">
            <button type="button" class="pg" disabled={!cHasPrev} onclick={() => analyze(Math.max(0, (consumers.offset ?? 0) - CONSUMER_PAGE))}>Previous</button>
            <button type="button" class="pg" disabled={!cHasNext} onclick={() => analyze(consumers.nextOffset)}>Next</button>
          </div>
        </nav>
        <details class="confidence-legend">
          <summary>What do the confidence levels mean?</summary>
          <dl>{#each Object.entries(CONFIDENCE_EXPLAIN) as [k, v]}<dt>{k}</dt><dd>{v}</dd>{/each}</dl>
        </details>
      {/if}
    </div>

    <div class="meta-lists">
      <PreviewSection title="Owners" total={owners.total} count={owners.count} truncated={owners.truncated} empty="No owners identified.">
        <EntityRefList items={owners.items} showStatus={false} />
      </PreviewSection>
      <PreviewSection title="Active targets" total={activeTargets.total} count={activeTargets.count} truncated={activeTargets.truncated} empty="No active targets.">
        <EntityRefList items={activeTargets.items} />
      </PreviewSection>
    </div>
  {:else}
    <EmptyState title="Analyze a change" message="Pick two revisions to see the blast radius over the current operational graph." />
  {/if}
{/if}

<style>
  .page-header { display: flex; align-items: baseline; gap: var(--sp-3); margin-bottom: var(--sp-4); flex-wrap: wrap; }
  .subtitle { color: var(--c-text-3); font-size: var(--text-sm); }

  .picker { display: flex; flex-direction: column; gap: var(--sp-2); max-width: 480px; padding: var(--sp-4); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); }
  .picker label { font-size: var(--text-sm); color: var(--c-text-2); }
  .picker input[type="search"] { width: 100%; padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-bg); color: var(--c-text); font: inherit; font-size: var(--text-sm); min-height: var(--touch-min); }
  .picker-results { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .picker-results button { width: 100%; text-align: left; padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-bg); color: var(--c-text); font: inherit; cursor: pointer; }
  .picker-results button:hover { border-color: var(--c-accent); }

  .impact-form { display: flex; flex-direction: column; gap: var(--sp-3); margin-bottom: var(--sp-5); padding: var(--sp-4); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); }
  .svc-line { display: flex; align-items: center; gap: var(--sp-2); }
  .svc-k { font-size: var(--text-xs); color: var(--c-text-3); font-weight: 600; text-transform: uppercase; }
  .selectors { display: grid; grid-template-columns: 1fr 1fr; gap: var(--sp-3); }
  .field { display: flex; flex-direction: column; gap: 6px; }
  .field label { font-size: var(--text-xs); color: var(--c-text-3); font-weight: 600; text-transform: uppercase; }
  .field select { padding: var(--sp-2) var(--sp-3); min-height: var(--touch-min); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-bg); color: var(--c-text); font: inherit; font-size: var(--text-sm); }
  .form-actions { display: flex; align-items: center; justify-content: space-between; gap: var(--sp-3); flex-wrap: wrap; }
  .check-field { display: inline-flex; align-items: center; gap: var(--sp-2); font-size: var(--text-sm); color: var(--c-text-2); }
  .check-field input:disabled { cursor: not-allowed; }

  .impact-summary { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; margin-bottom: var(--sp-4); }
  .snap-id { font-family: var(--font-mono, monospace); font-size: var(--text-xs); color: var(--c-text-3); padding: 2px 6px; border: 1px solid var(--c-border); border-radius: 999px; }
  .snap-id.match { color: var(--c-ok); border-color: var(--c-ok-border, var(--c-ok)); }

  .partial-banner { display: flex; align-items: center; gap: var(--sp-3); flex-wrap: wrap; padding: var(--sp-3) var(--sp-4); margin-bottom: var(--sp-4); border: 1px solid var(--c-warn-border); border-radius: var(--radius-sm); background: var(--c-warn-bg); color: var(--c-text-2); font-size: var(--text-sm); }
  .partial-banner strong { color: var(--c-warn); }

  .section { margin-top: var(--sp-5); }
  .section-title { font-weight: 600; margin-bottom: var(--sp-3); }
  .tab-count { color: var(--c-text-3); font-weight: 400; }
  .path-cell { font-family: var(--font-mono, monospace); font-size: var(--text-xs); }
  .consumer-pager { display: flex; align-items: center; justify-content: space-between; gap: var(--sp-3); flex-wrap: wrap; margin-top: var(--sp-3); }
  .pager-btns { display: flex; gap: var(--sp-2); }
  .pg { padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); color: var(--c-text); font: inherit; font-size: var(--text-sm); cursor: pointer; min-height: var(--touch-min); }
  .pg:disabled { color: var(--c-text-3); opacity: 0.5; cursor: not-allowed; }
  .confidence-legend { margin-top: var(--sp-3); font-size: var(--text-sm); }
  .confidence-legend dl { display: grid; grid-template-columns: auto 1fr; gap: 4px var(--sp-3); margin-top: var(--sp-2); }
  .confidence-legend dt { font-weight: 600; color: var(--c-text-2); }
  .confidence-legend dd { margin: 0; color: var(--c-text-3); }

  .meta-lists { display: grid; grid-template-columns: repeat(auto-fit, minmax(240px, 1fr)); gap: var(--sp-4); margin-top: var(--sp-5); }

  .text-dim { color: var(--c-text-3); }
  .text-2 { color: var(--c-text-2); }
  .text-3 { color: var(--c-text-3); font-size: var(--text-sm); }

  @media (max-width: 768px) {
    .selectors { grid-template-columns: 1fr; }
  }
</style>
