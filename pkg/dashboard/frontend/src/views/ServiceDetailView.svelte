<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.ts';
  import { navigate, serviceUrl, diffUrl } from '../lib/router.ts';
  import { phaseClass, complianceClass, classificationClass, sourceTooltip } from '../lib/format.ts';
  import DiffChangesTable from '../DiffChangesTable.svelte';

  import OverviewSection from '../sections/OverviewSection.svelte';
  import InterfacesSection from '../sections/InterfacesSection.svelte';
  import DependenciesSection from '../sections/DependenciesSection.svelte';
  import ConfigSection from '../sections/ConfigSection.svelte';
  import PolicySection from '../sections/PolicySection.svelte';
  import ValidationSection from '../sections/ValidationSection.svelte';
  import RuntimeDiffSection from '../sections/RuntimeDiffSection.svelte';
  import ObservedRuntimeSection from '../sections/ObservedRuntimeSection.svelte';

  let { name, services = [], refreshTick = 0, onServiceResolved } = $props();

  let loading = $state(true);
  let error = $state(null);
  let detail = $state(null);
  let versions = $state([]);
  let dependents = $state([]);
  let crossRefs = $state(null);
  let graphData = $state(null);
  let resolving = $state(false);
  let resolveError = $state(null);

  // Inline version diff
  let diffExpandedVer = $state(null);
  let diffLoading = $state(false);
  let diffResult = $state(null);
  let diffError = $state(null);

  async function compareVersion(fromVersion) {
    if (diffExpandedVer === fromVersion) {
      diffExpandedVer = null;
      return;
    }
    diffExpandedVer = fromVersion;
    diffLoading = true;
    diffResult = null;
    diffError = null;
    try {
      diffResult = await api.diff(name, fromVersion, name, detail.version);
    } catch (e) {
      diffError = e.message;
    }
    diffLoading = false;
  }

  // Section open states
  let openSections = $state({
    overview: true, interfaces: true, dependencies: true,
    config: false, policy: false, validation: false,
    runtimeDiff: false, observed: false,
  });

  // Derived view model
  let insights = $derived(detail?.insights || []);
  let sources = $derived.by(() => {
    const s = detail?.sources || [];
    return s.length ? s : detail?.source ? [detail.source] : [];
  });
  let availableSections = $derived.by(() => {
    if (!detail) return [];
    const sections = [];
    sections.push({ id: 'overview', label: 'Overview' });
    if (detail.interfaces?.length > 0) sections.push({ id: 'interfaces', label: 'Interfaces' });
    if (detail.dependencies?.length > 0 || dependents.length > 0 || crossRefs)
      sections.push({ id: 'dependencies', label: 'Dependencies' });
    if (detail.configuration) sections.push({ id: 'config', label: 'Configuration' });
    if (detail.policy) sections.push({ id: 'policy', label: 'Policy' });
    if ((detail.validation?.errors?.length > 0) || (detail.validation?.warnings?.length > 0))
      sections.push({ id: 'validation', label: 'Validation' });
    if (detail.runtimeDiff?.length > 0) sections.push({ id: 'runtimeDiff', label: 'Contract vs Runtime' });
    if (detail.observedRuntime) sections.push({ id: 'observed', label: 'Observed Runtime' });
    if (versions.length > 0) sections.push({ id: 'versions', label: 'Versions' });
    return sections;
  });

  async function load() {
    loading = true;
    error = null;
    resolveError = null;
    try {
      detail = await api.service(name);
      loading = false;

      const [vers, deps, refs] = await Promise.all([
        api.versions(name).catch(() => []),
        api.dependents(name).catch(() => []),
        api.crossRefs(name).catch(() => null),
      ]);
      versions = vers || [];
      dependents = deps || [];
      crossRefs = refs;

      // Lazy-load graph only when dependencies section is already open
      if (openSections.dependencies && (detail.dependencies?.length > 0 || deps.length > 0)) {
        loadGraph();
      }
    } catch (e) {
      error = e.message;
      loading = false;
    }
  }

  let graphLoaded = $state(false);

  async function loadGraph() {
    if (graphLoaded) return;
    graphLoaded = true;
    graphData = await api.graph().catch(() => null);
  }

  // Trigger graph load when dependencies section is opened
  $effect(() => {
    if (openSections.dependencies && !graphLoaded && detail) {
      loadGraph();
    }
  });

  function resolveErrorTitle(status) {
    if (status === 403) return 'Authentication failed';
    if (status === 404) return 'Artifact not found';
    if (status === 422) return 'Invalid reference';
    if (status === 502) return 'Registry unreachable';
    return 'Failed to resolve';
  }

  function scrollToSection(id) {
    const el = document.getElementById(`section-${id}`);
    if (el) el.scrollIntoView({ behavior: 'smooth', block: 'start' });
    if (openSections[id] === false) openSections = { ...openSections, [id]: true };
  }

  let initialTick = refreshTick;

  $effect(() => {
    if (refreshTick > initialTick) {
      reload();
    }
  });

  async function reload() {
    try {
      const [svc, vers, deps, refs] = await Promise.all([
        api.service(name),
        api.versions(name).catch(() => []),
        api.dependents(name).catch(() => []),
        api.crossRefs(name).catch(() => null),
      ]);
      detail = svc;
      versions = vers || [];
      dependents = deps || [];
      crossRefs = refs;
    } catch {
      // keep stale data on background refresh
    }
  }

  onMount(() => { load(); });
</script>

{#if resolving}
  <div class="state-box"><div class="spinner"></div><h3>Resolving remote dependency...</h3></div>
{:else if resolveError}
  <div class="state-box">
    <h3>{resolveError.title}</h3>
    <p>{resolveError.message}</p>
    <code>{resolveError.ref}</code>
    <a href="#/" class="btn" style="margin-top:12px">Back to overview</a>
  </div>
{:else if loading}
  <div class="detail-skeleton fade-in">
    <div class="skeleton skeleton-line" style="width:40%; height:24px; margin-bottom:var(--sp-4)"></div>
    <div class="skeleton skeleton-line" style="width:60%; margin-bottom:var(--sp-2)"></div>
    <div class="skeleton skeleton-line" style="width:45%; margin-bottom:var(--sp-6)"></div>
    <div class="skeleton skeleton-line" style="width:100%; height:80px; margin-bottom:var(--sp-4)"></div>
    <div class="skeleton skeleton-line" style="width:80%; margin-bottom:var(--sp-2)"></div>
    <div class="skeleton skeleton-line" style="width:55%"></div>
  </div>
{:else if error}
  <div class="state-box">
    <h3>Service not found</h3>
    <p>{error}</p>
    <a href="#/" class="btn" style="margin-top:12px">Back to overview</a>
  </div>
{:else if detail}

  <!-- Breadcrumb -->
  <nav class="breadcrumb fade-in" aria-label="Breadcrumb">
    <a href="#/">Services</a>
    <span class="sep">/</span>
    <span>{detail.name}</span>
  </nav>

  <!-- Header -->
  <header class="detail-header fade-in-up">
    <div class="detail-title-row">
      <h1>{detail.name}</h1>
      <span class="badge badge-{phaseClass(detail.phase)}"><span class="badge-dot"></span>{detail.phase}</span>
      {#if detail.compliance}
        {#if detail.compliance.score != null}
          <span class="score {complianceClass(detail.compliance.score)}">{detail.compliance.score}%</span>
        {/if}
        {#if detail.compliance.summary?.errors > 0}
          <span class="badge badge-err">{detail.compliance.summary.errors} error{detail.compliance.summary.errors > 1 ? 's' : ''}</span>
        {/if}
        {#if detail.compliance.summary?.warnings > 0}
          <span class="badge badge-warn">{detail.compliance.summary.warnings} warning{detail.compliance.summary.warnings > 1 ? 's' : ''}</span>
        {/if}
      {/if}
      {#if detail.checksSummary}
        <span class="text-2">{detail.checksSummary.passed}/{detail.checksSummary.total} checks</span>
      {/if}
    </div>
    <div class="detail-meta">
      {#if detail.version}<span class="pill">{detail.version}</span>{/if}
      {#each sources as src}
        <span class="source-dot source-dot-{src}" data-tip={sourceTooltip(src)}></span>
      {/each}
      {#if detail.owner}<span class="text-2">owner: {detail.owner}</span>{/if}
      {#if detail.namespace}<span class="text-2">ns: {detail.namespace}</span>{/if}
      {#if detail.imageRef}<code class="text-3">{detail.imageRef}</code>{/if}
      {#if versions.length > 1}
        <a href={diffUrl(name)} class="btn btn-sm" style="margin-left:auto">Compare versions</a>
      {/if}
    </div>
  </header>

  <!-- Reference-only banner -->
  {#if detail.phase === 'Unknown' || detail.phase === 'Reference'}
    <div class="ref-banner">
      <strong>Reference-only contract</strong> — no runtime target. Used as a shared definition or dependency reference.
    </div>
  {/if}

  <!-- Section nav -->
  {#if availableSections.length > 2}
    <nav class="section-nav" aria-label="Sections">
      {#each availableSections as sec}
        <button type="button" class="section-nav-item" onclick={() => scrollToSection(sec.id)}>
          {sec.label}
        </button>
      {/each}
    </nav>
  {/if}

  <!-- Insights -->
  {#if insights.length > 0}
    <div class="section">
      <div class="section-title">Insights</div>
      <div class="insights-list">
        {#each insights as ins}
          <div class="insight insight-{ins.severity}">
            <strong>{ins.title}</strong>
            {#if ins.description}<span>{ins.description}</span>{/if}
          </div>
        {/each}
      </div>
    </div>
  {/if}

  <!-- Endpoints (health/metrics probes) -->
  {#if detail.endpoints?.length > 0}
    <div class="section">
      <div class="section-title">Endpoint Probes</div>
      <div class="probes-grid">
        {#each detail.endpoints as ep}
          <div class="probe" class:probe-ok={ep.healthy === true} class:probe-err={ep.healthy === false}>
            <span class="probe-label">{ep.interface}{ep.type ? ` (${ep.type})` : ''}</span>
            {#if ep.url}<code class="probe-url">{ep.url}</code>{/if}
            {#if ep.statusCode}<span class="pill">{ep.statusCode}</span>{/if}
            {#if ep.latencyMs != null}<span class="text-3">{ep.latencyMs}ms</span>{/if}
            {#if ep.error}<span class="text-err">{ep.error}</span>{/if}
          </div>
        {/each}
      </div>
    </div>
  {/if}

  <!-- Domain sections -->
  <OverviewSection
    id="section-overview"
    conditions={detail.conditions || []}
    runtime={detail.runtime}
    scaling={detail.scaling}
    metadata={detail.metadata}
    bind:open={openSections.overview}
  />

  <InterfacesSection
    id="section-interfaces"
    interfaces={detail.interfaces || []}
    bind:open={openSections.interfaces}
  />

  <DependenciesSection
    id="section-dependencies"
    {name} {services} {graphData} {dependents} {crossRefs}
    dependencies={detail.dependencies || []}
    bind:open={openSections.dependencies}
  />

  <ConfigSection
    id="section-config"
    config={detail.configuration}
    bind:open={openSections.config}
  />

  <PolicySection
    id="section-policy"
    policy={detail.policy}
    bind:open={openSections.policy}
  />

  <ValidationSection
    id="section-validation"
    validation={detail.validation}
    conditions={detail.conditions || []}
    bind:open={openSections.validation}
  />

  <RuntimeDiffSection
    id="section-runtimeDiff"
    runtimeDiff={detail.runtimeDiff || []}
    bind:open={openSections.runtimeDiff}
  />

  <ObservedRuntimeSection
    id="section-observed"
    observed={detail.observedRuntime}
    bind:open={openSections.observed}
  />

  <!-- Version History -->
  {#if versions.length > 0}
    <section class="section" id="section-versions">
      <div class="section-title">Version History <span class="tab-count">{versions.length}</span></div>
      <div class="table-wrap">
        <table>
          <thead><tr><th data-tip="Semver version tag">Version</th><th data-tip="Change impact vs previous version">Classification</th><th data-tip="Where this version was found">Source</th><th data-tip="When this version was published">Created</th><th data-tip="Compare this version against current">Compare</th></tr></thead>
          <tbody>
            {#each versions as ver}
              <tr>
                <td><code>{ver.version}</code></td>
                <td>
                  {#if ver.classification === 'BREAKING'}<span class="badge badge-err">Breaking</span>
                  {:else if ver.classification === 'POTENTIAL_BREAKING'}<span class="badge badge-warn">Potential breaking</span>
                  {:else if ver.classification === 'NON_BREAKING'}<span class="badge badge-ok">Non-breaking</span>
                  {:else}<span class="text-3">—</span>
                  {/if}
                </td>
                <td>{#if ver.source}<span class="source-dot source-dot-{ver.source}" data-tip={sourceTooltip(ver.source)}></span> <span class="text-3" style="font-size:var(--text-xs)">{ver.source}</span>{:else}—{/if}</td>
                <td class="text-2">{ver.createdAt ? new Date(ver.createdAt).toLocaleDateString() : '—'}</td>
                <td>
                  {#if ver.version !== detail.version}
                    <button type="button" class="btn btn-sm" class:btn-active={diffExpandedVer === ver.version} onclick={() => compareVersion(ver.version)}>
                      {diffExpandedVer === ver.version ? 'Close' : 'vs current'}
                    </button>
                  {:else}
                    <span class="badge badge-neutral">current</span>
                  {/if}
                </td>
              </tr>
              {#if diffExpandedVer === ver.version}
                <tr class="diff-expand-row">
                  <td colspan="5">
                    {#if diffLoading}
                      <div class="diff-inline-loading"><div class="spinner"></div> Comparing {ver.version} → {detail.version}…</div>
                    {:else if diffError}
                      <div class="insight insight-critical">{diffError}</div>
                    {:else if diffResult}
                      <div class="diff-inline">
                        <div class="diff-inline-header">
                          <span class="badge {classificationClass(diffResult.classification)}">{diffResult.classification.replace(/_/g, ' ')}</span>
                          <span class="text-2">{diffResult.changes.length} change{diffResult.changes.length !== 1 ? 's' : ''}</span>
                          <span class="text-3">{ver.version} → {detail.version}</span>
                        </div>
                        <DiffChangesTable changes={diffResult.changes} compact />
                      </div>
                    {/if}
                  </td>
                </tr>
              {/if}
            {/each}
          </tbody>
        </table>
      </div>
    </section>
  {/if}

{/if}

<style>
  .breadcrumb {
    font-size: var(--text-sm); margin-bottom: var(--sp-4);
    color: var(--c-text-3); display: flex; align-items: center; gap: 6px;
  }
  .breadcrumb a { color: var(--c-text-3); }
  .breadcrumb a:hover { color: var(--c-text); }
  .sep { color: var(--c-text-3); }

  .detail-header { margin-bottom: var(--sp-5); position: relative; z-index: 60; }
  .detail-title-row { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .detail-meta {
    display: flex; align-items: center; gap: var(--sp-2); margin-top: var(--sp-2);
    flex-wrap: wrap; font-size: var(--text-sm);
  }

  .ref-banner {
    padding: var(--sp-3) var(--sp-4);
    margin-bottom: var(--sp-5);
    border-radius: var(--radius-sm);
    background: var(--c-neutral-bg);
    border: 1px solid var(--c-border);
    color: var(--c-text-2);
    font-size: var(--text-sm);
  }

  .section-nav {
    display: flex; gap: var(--sp-1); flex-wrap: wrap;
    margin-bottom: var(--sp-5);
    padding: var(--sp-2) 0;
    border-bottom: 1px solid var(--c-border);
    position: sticky; top: 48px; z-index: 50;
    background: var(--c-bg);
  }
  .section-nav-item {
    padding: var(--sp-1) var(--sp-3);
    border: none; background: none;
    font: inherit; font-size: var(--text-xs); font-weight: 500;
    color: var(--c-text-3); cursor: pointer;
    border-radius: var(--radius-xs);
    transition: color var(--transition), background var(--transition);
  }
  .section-nav-item:hover { color: var(--c-text); background: var(--c-surface-hover); }

  .insights-list { display: flex; flex-direction: column; gap: var(--sp-2); }

  .probes-grid { display: flex; flex-wrap: wrap; gap: var(--sp-2); }
  .probe {
    display: flex; align-items: center; gap: var(--sp-2);
    padding: var(--sp-2) var(--sp-3);
    border-radius: var(--radius-sm);
    background: var(--c-surface); border: 1px solid var(--c-border);
    font-size: var(--text-sm);
  }
  .probe-ok { border-color: var(--c-ok-border); }
  .probe-err { border-color: var(--c-err-border); }
  .probe-label { font-weight: 500; }
  .probe-url { font-size: 10px; color: var(--c-text-3); }

  .text-2 { color: var(--c-text-2); }
  .text-3 { color: var(--c-text-3); }
  .text-err { color: var(--c-err); font-size: var(--text-xs); }

  .detail-skeleton { padding: var(--sp-4) 0; }

  .btn-active { background: var(--c-accent); color: white; }

  .diff-expand-row td {
    padding: 0 !important;
    border-top: none !important;
  }
  .diff-inline {
    padding: var(--sp-3) var(--sp-4);
    background: var(--c-surface-inset);
    border-top: 1px solid var(--c-border);
    animation: slideDown 200ms ease;
  }
  .diff-inline-header {
    display: flex; align-items: center; gap: var(--sp-2);
    margin-bottom: var(--sp-3);
  }
  .diff-inline-loading {
    display: flex; align-items: center; gap: var(--sp-2);
    padding: var(--sp-3) var(--sp-4);
    color: var(--c-text-2); font-size: var(--text-sm);
  }
</style>
