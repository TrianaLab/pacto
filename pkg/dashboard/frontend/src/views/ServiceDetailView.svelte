<script>
  import { onMount, untrack } from 'svelte';
  import { api } from '../lib/api.ts';
  import { navigate, serviceUrl, serviceVersionUrl, diffUrl, ownerUrl } from '../lib/router.ts';
  import { complianceClass, classificationClass, versionPolicyLabel, versionPolicyClass, ownerIsStructured, referencedDocPaths, paginate } from '../lib/format.ts';
  import StatusBadge from '../components/StatusBadge.svelte';
  import ComplianceScore from '../components/ComplianceScore.svelte';
  import OwnerLink from '../components/OwnerLink.svelte';
  import SourceDot from '../components/SourceDot.svelte';
  import { compareDiffUrl } from '../lib/router.ts';
  import { buildVersionSubgraph } from '../lib/graph.ts';
  import { formatDate } from '../lib/dateFormat.ts';
  import DiffChangesTable from '../DiffChangesTable.svelte';

  import OverviewSection from '../sections/OverviewSection.svelte';
  import InterfacesSection from '../sections/InterfacesSection.svelte';
  import DependenciesSection from '../sections/DependenciesSection.svelte';
  import ConfigSection from '../sections/ConfigSection.svelte';
  import PolicySection from '../sections/PolicySection.svelte';
  import ReadinessSection from '../sections/ReadinessSection.svelte';
  import DocsSection from '../sections/DocsSection.svelte';
  import SectionState from '../sections/SectionState.svelte';
  import SourcesPanel from '../sections/SourcesPanel.svelte';
  import ValidationSection from '../sections/ValidationSection.svelte';
  import RuntimeDiffSection from '../sections/RuntimeDiffSection.svelte';
  import ObservedRuntimeSection from '../sections/ObservedRuntimeSection.svelte';

  let { name, version = null, services = [], refreshTick = 0, onServiceResolved } = $props();

  let loading = $state(true);
  let error = $state(null);
  let detail = $state(null);
  let versions = $state([]);
  let dependents = $state([]);
  let crossRefs = $state(null);
  // Distinguish "failed to load" from "empty" for the secondary fetches so the
  // sections render an explicit error/retry instead of silently disappearing.
  let versionsError = $state(false);
  let depsError = $state(false);

  // Stable, ordered domain sections. `key` maps into detail.sectionMeta so each
  // section reports present / empty / not_applicable / unavailable consistently.
  const DOMAIN_SECTIONS = [
    { id: 'interfaces', label: 'Interfaces', key: 'interfaces' },
    { id: 'dependencies', label: 'Dependencies', key: 'dependencies' },
    { id: 'config', label: 'Configurations', key: 'configurations' },
    { id: 'policy', label: 'Policies', key: 'policies' },
    { id: 'readiness', label: 'Readiness', key: 'readiness' },
    { id: 'docs', label: 'Documentation', key: 'docs' },
    { id: 'validation', label: 'Validation', key: 'validation' },
    { id: 'runtimeDiff', label: 'Contract vs Runtime', key: 'runtimeDiff' },
    { id: 'observed', label: 'Observed Runtime', key: 'observedRuntime' },
  ];

  function sectionState(key) {
    return detail?.sectionMeta?.[key] ?? { state: 'present' };
  }
  function isPresent(key) {
    return sectionState(key).state === 'present';
  }
  // Dependencies has extra client-side inputs (dependents/cross-refs) beyond the
  // contract's own deps, so its presence is computed across all three.
  let hasDependencyData = $derived(
    isPresent('dependencies') ||
    (dependents?.length > 0) ||
    (crossRefs?.references?.length > 0) ||
    (crossRefs?.referencedBy?.length > 0),
  );
  let graphData = $state(null);
  let resolving = $state(false);
  let resolveError = $state(null);

  // Version history pagination
  const VERSIONS_PER_PAGE = 10;
  let versionsPage = $state(1);
  let pagedVersions = $derived(paginate(versions || [], versionsPage, VERSIONS_PER_PAGE));

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
      // Compare against the version currently being viewed (the baseline), so
      // from a historical view you can diff it against any other row.
      diffResult = await api.diff(name, fromVersion, name, detail.version);
    } catch (e) {
      diffError = e.message;
    }
    diffLoading = false;
  }

  function goToVersion(v) {
    if (!v || v === currentVersion) navigate('detail', { name });
    else navigate('detail', { name, version: v });
  }

  // Section open states
  let openSections = $state({
    overview: true, sources: false, interfaces: true, dependencies: true,
    config: false, policy: false, readiness: false, docs: false, validation: false,
    runtimeDiff: false, observed: false,
  });

  // Derived view model
  let blastRadius = $derived(services.find(s => s.name === name)?.blastRadius || 0);
  // When viewing a specific version, "current" comes from the versions list
  // (isCurrent) or the fleet services prop; otherwise it is just what we show.
  let currentVersion = $derived(
    version
      ? (versions.find(v => v.isCurrent)?.version || services.find(s => s.name === name)?.version || '')
      : (detail?.version || '')
  );
  let isHistorical = $derived(!!version && !!currentVersion && version !== currentVersion);
  // Label for the version-history compare buttons: the baseline is whatever
  // version is being viewed ("current" when that's the deployed one).
  let baselineLabel = $derived(detail?.version && detail.version !== currentVersion ? detail.version : 'current');
  let insights = $derived(detail?.insights || []);
  let sources = $derived.by(() => {
    const s = detail?.sources || [];
    return s.length ? s : detail?.source ? [detail.source] : [];
  });
  // Stable section list: the same sections always appear (each self-explains its
  // state) so nothing silently appears/disappears between services or reloads.
  let availableSections = $derived.by(() => {
    if (!detail) return [];
    const sections = [{ id: 'overview', label: 'Overview' }, { id: 'sources', label: 'Sources' }];
    for (const s of DOMAIN_SECTIONS) sections.push({ id: s.id, label: s.label });
    if (versions?.length > 0 || versionsError) sections.push({ id: 'versions', label: 'Versions' });
    return sections;
  });

  async function load() {
    loading = true;
    error = null;
    resolveError = null;
    try {
      detail = version ? await api.serviceAtVersion(name, version) : await api.service(name);
      loading = false;

      versionsError = false;
      depsError = false;
      const [vers, deps, refs] = await Promise.all([
        api.versions(name).catch(() => { versionsError = true; return []; }),
        api.dependents(name).catch(() => { depsError = true; return []; }),
        api.crossRefs(name).catch(() => { depsError = true; return null; }),
      ]);
      versions = vers || [];
      versionsPage = 1;
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

  async function retryVersions() {
    versionsError = false;
    try {
      versions = (await api.versions(name)) || [];
      versionsPage = 1;
    } catch {
      versionsError = true;
    }
  }

  let graphLoaded = $state(false);

  async function loadGraph() {
    if (graphLoaded) return;
    graphLoaded = true;
    if (version) {
      // Historical view: build the graph from THIS version's declared deps so it
      // reflects the selected version (e.g. a dep added later) instead of the
      // current global topology.
      graphData = buildVersionSubgraph(detail, services, version);
    } else {
      graphData = await api.graph().catch(() => null);
    }
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

  let initialTick = untrack(() => refreshTick);

  $effect(() => {
    if (refreshTick > initialTick) {
      reload();
    }
  });

  async function reload() {
    try {
      // Background refresh (runs every poll tick). A transient failure on a
      // secondary fetch must NOT clobber good data with empty — keep the stale
      // value and surface failed!=empty, matching load(). (Sentinel marks a
      // failed fetch so we can skip the overwrite.)
      const FAILED = Symbol('failed');
      const [svc, vers, deps, refs] = await Promise.all([
        version ? api.serviceAtVersion(name, version) : api.service(name),
        api.versions(name).catch(() => FAILED),
        api.dependents(name).catch(() => FAILED),
        api.crossRefs(name).catch(() => FAILED),
      ]);
      detail = svc;
      if (vers !== FAILED) { versions = vers || []; versionsError = false; } else { versionsError = true; }
      if (deps !== FAILED) { dependents = deps || []; } else { depsError = true; }
      if (refs !== FAILED) { crossRefs = refs; } else { depsError = true; }
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

  {#if isHistorical}
    <div class="version-banner">
      Viewing version <strong>{version}</strong> — not the current version (<strong>{currentVersion}</strong>).
      <a href={serviceUrl(name)}>Back to current</a>
      <a href={compareDiffUrl({ fromName: name, fromVer: version, toName: name, toVer: currentVersion })}>Compare with current</a>
    </div>
  {/if}

  <!-- Header -->
  <header class="detail-header fade-in-up">
    <div class="detail-title-row">
      <h1>{detail.name}</h1>
      {#if detail.runtimeEvaluated}
        <StatusBadge status={detail.contractStatus} />
      {:else}
        <span class="badge badge-definition" data-tip="No cluster runtime data for this view — showing the contract definition only (runtime status unknown)">
          <span class="badge-dot"></span>Definition only
        </span>
      {/if}
      {#if detail.runtimeEvaluated && detail.compliance}
        {#if detail.compliance.score != null}
          <ComplianceScore score={detail.compliance.score} />
        {/if}
        {#if detail.compliance.summary?.errors > 0}
          <span class="badge badge-err">{detail.compliance.summary.errors} error{detail.compliance.summary.errors > 1 ? 's' : ''}</span>
        {/if}
        {#if detail.compliance.summary?.warnings > 0}
          <span class="badge badge-warn">{detail.compliance.summary.warnings} warning{detail.compliance.summary.warnings > 1 ? 's' : ''}</span>
        {/if}
      {/if}
      {#if detail.checksSummary && detail.runtimeEvaluated && detail.contractStatus !== 'Reference'}
        <span class="text-2">{detail.checksSummary.passed}/{detail.checksSummary.total} checks</span>
      {/if}
      {#if blastRadius > 0}
        <a href="#/graph" class="blast-indicator" class:blast-warn={blastRadius >= 3 && blastRadius < 5} class:blast-high={blastRadius >= 5} data-tip="If this service fails, {blastRadius} other service{blastRadius !== 1 ? 's are' : ' is'} transitively impacted">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="14" height="14"><circle cx="12" cy="12" r="3"/><circle cx="12" cy="12" r="7" opacity="0.4"/><circle cx="12" cy="12" r="11" opacity="0.2"/></svg>
          blast radius: {blastRadius}
        </a>
      {/if}
    </div>
    <div class="detail-meta">
      {#if detail.version}<span class="pill">{detail.version}</span>{/if}
      {#if detail.sectionMeta?.version?.overriddenBy === 'k8s'}
        <span class="pill pill-override" data-tip="Effective deployed version from the cluster — differs from the contract-declared version">via cluster</span>
      {/if}
      {#if detail.contractStatus !== 'Reference'}
        {#if detail.versionPolicy}
          <span class="pill pill-policy {versionPolicyClass(detail.versionPolicy)}" data-tip={detail.resolvedRef || ''}>{versionPolicyLabel(detail.versionPolicy)}</span>
        {/if}
        {#if detail.updateAvailable && detail.latestAvailable}
          <span class="pill pill-update" data-tip="Informational — does not affect compliance">
            {detail.latestAvailable} available
          </span>
          <a href={compareDiffUrl({ fromName: name, fromVer: detail.version, toName: name, toVer: detail.latestAvailable })} class="btn btn-sm btn-update">Compare</a>
        {/if}
      {/if}
      {#each sources as src}
        <SourceDot source={src} />
      {/each}
      <span class="text-2 owner-link">owner: <OwnerLink owner={detail.owner} /></span>
      {#if detail.sectionMeta?.owner?.overriddenBy === 'k8s'}
        <span class="pill pill-override" data-tip="Owner from the cluster — differs from the contract-declared owner">via cluster</span>
      {/if}
      {#if ownerIsStructured(detail.owner) && detail.owner.dri}
          <span class="text-3">dri: {detail.owner.dri}</span>
        {/if}
      {#if detail.namespace}<span class="text-2">ns: {detail.namespace}</span>{/if}
      {#if detail.resolvedRef || detail.imageRef}<code class="detail-ref text-3">{detail.resolvedRef || detail.imageRef}</code>{/if}
      {#if versions?.length > 1}
        <div class="version-actions">
          <select class="btn btn-sm version-select" aria-label="Select version"
            value={detail.version}
            onchange={(e) => goToVersion(e.currentTarget.value)}>
            {#each versions as v}
              <option value={v.version}>{v.version}{v.isCurrent ? ' (current)' : ''}</option>
            {/each}
          </select>
          <a href={diffUrl(name)} class="btn btn-sm btn-compare">Compare versions</a>
        </div>
      {/if}
    </div>
  </header>

  <!-- Reference-only banner -->
  {#if detail.contractStatus === 'Reference'}
    <div class="ref-banner">
      <strong>Reference-only contract</strong> — no runtime target. Used as a shared definition or dependency reference.
    </div>
  {/if}

  <!-- Section nav -->
  {#if availableSections?.length > 2}
    <nav class="section-nav" aria-label="Sections">
      {#each availableSections as sec}
        <button type="button" class="section-nav-item" onclick={() => scrollToSection(sec.id)}>
          {sec.label}
        </button>
      {/each}
    </nav>
  {/if}

  <!-- Insights -->
  {#if insights?.length > 0}
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
    source={detail.source || ''}
    bind:open={openSections.overview}
  />

  <SourcesPanel id="section-sources" {name} bind:open={openSections.sources} />

  {#if isPresent('interfaces')}
    <InterfacesSection id="section-interfaces" interfaces={detail.interfaces || []} source={sectionState('interfaces').source} bind:open={openSections.interfaces} />
  {:else}
    <SectionState id="section-interfaces" title="Interfaces" meta={sectionState('interfaces')} bind:open={openSections.interfaces} />
  {/if}

  {#if hasDependencyData}
    <DependenciesSection
      id="section-dependencies"
      {name} {services} {graphData} {dependents} {crossRefs} {isHistorical}
      dependencies={detail.dependencies || []}
      source={sectionState('dependencies').source}
      bind:open={openSections.dependencies}
    />
  {:else}
    <SectionState id="section-dependencies" title="Dependencies"
      meta={depsError ? { state: 'unavailable', reason: 'could not load dependents / references' } : sectionState('dependencies')}
      onRetry={depsError ? load : null} bind:open={openSections.dependencies} />
  {/if}

  {#if isPresent('configurations')}
    <ConfigSection id="section-config" configs={detail.configurations || []} source={sectionState('configurations').source} bind:open={openSections.config} />
  {:else}
    <SectionState id="section-config" title="Configurations" meta={sectionState('configurations')} bind:open={openSections.config} />
  {/if}

  {#if isPresent('policies')}
    <PolicySection id="section-policy" policies={detail.policies || []} source={sectionState('policies').source} bind:open={openSections.policy} />
  {:else}
    <SectionState id="section-policy" title="Policies" meta={sectionState('policies')} bind:open={openSections.policy} />
  {/if}

  {#if isPresent('readiness')}
    <ReadinessSection id="section-readiness" readiness={detail.readiness} docs={detail.docs || []} source={sectionState('readiness').source} bind:open={openSections.readiness} />
  {:else}
    <SectionState id="section-readiness" title="Readiness" meta={sectionState('readiness')} bind:open={openSections.readiness} />
  {/if}

  {#if isPresent('docs')}
    <DocsSection id="section-docs" docs={detail.docs || []} referencedPaths={referencedDocPaths(detail.readiness)} source={sectionState('docs').source} bind:open={openSections.docs} />
  {:else}
    <SectionState id="section-docs" title="Documentation" meta={sectionState('docs')} bind:open={openSections.docs} />
  {/if}

  {#if isPresent('validation')}
    <ValidationSection id="section-validation" validation={detail.validation} conditions={detail.conditions || []} source={sectionState('validation').source} bind:open={openSections.validation} />
  {:else}
    <SectionState id="section-validation" title="Validation" meta={sectionState('validation')} bind:open={openSections.validation} />
  {/if}

  {#if isPresent('runtimeDiff')}
    <RuntimeDiffSection id="section-runtimeDiff" runtimeDiff={detail.runtimeDiff || []} source={sectionState('runtimeDiff').source} bind:open={openSections.runtimeDiff} />
  {:else}
    <SectionState id="section-runtimeDiff" title="Contract vs Runtime" meta={sectionState('runtimeDiff')} bind:open={openSections.runtimeDiff} />
  {/if}

  {#if isPresent('observedRuntime')}
    <ObservedRuntimeSection id="section-observed" observed={detail.observedRuntime} source={sectionState('observedRuntime').source} bind:open={openSections.observed} />
  {:else}
    <SectionState id="section-observed" title="Observed Runtime" meta={sectionState('observedRuntime')} bind:open={openSections.observed} />
  {/if}

  <!-- Version History -->
  {#if versionsError}
    <section class="section" id="section-versions">
      <div class="section-title">Version History</div>
      <div class="section-state state-unavailable">
        <span class="state-label">Couldn't load</span>
        <span class="state-reason">version history could not be fetched</span>
        <button type="button" class="state-retry" onclick={retryVersions}>Retry</button>
      </div>
    </section>
  {:else if versions?.length > 0}
    <section class="section" id="section-versions">
      <div class="section-title">Version History <span class="tab-count">{versions.length}</span></div>
      <div class="table-wrap">
        <table class="version-history">
          <colgroup>
            <col class="vh-version" />
            <col class="vh-class" />
            <col class="vh-source" />
            <col class="vh-created" />
            <col class="vh-compare" />
          </colgroup>
          <thead><tr><th data-tip="Semver version tag">Version</th><th data-tip="Change impact vs previous version">Classification</th><th data-tip="Where this version was found">Source</th><th data-tip="When this version was published">Created</th><th data-tip="Compare this version against the one you're viewing">Compare</th></tr></thead>
          <tbody>
            {#each pagedVersions.items as ver}
              <tr class:version-current={ver.version === detail.version}>
                <td>
                  <a href={ver.isCurrent ? serviceUrl(name) : serviceVersionUrl(name, ver.version)}><code>{ver.version}</code></a>
                  {#if ver.isCurrent}<span class="badge badge-neutral" style="margin-left:6px;font-size:10px">current</span>{/if}
                  {#if ver.version === detail.version && !ver.isCurrent}<span class="badge badge-viewing" style="margin-left:6px;font-size:10px">viewing</span>{/if}
                </td>
                <td>
                  {#if ver.classification === 'BREAKING'}<span class="badge badge-err">Breaking</span>
                  {:else if ver.classification === 'POTENTIAL_BREAKING'}<span class="badge badge-warn">Potential breaking</span>
                  {:else if ver.classification === 'NON_BREAKING'}<span class="badge badge-ok">Non-breaking</span>
                  {:else}<span class="text-3">—</span>
                  {/if}
                </td>
                <td>{#if ver.source}<SourceDot source={ver.source} /> <span class="text-3" style="font-size:var(--text-xs)">{ver.source}</span>{:else}—{/if}</td>
                <td class="text-2">{formatDate(ver.createdAt) || '—'}</td>
                <td>
                  {#if ver.version !== detail.version}
                    <button type="button" class="btn btn-sm" class:btn-active={diffExpandedVer === ver.version} onclick={() => compareVersion(ver.version)}>
                      {diffExpandedVer === ver.version ? 'Close' : `vs ${baselineLabel}`}
                    </button>
                  {:else}
                    <span class="text-3">—</span>
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
                          <span class="text-2">{diffResult.changes?.length || 0} change{(diffResult.changes?.length ?? 0) !== 1 ? 's' : ''}</span>
                          <span class="text-3">{ver.version} → {detail.version}</span>
                        </div>
                        <DiffChangesTable changes={diffResult.changes || []} compact />
                      </div>
                    {/if}
                  </td>
                </tr>
              {/if}
            {/each}
          </tbody>
        </table>
      </div>
      {#if pagedVersions.totalPages > 1}
        <div class="pager">
          <button type="button" class="btn btn-sm" disabled={pagedVersions.page <= 1}
            onclick={() => { versionsPage = pagedVersions.page - 1; }} aria-label="Previous page">‹ Prev</button>
          <span class="pager-info text-3">
            Page {pagedVersions.page} of {pagedVersions.totalPages}
            <span class="text-3">· {pagedVersions.total} versions</span>
          </span>
          <button type="button" class="btn btn-sm" disabled={pagedVersions.page >= pagedVersions.totalPages}
            onclick={() => { versionsPage = pagedVersions.page + 1; }} aria-label="Next page">Next ›</button>
        </div>
      {/if}
    </section>
  {/if}

{/if}

<style>
  .section-state {
    display: flex; align-items: center; gap: var(--sp-2);
    padding: var(--sp-3); border: 1px solid var(--c-warn);
    border-radius: var(--radius-sm); font-size: var(--text-sm);
  }
  .section-state .state-label { font-weight: 600; color: var(--c-warn); }
  .section-state .state-reason { color: var(--c-text-2); }
  .section-state .state-retry {
    margin-left: auto; background: none; border: 1px solid var(--c-border);
    border-radius: var(--radius-xs); color: var(--c-accent); font: inherit;
    padding: 4px 10px; cursor: pointer;
  }
  .section-state .state-retry:hover { background: var(--c-surface-hover, var(--c-surface-inset)); }
  .breadcrumb {
    font-size: var(--text-sm); margin-bottom: var(--sp-4);
    color: var(--c-text-3); display: flex; align-items: center; gap: 6px;
  }
  .breadcrumb a { color: var(--c-text-3); }
  .breadcrumb a:hover { color: var(--c-text); }
  .sep { color: var(--c-text-3); }

  .detail-header { margin-bottom: var(--sp-6); position: relative; z-index: 60; }
  .detail-title-row { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .detail-meta {
    display: flex; align-items: center; gap: var(--sp-2); margin-top: var(--sp-3);
    flex-wrap: wrap; font-size: var(--text-sm);
  }

  /* Version selector + Compare button grouped at the right of the meta row. */
  .version-actions {
    margin-left: auto;
    display: inline-flex;
    align-items: center;
    gap: var(--sp-2);
  }
  /* Native <select> carries the .btn .btn-sm classes so it matches the adjacent
     Compare button; this just restores the pointer cursor. */
  .version-select { cursor: pointer; }
  .version-banner {
    padding: var(--sp-3) var(--sp-4); margin-bottom: var(--sp-5);
    border-radius: var(--radius-sm);
    background: var(--c-warn-bg); border: 1px solid var(--c-warn);
    color: var(--c-text-2); font-size: var(--text-sm);
    display: flex; align-items: center; gap: var(--sp-3); flex-wrap: wrap;
  }
  .version-banner a { color: var(--c-accent); }

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
    display: flex; flex-wrap: wrap; gap: var(--sp-1); row-gap: 2px;
    margin-bottom: var(--sp-5);
    padding: var(--sp-2) 0;
    border-bottom: 1px solid var(--c-border);
    position: sticky; top: var(--navbar-h); z-index: 50;
    background: var(--c-bg);
  }
  .section-nav-item {
    padding: var(--sp-2) var(--sp-3);
    border: none; background: none;
    font: inherit; font-size: var(--text-xs); font-weight: 500;
    color: var(--c-text-3); cursor: pointer;
    border-radius: var(--radius-xs);
    transition: color var(--transition), background var(--transition);
    white-space: nowrap;
    min-height: 36px;
    display: inline-flex; align-items: center;
  }
  .section-nav-item:hover { color: var(--c-text); background: var(--c-surface-hover); }

  .insights-list { display: flex; flex-direction: column; gap: var(--sp-2); }

  .probes-grid { display: flex; flex-wrap: wrap; gap: var(--sp-2); }
  .probe {
    display: flex; align-items: center; gap: var(--sp-2);
    padding: var(--sp-3) var(--sp-3);
    border-radius: var(--radius-sm);
    background: var(--c-surface); border: 1px solid var(--c-border);
    font-size: var(--text-sm);
  }
  .probe-ok { border-color: var(--c-ok-border); }
  .probe-err { border-color: var(--c-err-border); }
  .probe-label { font-weight: 500; }
  .probe-url { font-size: var(--text-xs); color: var(--c-text-3); }

  .text-2 { color: var(--c-text-2); }
  .text-3 { color: var(--c-text-3); }
  .pill-override {
    background: rgba(59, 130, 246, 0.12); color: #2563eb;
    font-size: var(--text-xs); font-weight: 500;
  }
  .owner-link { text-decoration: none; }
  .owner-link:hover { text-decoration: underline; color: var(--c-text); }
  .text-err { color: var(--c-err); font-size: var(--text-xs); }

  .detail-skeleton { padding: var(--sp-4) 0; }

  .btn-active { background: var(--c-accent); color: white; }

  /* Child combinator so `padding: 0` applies only to the wrapper cell, not the
     nested DiffChangesTable's cells (a descendant selector made those rows tight). */
  .diff-expand-row > td {
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
    flex-wrap: wrap;
  }
  .diff-inline-loading {
    display: flex; align-items: center; gap: var(--sp-2);
    padding: var(--sp-3) var(--sp-4);
    color: var(--c-text-2); font-size: var(--text-sm);
  }

  .pill-policy {
    font-size: var(--text-xs); font-weight: 500; opacity: 0.85;
  }
  .policy-tracking { background: var(--c-neutral-bg); color: var(--c-text-2); }
  .policy-tag { background: var(--c-accent-bg); color: var(--c-accent); }
  .policy-digest { background: var(--c-accent-bg); color: var(--c-accent); }

  .pill-update {
    background: var(--c-info-bg, var(--c-accent-bg)); color: var(--c-info, var(--c-accent));
    font-size: var(--text-xs); font-weight: 500;
  }
  .btn-update {
    font-size: var(--text-xs); padding: 4px 10px;
  }

  /* Fixed layout so the nowrap cells can't over-grow the table and flash a
     spurious horizontal scrollbar; Version is left flexible to absorb rounding.
     The .diff-expand-row's colspan cell is exempt (its content wraps freely). */
  .version-history { table-layout: fixed; }
  .version-history th { white-space: normal; }
  .version-history td { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .vh-version { width: auto; }
  .vh-class { width: 22%; }
  .vh-source { width: 16%; }
  .vh-created { width: 18%; }
  .vh-compare { width: 16%; }
  .version-history .diff-expand-row > td { white-space: normal; overflow: visible; }

  .version-current {
    background: var(--c-surface-hover);
  }
  .badge-viewing {
    background: var(--c-accent-bg);
    color: var(--c-accent);
  }

  .pager {
    display: flex; align-items: center; justify-content: flex-end; gap: 12px;
    margin-top: 10px;
  }
  .pager-info { font-size: var(--text-xs); }
  .pager .btn[disabled] { opacity: 0.4; cursor: default; }

  .detail-ref {
    word-break: break-all;
    font-size: var(--text-xs);
  }

  .blast-indicator {
    display: inline-flex; align-items: center; gap: 5px;
    padding: 3px 10px; border-radius: var(--radius-xs);
    font-size: var(--text-xs); font-weight: 600;
    text-decoration: none;
    background: var(--c-warn-bg); color: var(--c-warn);
    transition: opacity var(--transition);
  }
  .blast-indicator:hover { opacity: 0.8; text-decoration: none; }
  .blast-warn { background: var(--c-warn-bg); color: var(--c-warn); }
  .blast-high { background: var(--c-err-bg); color: var(--c-err); }

  /* ─── Mobile ─── */
  @media (max-width: 768px) {
    .detail-title-row { gap: var(--sp-2); }
    .detail-title-row h1 { width: 100%; }
    .detail-meta { gap: var(--sp-2); }
    .probes-grid { flex-direction: column; }
    .probe { flex-wrap: wrap; }
    .diff-inline-header { flex-wrap: wrap; }
    .version-actions { margin-left: 0; width: 100%; }
    .version-actions > * { flex: 1; justify-content: center; }
  }
</style>
