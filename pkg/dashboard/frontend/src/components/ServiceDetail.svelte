<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.js';
  import {
    services, serviceDetails, serviceVersions, serviceAggregated,
    dependents, crossRefs, graphData, currentTab, currentService,
    pendingRef, pendingCompat, navigateTo,
  } from '../lib/stores.js';
  import { getSources, extractServiceName, hasValidationPath } from '../lib/helpers.js';
  import PhaseBadge from './PhaseBadge.svelte';
  import SourcePill from './SourcePill.svelte';
  import OverviewTab from './tabs/OverviewTab.svelte';
  import DependenciesTab from './tabs/DependenciesTab.svelte';
  import HistoryTab from './tabs/HistoryTab.svelte';
  import DiffTab from './tabs/DiffTab.svelte';
  import InterfacesTab from './tabs/InterfacesTab.svelte';
  import ValidationsTab from './tabs/ValidationsTab.svelte';
  import RuntimeDiffTab from './tabs/RuntimeDiffTab.svelte';
  import ObservedRuntimeTab from './tabs/ObservedRuntimeTab.svelte';
  import ConfigTab from './tabs/ConfigTab.svelte';
  import PolicyTab from './tabs/PolicyTab.svelte';
  import SourcesTab from './tabs/SourcesTab.svelte';

  let { name } = $props();

  let loading = $state(true);
  let error = $state(null);
  let resolving = $state(false);
  let resolveError = $state(null);

  let detail = $derived($serviceDetails[name]);
  let versions = $derived($serviceVersions[name] || []);
  let agg = $derived($serviceAggregated[name]);
  let sources = $derived(detail ? getSources(detail) : []);

  let deps = $derived(detail?.dependencies || []);
  let hasInterfaces = $derived(detail?.interfaces?.length > 0);
  let hasValidations = $derived(
    (detail?.conditions?.length > 0) ||
    (detail?.validation && ((detail.validation.errors || []).length || (detail.validation.warnings || []).length))
  );
  let hasRuntimeDiff = $derived(detail?.runtimeDiff?.length > 0);
  let hasObserved = $derived(!!detail?.observedRuntime);
  let hasConfig = $derived(!!detail?.configuration || hasValidationPath(detail, 'configuration'));
  let hasPolicy = $derived(!!detail?.policy || hasValidationPath(detail, 'policy'));
  let hasSources = $derived(agg?.sources?.length > 1);

  async function loadDetail() {
    loading = true;
    error = null;
    try {
      const d = await api.getService(name);
      serviceDetails.update((cur) => ({ ...cur, [name]: d }));
      dependents.set([]);
      crossRefs.set({ references: [], referencedBy: [] });
      loading = false;

      // Load supplementary data in background
      const [vers, srcs, deps, refs] = await Promise.all([
        api.getVersions(name).catch(() => []),
        api.getServiceSources(name).catch(() => null),
        api.getDependents(name).catch(() => []),
        api.getCrossRefs(name).catch(() => ({ references: [], referencedBy: [] })),
      ]);
      serviceVersions.update((cur) => ({ ...cur, [name]: vers || [] }));
      if (srcs) serviceAggregated.update((cur) => ({ ...cur, [name]: srcs }));
      dependents.set(deps || []);
      crossRefs.set(refs || { references: [], referencedBy: [] });

      // Lazy-load global graph for dependency visualization
      let gd;
      graphData.subscribe((v) => (gd = v))();
      if (!gd) {
        api.getGraph().catch(() => null).then((g) => { if (g) graphData.set(g); });
      }
    } catch (e) {
      // If 404, try lazy resolution
      const ref = $pendingRef;
      const compat = $pendingCompat;
      const depInfo = ref ? { ref, compatibility: compat || '' } : findDepInfo(name);
      if (e.status === 404 && depInfo) {
        await resolveRemoteDep(depInfo.ref, depInfo.compatibility);
      } else {
        error = e.message;
        loading = false;
      }
    }
  }

  function findDepInfo(targetName) {
    const allDetails = $serviceDetails;
    for (const key in allDetails) {
      const d = allDetails[key];
      if (!d) continue;
      if (d.dependencies) {
        for (const dep of d.dependencies) {
          const depName = dep.name || extractServiceName(dep.ref);
          if (depName === targetName && dep.ref && dep.ref !== targetName)
            return { ref: dep.ref, compatibility: dep.compatibility || '' };
        }
      }
      if (d.configuration?.ref) {
        if (extractServiceName(d.configuration.ref) === targetName)
          return { ref: d.configuration.ref, compatibility: '' };
      }
      if (d.policy?.ref) {
        if (extractServiceName(d.policy.ref) === targetName)
          return { ref: d.policy.ref, compatibility: '' };
      }
    }
    const refs = $crossRefs;
    if (refs?.references) {
      for (const cr of refs.references) {
        if (cr.name === targetName && cr.ref)
          return { ref: cr.ref, compatibility: '' };
      }
    }
    return null;
  }

  async function resolveRemoteDep(ref, compatibility) {
    resolving = true;
    resolveError = null;
    try {
      const d = await api.resolveRef(ref, compatibility);
      const resolvedName = d.name || name;
      serviceDetails.update((cur) => ({ ...cur, [resolvedName]: d }));
      if (resolvedName !== name) {
        currentService.set(resolvedName);
        history.replaceState(null, '', '#service/' + encodeURIComponent(resolvedName));
      }
      services.update((cur) => {
        if (!cur.some((s) => s.name === resolvedName)) {
          return [...cur, { name: resolvedName, version: d.version, owner: d.owner, phase: d.phase, source: 'oci', sources: ['oci'] }];
        }
        return cur;
      });
      pendingRef.set(null);
      pendingCompat.set(null);
      dependents.set([]);
      crossRefs.set({ references: [], referencedBy: [] });
    } catch (e) {
      let title = 'Failed to resolve dependency';
      if (e.status === 403) title = 'Authentication failed';
      else if (e.status === 404) title = 'Artifact not found in registry';
      else if (e.status === 422) title = 'Invalid reference or bundle';
      else if (e.status === 502) title = 'Registry unreachable';
      resolveError = { title, message: e.message, ref };
    }
    resolving = false;
    loading = false;
  }

  function switchTab(tab) {
    currentTab.set(tab);
  }

  onMount(() => {
    loadDetail();
  });
</script>

{#if resolving}
  <div class="loading">
    <div class="spinner"></div>
    Resolving remote dependency&hellip;
    <br><code class="text-dim" style="font-size:var(--text-xs);margin-top:8px;display:inline-block">{$pendingRef}</code>
  </div>
{:else if resolveError}
  <div class="empty-state">
    <div class="empty-state-title">{resolveError.title}</div>
    <p>{resolveError.message}</p>
    <code class="text-dim" style="font-size:var(--text-xs);display:block;margin-top:8px">{resolveError.ref}</code>
    <div style="margin-top:16px"><button type="button" class="dep-link" onclick={() => navigateTo('list')}>Back to overview</button></div>
  </div>
{:else if loading}
  <div class="loading"><div class="spinner"></div>Loading...</div>
{:else if error}
  <div class="empty-state">
    <div class="empty-state-title">Service not found</div>
    <p>{error}</p>
    <div style="margin-top:16px"><button type="button" class="dep-link" onclick={() => navigateTo('list')}>Back to overview</button></div>
  </div>
{:else if detail}
  <!-- Breadcrumb -->
  <div class="breadcrumb">
    <button type="button" class="link-btn" onclick={() => navigateTo('list')}>Overview</button>
    <span class="separator">/</span>
    <span>{detail.name}</span>
  </div>

  <!-- Service header -->
  <div class="service-header">
    <div style="display:flex;align-items:center;gap:8px;flex:1;flex-wrap:wrap">
      <h1 class="service-title">{detail.name}</h1>
      <PhaseBadge phase={detail.phase} />
      {#if detail.compliance}
        <span class="badge {({'OK':'badge-ok','WARNING':'badge-warning','ERROR':'badge-critical'})[detail.compliance.status] || 'badge-neutral'}">{detail.compliance.status}</span>
        {#if detail.compliance.score != null}
          <span class="compliance-score {detail.compliance.score < 50 ? 'compliance-score-error' : detail.compliance.score < 80 ? 'compliance-score-warning' : 'compliance-score-ok'}">{detail.compliance.score}%</span>
        {/if}
        {#if detail.compliance.summary}
          {#if detail.compliance.summary.errors > 0}
            <span class="pill pill-critical">{detail.compliance.summary.errors} error{detail.compliance.summary.errors > 1 ? 's' : ''}</span>
          {/if}
          {#if detail.compliance.summary.warnings > 0}
            <span class="pill pill-warning">{detail.compliance.summary.warnings} warning{detail.compliance.summary.warnings > 1 ? 's' : ''}</span>
          {/if}
        {/if}
      {/if}
      {#if detail.checksSummary}
        <span class="text-dim" style="margin-left:4px">{detail.checksSummary.passed}/{detail.checksSummary.total} checks</span>
      {/if}
      {#if detail.owner}
        <span class="text-dim" style="margin-left:4px">owner: {detail.owner}</span>
      {/if}
    </div>
  </div>

  <!-- Contract info line -->
  <div class="contract-info-line">
    {#if detail.version}
      <span class="pill pill-dim">{detail.version}</span>
    {/if}
    {#each sources as src}
      <SourcePill type={src} />
    {/each}
    {#if detail.imageRef}
      <code class="contract-ref-code">{detail.imageRef}</code>
    {/if}
  </div>

  <!-- Reference-only banner -->
  {#if detail.phase === 'Unknown' || detail.phase === 'Reference'}
    <div style="display:flex;align-items:center;gap:10px;padding:12px 16px;margin-bottom:16px;border-radius:var(--radius-sm);background:var(--neutral-bg);border:1px solid var(--border);color:var(--text-secondary);font-size:var(--text-sm)">
      <span style="font-size:18px">{'\uD83D\uDCC4'}</span>
      <span><strong>Reference-only contract</strong> &mdash; no runtime target. Used as a shared definition or dependency reference.</span>
    </div>
  {/if}

  <!-- Tab bar -->
  <div class="tab-bar" role="tablist">
    <button class="tab-btn" class:tab-active={$currentTab === 'overview'} onclick={() => switchTab('overview')}>Overview</button>
    <button class="tab-btn" class:tab-active={$currentTab === 'dependencies'} onclick={() => switchTab('dependencies')}>
      Dependencies {#if deps.length}<span class="tab-count">{deps.length}</span>{/if}
    </button>
    <button class="tab-btn" class:tab-active={$currentTab === 'history'} onclick={() => switchTab('history')}>
      History {#if versions.length}<span class="tab-count">{versions.length}</span>{/if}
    </button>
    {#if versions.length > 1}
      <button class="tab-btn" class:tab-active={$currentTab === 'diff'} onclick={() => switchTab('diff')}>Diff</button>
    {/if}
    {#if hasInterfaces}
      <button class="tab-btn" class:tab-active={$currentTab === 'interfaces'} onclick={() => switchTab('interfaces')}>
        Interfaces <span class="tab-count">{detail.interfaces.length}</span>
      </button>
    {/if}
    {#if hasValidations}
      <button class="tab-btn" class:tab-active={$currentTab === 'validations'} onclick={() => switchTab('validations')}>Validations</button>
    {/if}
    {#if hasRuntimeDiff}
      <button class="tab-btn" class:tab-active={$currentTab === 'runtime-diff'} onclick={() => switchTab('runtime-diff')}>Contract vs Runtime</button>
    {/if}
    {#if hasObserved}
      <button class="tab-btn" class:tab-active={$currentTab === 'observed'} onclick={() => switchTab('observed')}>Observed Runtime</button>
    {/if}
    {#if hasConfig}
      <button class="tab-btn" class:tab-active={$currentTab === 'config'} onclick={() => switchTab('config')}>Config</button>
    {/if}
    {#if hasPolicy}
      <button class="tab-btn" class:tab-active={$currentTab === 'policy'} onclick={() => switchTab('policy')}>Policy</button>
    {/if}
    {#if hasSources}
      <button class="tab-btn" class:tab-active={$currentTab === 'sources'} onclick={() => switchTab('sources')}>
        Sources <span class="tab-count">{agg.sources.length}</span>
      </button>
    {/if}
  </div>

  <!-- Tab content -->
  <div>
    {#if $currentTab === 'overview'}
      <OverviewTab {detail} />
    {:else if $currentTab === 'dependencies'}
      <DependenciesTab {detail} />
    {:else if $currentTab === 'history'}
      <HistoryTab {versions} serviceName={name} />
    {:else if $currentTab === 'diff'}
      <DiffTab {versions} serviceName={name} />
    {:else if $currentTab === 'interfaces'}
      <InterfacesTab interfaces={detail.interfaces || []} />
    {:else if $currentTab === 'validations'}
      <ValidationsTab {detail} />
    {:else if $currentTab === 'runtime-diff'}
      <RuntimeDiffTab rows={detail.runtimeDiff || []} />
    {:else if $currentTab === 'observed'}
      <ObservedRuntimeTab observed={detail.observedRuntime} />
    {:else if $currentTab === 'config'}
      <ConfigTab config={detail.configuration} />
    {:else if $currentTab === 'policy'}
      <PolicyTab policy={detail.policy} />
    {:else if $currentTab === 'sources'}
      <SourcesTab {agg} />
    {/if}
  </div>
{/if}
