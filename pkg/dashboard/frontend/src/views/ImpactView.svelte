<script>
  import { onMount } from 'svelte';
  import { api, ApiError } from '../lib/api.ts';
  import { classificationClass, changeTypeClass, completenessClass, completenessLabel } from '../lib/format.ts';
  import { formatDate } from '../lib/dateFormat.ts';
  import { layerAvailability } from '../lib/fleetGraph.ts';
  import EmptyState from '../components/EmptyState.svelte';

  let { params = {} } = $props();

  // Snapshot backs the revision selectors, the observed-availability gate and the
  // snapshotId comparison — impact analyzes THIS published snapshot (§2.2).
  let snapshot = $state(null);
  let caps = $state(null);
  let snapLoading = $state(true);

  // Revision-selector state (the primary workflow); raw refs are an advanced input.
  let svcKey = $state(params.svc || '');
  let oldRef = $state(params.old || '');
  let newRef = $state(params.new || '');
  let showAdvanced = $state(!!(params.old || params.new) && !params.svc);
  let includeObserved = $state(params.observed === '1');

  let loading = $state(false);
  let error = $state(null);
  let result = $state(null);

  const CONFIDENCE_EXPLAIN = {
    contractual: 'Declared dependency with a usable compatibility range.',
    declared: 'Declared dependency, but no usable compatibility range.',
    observed: 'Runtime use observed in a window.',
    corroborated: 'Declared and observed evidence agree.',
    inferred: 'Transitive effect reached through another affected service.',
    unknown: 'Required evidence is incomplete or stale.',
  };

  function verdictClass(v) {
    if (v === 'compatible') return 'badge-ok';
    if (v === 'incompatible') return 'badge-err';
    return 'badge-neutral';
  }

  // §2.4: the include-observed control is only usable when an observation source
  // exists — either observed edges in the snapshot, or the host declaring an
  // observation source (capabilities.observed, e.g. the demo's embedded traces).
  // Otherwise it is a placebo and is disabled with a reason.
  const observedAvailable = $derived(!!caps?.observed || layerAvailability(snapshot).observed);
  $effect(() => { if (!observedAvailable && includeObserved) includeObserved = false; });

  // Services and their revisions, domain-qualified, for the selectors.
  const services = $derived(
    Object.values(snapshot?.services || {})
      .map((s) => ({ key: s.key, name: s.name, domain: s.domain || '', label: s.domain ? `${s.name} (${s.domain})` : s.name }))
      .sort((a, b) => a.label.localeCompare(b.label)),
  );
  // Sort revisions newest-first by semver so the selectors and the default
  // old→new pair are deterministic regardless of the snapshot's map order.
  function cmpVersionDesc(a, b) {
    const pa = String(a).split('.').map((n) => parseInt(n, 10));
    const pb = String(b).split('.').map((n) => parseInt(n, 10));
    for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
      const x = pa[i] || 0;
      const y = pb[i] || 0;
      if (x !== y) return y - x;
    }
    return String(b).localeCompare(String(a));
  }
  function revsForService(key) {
    const revs = snapshot?.revisions || {};
    const svc = snapshot?.services?.[key];
    if (!svc) return [];
    return (svc.revisions || [])
      .map((rk) => revs[rk])
      .filter(Boolean)
      .map((r) => ({ ref: r.resolvedRef || r.key, version: r.version || r.key, digest: r.digest || '' }))
      .sort((a, b) => cmpVersionDesc(a.version, b.version));
  }
  const revisionsOfService = $derived(revsForService(svcKey));

  async function loadSnapshot() {
    snapLoading = true;
    try {
      const [snap, c] = await Promise.all([api.fleetSnapshot(), api.capabilities().catch(() => null)]);
      snapshot = snap;
      caps = c;
    } catch {
      snapshot = null; // selectors degrade to the advanced raw-ref inputs
      showAdvanced = true;
    }
    snapLoading = false;
  }

  function pickService(key) {
    svcKey = key;
    const revs = revsForService(key); // newest-first
    // Default to the full known history (old = oldest, new = newest) so a service
    // spanning a major bump yields a ready — and, across a major, breaking —
    // comparison. The user can narrow it with the selectors.
    if (revs.length >= 2) {
      newRef = revs[0].ref;
      oldRef = revs[revs.length - 1].ref;
    } else if (revs.length === 1) {
      newRef = revs[0].ref;
    }
  }

  async function analyze(e) {
    e?.preventDefault();
    if (!oldRef || !newRef || loading) return;
    loading = true;
    error = null;
    result = null;
    try {
      result = await api.fleetImpact(oldRef, newRef, includeObserved);
    } catch (err) {
      error = err instanceof ApiError ? err.message : 'Couldn’t analyze the impact.';
    }
    loading = false;
  }

  onMount(async () => {
    await loadSnapshot();
    // An entry point from the Operational Graph passes a service key; preselect it
    // and default to its two most recent revisions so the comparison is ready.
    if (params.svc && !(params.old && params.new)) pickService(params.svc);
    else if (params.svc) svcKey = params.svc;
    // A deep link with both refs runs immediately (Compare, a service or a
    // revision preconfigured the analysis).
    if (oldRef && newRef) analyze();
  });

  const consumers = $derived(result?.consumers || []);
  const breakingChanges = $derived(result?.breakingChanges || []);
  const potentialChanges = $derived(result?.potentiallyBreakingChanges || []);
  const owners = $derived(result?.owners || []);
  const activeTargets = $derived(result?.activeTargets || []);
  const limitations = $derived(result?.limitations || []);
  const snapshotMatches = $derived(!!snapshot && !!result && snapshot.snapshotId === result.snapshotId);
</script>

<div class="page-header">
  <h1>Impact</h1>
  <span class="subtitle">Blast radius of an old → new contract change over the current operational graph.</span>
</div>

<form class="impact-form" onsubmit={analyze}>
  <!-- §2.1: the primary workflow is revision selectors populated from known
       revisions; raw refs remain available as an advanced input. -->
  <div class="selectors">
    <div class="field">
      <label for="impact-svc">Service</label>
      <select id="impact-svc" value={svcKey} onchange={(e) => pickService(e.currentTarget.value)} disabled={snapLoading || services.length === 0}>
        <option value="">{snapLoading ? 'Loading…' : (services.length ? 'Select a service…' : 'No services')}</option>
        {#each services as s}<option value={s.key}>{s.label}</option>{/each}
      </select>
    </div>
    <div class="field">
      <label for="impact-old-rev">Old revision</label>
      <select id="impact-old-rev" value={oldRef} onchange={(e) => (oldRef = e.currentTarget.value)} disabled={!svcKey}>
        <option value="">Select…</option>
        {#each revisionsOfService as r}<option value={r.ref}>{r.version}</option>{/each}
      </select>
    </div>
    <div class="field">
      <label for="impact-new-rev">New revision</label>
      <select id="impact-new-rev" value={newRef} onchange={(e) => (newRef = e.currentTarget.value)} disabled={!svcKey}>
        <option value="">Select…</option>
        {#each revisionsOfService as r}<option value={r.ref}>{r.version}</option>{/each}
      </select>
    </div>
  </div>

  <button type="button" class="link-btn" onclick={() => (showAdvanced = !showAdvanced)}>
    {showAdvanced ? '− Hide' : '+ Advanced'} raw refs
  </button>
  {#if showAdvanced}
    <div class="advanced">
      <div class="field">
        <label for="impact-old">Old ref</label>
        <input id="impact-old" type="text" bind:value={oldRef} placeholder="oci://ghcr.io/org/svc@sha256:… or ./path" />
      </div>
      <div class="field">
        <label for="impact-new">New ref</label>
        <input id="impact-new" type="text" bind:value={newRef} placeholder="oci://ghcr.io/org/svc@sha256:… or ./path" />
      </div>
    </div>
  {/if}

  <div class="form-actions">
    <label class="check-field" title={observedAvailable ? 'Let observed (runtime) relationships raise consumer confidence' : 'No observed relationship source is configured for this dashboard'}>
      <input type="checkbox" bind:checked={includeObserved} disabled={!observedAvailable} />
      Include observed{#if !observedAvailable} <span class="text-dim">(no observed source)</span>{/if}
    </label>
    <button type="submit" class="btn btn-primary" disabled={!oldRef || !newRef || loading}>
      {loading ? 'Analyzing…' : 'Analyze impact'}
    </button>
  </div>
</form>

{#if error}
  <EmptyState error={error} onRetry={analyze} title="Couldn’t analyze the impact" />
{:else if loading}
  <EmptyState loading message="Analyzing the change…" />
{:else if result}
  <div class="impact-summary">
    <span class="badge {classificationClass(result.classification)}">{result.classification.replace(/_/g, ' ')}</span>
    <span class="text-2">{result.service}{#if result.oldVersion || result.newVersion} · {result.oldVersion || '?'} → {result.newVersion || '?'}{/if}</span>
    <span class="badge {completenessClass(result.completeness)}"><span class="badge-dot"></span>{completenessLabel(result.completeness)}</span>
    {#if result.asOf}<span class="text-3">as of {formatDate(result.asOf)}</span>{/if}
    {#if result.snapshotId}
      <span class="snap-id" class:match={snapshotMatches} title="{snapshotMatches ? 'Same snapshot the Operational Graph is showing' : result.snapshotId}">
        snapshot {result.snapshotId.slice(0, 15)}…{#if snapshotMatches} ✓ matches graph{/if}
      </span>
    {/if}
  </div>

  {#if limitations.length > 0}
    <div class="partial-banner" role="status">
      <strong>Incomplete evidence.</strong>
      <ul class="limitations">
        {#each limitations as lim}<li><code>{lim.code}</code>{#if lim.source} <span class="text-dim">({lim.source})</span>{/if} — {lim.message}</li>{/each}
      </ul>
    </div>
  {/if}

  <!-- §2.3: breaking and potentially-breaking are shown SEPARATELY; a
       POTENTIAL_BREAKING change is never labelled "Breaking". -->
  <div class="changes-grid">
    <div class="section">
      <div class="section-title">Breaking changes <span class="tab-count">{breakingChanges.length}</span></div>
      {#if breakingChanges.length === 0}
        <p class="text-dim">No confirmed breaking changes.</p>
      {:else}
        <div class="table-wrap">
          <table>
            <thead><tr><th>Path</th><th>Type</th><th>Reason</th></tr></thead>
            <tbody>
              {#each breakingChanges as c}
                <tr><td><code>{c.path}</code></td><td><span class="badge {changeTypeClass(c.type)}">{c.type}</span></td><td class="text-2">{c.reason}</td></tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </div>
    <div class="section">
      <div class="section-title">Potentially breaking <span class="tab-count">{potentialChanges.length}</span></div>
      {#if potentialChanges.length === 0}
        <p class="text-dim">No potentially breaking changes.</p>
      {:else}
        <div class="table-wrap">
          <table>
            <thead><tr><th>Path</th><th>Type</th><th>Reason</th></tr></thead>
            <tbody>
              {#each potentialChanges as c}
                <tr><td><code>{c.path}</code></td><td><span class="badge badge-warn">{c.type}</span></td><td class="text-2">{c.reason}</td></tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </div>
  </div>

  <div class="section" style="margin-top:var(--sp-5)">
    <div class="section-title">Affected consumers <span class="tab-count">{consumers.length}</span></div>
    {#if consumers.length === 0}
      <EmptyState title="No affected consumers" message="No service in the operational graph consumes this change." />
    {:else}
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Consumer</th>
              <th data-tip="Direct consumers depend on the changed service; transitive ones are reached through others">Reach</th>
              <th data-tip="The path from the consumer to the changed service">Path</th>
              <th data-tip="The consumer's declared compatibility range">Range</th>
              <th>Verdict</th>
              <th data-tip="How strongly the impact is evidenced">Confidence</th>
              <th>Provenance</th>
              <th>Owner</th>
              <th>Targets</th>
            </tr>
          </thead>
          <tbody>
            {#each consumers as c}
              <tr>
                <td>{c.service}{#if c.domain} <span class="text-dim">({c.domain})</span>{/if}</td>
                <td>{#if c.direct}<span class="badge badge-info">Direct</span>{:else}<span class="badge badge-neutral">Transitive · depth {c.depth}</span>{/if}</td>
                <td class="path-cell">{#if (c.path || []).length}{(c.path || []).join(' → ')}{:else}<span class="text-dim">—</span>{/if}</td>
                <td>{#if c.compatibility}<code>{c.compatibility}</code>{:else}<span class="text-dim">—</span>{/if}</td>
                <td><span class="badge {verdictClass(c.compatibilityVerdict)}">{c.compatibilityVerdict}</span></td>
                <td><span class="pill" title={CONFIDENCE_EXPLAIN[c.confidence] || ''}>{c.confidence}</span></td>
                <td class="text-2">{c.provenance}</td>
                <td>{#if c.owner}{c.owner}{:else}<span class="text-dim">—</span>{/if}</td>
                <td>{(c.targets || []).length}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      <details class="confidence-legend">
        <summary>What do the confidence levels mean?</summary>
        <dl>
          {#each Object.entries(CONFIDENCE_EXPLAIN) as [k, v]}<dt>{k}</dt><dd>{v}</dd>{/each}
        </dl>
      </details>
    {/if}
  </div>

  <div class="meta-lists">
    <div class="meta-block">
      <div class="section-title">Owners <span class="tab-count">{owners.length}</span></div>
      {#if owners.length > 0}<div class="chips">{#each owners as o}<span class="pill">{o}</span>{/each}</div>{:else}<p class="text-dim">No owners identified.</p>{/if}
    </div>
    <div class="meta-block">
      <div class="section-title">Active targets <span class="tab-count">{activeTargets.length}</span></div>
      {#if activeTargets.length > 0}<div class="chips">{#each activeTargets as t}<span class="pill">{t}</span>{/each}</div>{:else}<p class="text-dim">No active targets.</p>{/if}
    </div>
  </div>
{:else}
  <EmptyState title="Analyze a change" message="Pick a service and two revisions (or enter raw refs) to see the blast radius over the current operational graph." />
{/if}

<style>
  .page-header { display: flex; align-items: baseline; gap: var(--sp-3); margin-bottom: var(--sp-4); flex-wrap: wrap; }
  .subtitle { color: var(--c-text-3); font-size: var(--text-sm); }

  .impact-form { display: flex; flex-direction: column; gap: var(--sp-3); margin-bottom: var(--sp-5); padding: var(--sp-4); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); }
  .selectors { display: grid; grid-template-columns: 2fr 1fr 1fr; gap: var(--sp-3); }
  .advanced { display: grid; grid-template-columns: 1fr 1fr; gap: var(--sp-3); }
  .field { display: flex; flex-direction: column; gap: 6px; }
  .field label { font-size: var(--text-xs); color: var(--c-text-3); font-weight: 600; text-transform: uppercase; }
  .field input, .field select { padding: var(--sp-2) var(--sp-3); min-height: var(--touch-min); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-bg); color: var(--c-text); font: inherit; font-size: var(--text-sm); }
  .link-btn { align-self: flex-start; background: none; border: none; color: var(--c-accent); cursor: pointer; font-size: var(--text-sm); padding: 0; }
  .form-actions { display: flex; align-items: center; justify-content: space-between; gap: var(--sp-3); flex-wrap: wrap; }
  .check-field { display: inline-flex; align-items: center; gap: var(--sp-2); font-size: var(--text-sm); color: var(--c-text-2); }
  .check-field input:disabled { cursor: not-allowed; }

  .impact-summary { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; margin-bottom: var(--sp-4); }
  .snap-id { font-family: var(--font-mono, monospace); font-size: var(--text-xs); color: var(--c-text-3); padding: 2px 6px; border: 1px solid var(--c-border); border-radius: 999px; }
  .snap-id.match { color: var(--c-ok); border-color: var(--c-ok-border, var(--c-ok)); }

  .partial-banner { padding: var(--sp-3) var(--sp-4); margin-bottom: var(--sp-4); border: 1px solid var(--c-warn-border); border-radius: var(--radius-sm); background: var(--c-warn-bg); color: var(--c-text-2); font-size: var(--text-sm); }
  .partial-banner strong { color: var(--c-warn); }
  .limitations { margin: var(--sp-2) 0 0; padding-left: var(--sp-4); display: flex; flex-direction: column; gap: 4px; }

  .changes-grid { display: grid; grid-template-columns: 1fr 1fr; gap: var(--sp-4); }
  .path-cell { font-family: var(--font-mono, monospace); font-size: var(--text-xs); }
  .confidence-legend { margin-top: var(--sp-3); font-size: var(--text-sm); }
  .confidence-legend dl { display: grid; grid-template-columns: auto 1fr; gap: 4px var(--sp-3); margin-top: var(--sp-2); }
  .confidence-legend dt { font-weight: 600; color: var(--c-text-2); }
  .confidence-legend dd { margin: 0; color: var(--c-text-3); }

  .meta-lists { display: grid; grid-template-columns: repeat(auto-fit, minmax(240px, 1fr)); gap: var(--sp-4); margin-top: var(--sp-5); }
  .chips { display: flex; flex-wrap: wrap; gap: var(--sp-2); margin-top: var(--sp-2); }

  .text-dim { color: var(--c-text-3); }
  .text-2 { color: var(--c-text-2); }
  .text-3 { color: var(--c-text-3); font-size: var(--text-sm); }

  @media (max-width: 768px) {
    .selectors, .advanced, .changes-grid { grid-template-columns: 1fr; }
  }
</style>
