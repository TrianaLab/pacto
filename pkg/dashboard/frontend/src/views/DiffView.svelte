<script>
  import { onMount, untrack } from 'svelte';
  import { api } from '../lib/api.ts';
  import { serviceUrl, compareDiffUrl, fleetImpactUrl } from '../lib/router.ts';
  import { classificationClass } from '../lib/format.ts';
  import DiffChangesTable from '../DiffChangesTable.svelte';
  import EmptyState from '../components/EmptyState.svelte';

  let {
    name = '',
    initialFrom = '', initialTo = '',
    initialFromName = '', initialToName = '',
    services = [],
  } = $props();

  // Service selection for from/to — seeded from props via untrack (initial values only).
  let fromName = $state(untrack(() => initialFromName || name || ''));
  let toName = $state(untrack(() => initialToName || name || ''));
  let fromVersions = $state([]);
  let toVersions = $state([]);
  let fromVer = $state(untrack(() => initialFrom));
  let toVer = $state(untrack(() => initialTo));
  let loading = $state(false);
  let error = $state(null);
  let result = $state(null);

  async function loadVersionsFor(svcName) {
    if (!svcName) return [];
    try {
      return await api.versions(svcName);
    } catch {
      return [];
    }
  }

  async function onFromNameChange() {
    fromVer = '';
    result = null;
    fromVersions = await loadVersionsFor(fromName);
  }

  async function onToNameChange() {
    toVer = '';
    result = null;
    toVersions = await loadVersionsFor(toName);
  }

  async function initVersions() {
    const [fv, tv] = await Promise.all([
      loadVersionsFor(fromName),
      loadVersionsFor(toName),
    ]);
    fromVersions = fv;
    toVersions = tv;

    // Auto-select versions if not provided via URL
    if (!fromVer && !toVer && fromName === toName && fromVersions?.length >= 2) {
      fromVer = fromVersions[1].version;
      toVer = fromVersions[0].version;
    }
    if (fromVer && toVer) runDiff();
  }

  async function runDiff() {
    if (!fromName || !toName || !fromVer || !toVer) return;
    // Reflect the selection in the URL so a comparison is shareable and survives
    // reload. replaceState avoids a hashchange round-trip (state is seeded once).
    history.replaceState(null, '', compareDiffUrl({ fromName, fromVer, toName, toVer }));
    loading = true;
    error = null;
    result = null;
    try {
      result = await api.diff(fromName, fromVer, toName, toVer);
    } catch (e) {
      error = e.message;
    }
    loading = false;
  }

  function swapSides() {
    const tmpName = fromName;
    const tmpVer = fromVer;
    const tmpVersions = fromVersions;
    fromName = toName;
    fromVer = toVer;
    fromVersions = toVersions;
    toName = tmpName;
    toVer = tmpVer;
    toVersions = tmpVersions;
    // Re-run with the swapped sides rather than blanking the result pane.
    if (fromName && toName && fromVer && toVer) runDiff();
    else result = null;
  }

  // Service names for dropdowns
  let serviceNames = $derived(
    services.length > 0
      ? [...new Set(services.map((s) => s.name))].sort()
      : []
  );

  let isSameService = $derived(fromName === toName);

  // Compare -> Product Impact canonical identity (requirement A2). A Compare workflow
  // knows a service NAME, which is NOT a canonical ServiceKey: domain-a/payments and
  // domain-b/payments both have the name "payments". So the impact CTA RESOLVES the
  // name through the product Entities API to canonical services, and offers a Product
  // Impact route only for a real, unambiguous match -- it never guesses a domain and
  // never fabricates a route:
  //   exactly one match  -> a canonical /fleet/impact/:serviceKey CTA;
  //   several matches     -> explicit disambiguation (one CTA per domain-qualified match);
  //   no match / no fleet -> no CTA at all.
  let fleetCapable = $state(false);
  let impactMatches = $state([]);
  let impactResolved = $state(false);
  const impactName = $derived(toName || fromName);

  async function resolveImpact(name) {
    impactResolved = false;
    impactMatches = [];
    if (!name) { impactResolved = true; return; }
    try {
      const list = await api.fleetEntities({ kinds: ['service'], text: name, limit: 50 });
      // The entities text filter is a substring match; only an EXACT service-name
      // match is this service (label is the canonical service name). A different
      // service that merely contains the text is never offered as this one's impact.
      impactMatches = (list.entities ?? []).filter((e) => e.label === name);
    } catch {
      impactMatches = []; // fleet unavailable or the query failed: no fabricated route
    }
    impactResolved = true;
  }

  $effect(() => { if (fleetCapable) resolveImpact(impactName); });

  onMount(async () => {
    initVersions();
    try { const c = await api.capabilities(); fleetCapable = !!c?.fleet; } catch { fleetCapable = false; }
  });
</script>

<nav class="breadcrumb" aria-label="Breadcrumb">
  <a href="#/">Services</a>
  <span class="sep">/</span>
  {#if name}
    <a href={serviceUrl(name)}>{name}</a>
    <span class="sep">/</span>
  {/if}
  <span>Diff</span>
</nav>

<h1 style="margin-bottom:var(--sp-5)">Compare Versions</h1>

<!-- A2/J: Compare launches the contextual Product Impact workspace, but only for a
     canonical service the name resolves to unambiguously (never a guessed domain). -->
{#snippet impactCta(label)}
  {#if fleetCapable && impactName && impactResolved}
    {#if impactMatches.length === 1}
      <a class="impact-link" href={fleetImpactUrl(impactMatches[0].key)}>{label} {impactName} →</a>
    {:else if impactMatches.length > 1}
      <div class="impact-disambig" role="group" aria-label="Choose a service to analyze">
        <span>Multiple services are named "{impactName}" — choose one:</span>
        {#each impactMatches as m (m.key)}
          <a class="impact-link" href={fleetImpactUrl(m.key)}>{m.domain ? `${m.domain} / ` : ''}{m.label} →</a>
        {/each}
      </div>
    {/if}
    <!-- zero matches: no CTA is rendered; a Product Impact route is never fabricated -->
  {/if}
{/snippet}

<div class="impact-cta-top">
  {@render impactCta('Analyze impact of')}
</div>

<div class="diff-controls">
  <div class="diff-side">
    <div class="diff-field">
      <label for="from-svc">From service</label>
      {#if serviceNames.length > 0}
        <select id="from-svc" bind:value={fromName} onchange={onFromNameChange}>
          <option value="">Select service</option>
          {#each serviceNames as sn}
            <option value={sn}>{sn}</option>
          {/each}
        </select>
      {:else}
        <input id="from-svc" type="text" bind:value={fromName} onchange={onFromNameChange} placeholder="Service name" />
      {/if}
    </div>
    <div class="diff-field">
      <label for="from-ver">Version</label>
      <select id="from-ver" bind:value={fromVer}>
        <option value="">Select version</option>
        {#each fromVersions as v}
          <option value={v.version}>{v.version}{v.isCurrent ? ' (current)' : ''}</option>
        {/each}
      </select>
    </div>
  </div>

  <button type="button" class="btn-swap" onclick={swapSides} title="Swap sides" aria-label="Swap from and to">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="16" height="16"><path d="M7 16l-4-4 4-4"/><path d="M17 8l4 4-4 4"/><line x1="3" y1="12" x2="21" y2="12"/></svg>
  </button>

  <div class="diff-side">
    <div class="diff-field">
      <label for="to-svc">To service</label>
      {#if serviceNames.length > 0}
        <select id="to-svc" bind:value={toName} onchange={onToNameChange}>
          <option value="">Select service</option>
          {#each serviceNames as sn}
            <option value={sn}>{sn}</option>
          {/each}
        </select>
      {:else}
        <input id="to-svc" type="text" bind:value={toName} onchange={onToNameChange} placeholder="Service name" />
      {/if}
    </div>
    <div class="diff-field">
      <label for="to-ver">Version</label>
      <select id="to-ver" bind:value={toVer}>
        <option value="">Select version</option>
        {#each toVersions as v}
          <option value={v.version}>{v.version}{v.isCurrent ? ' (current)' : ''}</option>
        {/each}
      </select>
    </div>
  </div>

  <button type="button" class="btn btn-primary diff-run" onclick={runDiff} disabled={!fromName || !toName || !fromVer || !toVer || loading}>
    {loading ? 'Comparing...' : 'Compare'}
  </button>
</div>

{#if error}
  <div class="insight insight-critical" style="margin-top:var(--sp-4)">{error}</div>
{/if}

{#if result}
  <div class="diff-result">
    {#if (result.changes?.length ?? 0) === 0}
      <EmptyState
        title="No differences"
        message={isSameService
          ? `${fromVer} and ${toVer} are identical.`
          : `${fromName} ${fromVer} and ${toName} ${toVer} are identical.`} />
    {:else}
      <div class="diff-summary">
        <span class="badge {classificationClass(result.classification)}">{result.classification.replace(/_/g, ' ')}</span>
        <span class="text-2">{result.changes.length} change{result.changes.length !== 1 ? 's' : ''}</span>
        {#if !isSameService}
          <span class="text-3">({fromName} {fromVer} vs {toName} {toVer})</span>
        {/if}
        <!-- A2: launch the operational impact of this change over the canonical service. -->
        <span class="impact-cta">{@render impactCta('Analyze operational impact of')}</span>
      </div>

      <DiffChangesTable changes={result.changes} />
    {/if}
  </div>
{/if}

<style>
  .breadcrumb {
    font-size: var(--text-sm); margin-bottom: var(--sp-4);
    color: var(--c-text-3); display: flex; align-items: center; gap: 6px;
  }
  .breadcrumb a { color: var(--c-text-3); }
  .breadcrumb a:hover { color: var(--c-text); }
  .sep { color: var(--c-text-3); }

  .diff-controls {
    display: flex; align-items: flex-end; gap: var(--sp-3); flex-wrap: wrap;
    margin-bottom: var(--sp-5);
  }
  .diff-side {
    display: flex; gap: var(--sp-2); flex: 1; min-width: 200px;
  }
  .diff-field {
    display: flex; flex-direction: column; gap: 6px; flex: 1;
  }
  .diff-field label { font-size: var(--text-xs); color: var(--c-text-3); font-weight: 500; text-transform: uppercase; }
  .diff-field select, .diff-field input {
    padding: var(--sp-2) var(--sp-3);
    min-height: var(--touch-min);
    border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    background: var(--c-bg); color: var(--c-text); font: inherit; font-size: var(--text-sm);
  }

  .btn-swap {
    display: flex; align-items: center; justify-content: center;
    width: var(--touch-min); height: var(--touch-min);
    border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    background: var(--c-surface); cursor: pointer; color: var(--c-text-2);
    transition: all var(--transition); align-self: flex-end;
    flex-shrink: 0;
  }
  .btn-swap:hover { border-color: var(--c-accent); color: var(--c-accent); }

  .diff-run { align-self: flex-end; white-space: nowrap; }

  .diff-result { margin-top: var(--sp-5); }
  .diff-summary { display: flex; align-items: center; gap: var(--sp-2); margin-bottom: var(--sp-4); flex-wrap: wrap; }
  .impact-cta { margin-left: auto; font-weight: 600; font-size: var(--text-sm); }
  .impact-cta-top { margin-bottom: var(--sp-4); }
  .impact-link { color: var(--c-accent); text-decoration: none; font-size: var(--text-sm); }
  .impact-link:hover { text-decoration: underline; }
  .impact-disambig { display: flex; flex-direction: column; gap: 4px; font-size: var(--text-sm); color: var(--c-text-3); }
  .impact-disambig span { color: var(--c-text-2); }

  .text-2 { color: var(--c-text-2); }
  .text-3 { color: var(--c-text-3); font-size: var(--text-sm); }

  /* ─── Mobile ─── */
  @media (max-width: 768px) {
    .diff-controls { flex-direction: column; align-items: stretch; }
    .diff-side { min-width: 0; }
    .btn-swap { align-self: center; transform: rotate(90deg); }
    .diff-run { align-self: stretch; justify-content: center; }
  }
</style>
