<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.js';
  import { navigate, serviceUrl, diffUrl } from '../lib/router.js';
  import GraphCanvas from '../GraphCanvas.svelte';

  let { name, services = [], onServiceResolved } = $props();

  let loading = $state(true);
  let error = $state(null);
  let detail = $state(null);
  let versions = $state([]);
  let dependents = $state([]);
  let crossRefs = $state(null);
  let graphData = $state(null);
  let resolving = $state(false);
  let resolveError = $state(null);

  // Which sections are open
  let openSections = $state({
    overview: true, interfaces: true, dependencies: true,
    config: false, policy: false, runtime: false, validation: false,
    runtimeDiff: false, observed: false,
  });

  function toggle(section) {
    openSections = { ...openSections, [section]: !openSections[section] };
  }

  // Computed
  let insights = $derived(detail?.insights || []);
  let hasInterfaces = $derived((detail?.interfaces?.length || 0) > 0);
  let hasDeps = $derived((detail?.dependencies?.length || 0) > 0);
  let hasConfig = $derived(!!detail?.configuration);
  let hasPolicy = $derived(!!detail?.policy);
  let hasRuntime = $derived(!!detail?.runtime);
  let hasValidation = $derived(
    (detail?.validation?.errors?.length > 0) ||
    (detail?.validation?.warnings?.length > 0) ||
    (detail?.conditions?.length > 0)
  );
  let hasRuntimeDiff = $derived((detail?.runtimeDiff?.length || 0) > 0);
  let hasObserved = $derived(!!detail?.observedRuntime);
  let cfg = $derived(detail?.configuration);
  let pol = $derived(detail?.policy);
  let obs = $derived(detail?.observedRuntime);
  let sources = $derived(() => {
    const s = detail?.sources || [];
    if (s.length) return s;
    return detail?.source ? [detail.source] : [];
  });

  async function load() {
    loading = true;
    error = null;
    resolveError = null;
    try {
      detail = await api.service(name);
      loading = false;

      // Background loads
      const [vers, deps, refs, graph] = await Promise.all([
        api.versions(name).catch(() => []),
        api.dependents(name).catch(() => []),
        api.crossRefs(name).catch(() => null),
        api.graph().catch(() => null),
      ]);
      versions = vers || [];
      dependents = deps || [];
      crossRefs = refs;
      graphData = graph;
    } catch (e) {
      if (e.status === 404) {
        // Try to find ref info from services list for lazy resolution
        const depInfo = findDepRef(name);
        if (depInfo) {
          await resolveRemote(depInfo.ref, depInfo.compatibility);
          return;
        }
      }
      error = e.message;
      loading = false;
    }
  }

  function findDepRef(targetName) {
    // Look in services list for any service that has targetName as a dependency
    // This is a simplified approach - the real resolution happens server-side
    return null;
  }

  async function resolveRemote(ref, compatibility) {
    resolving = true;
    try {
      detail = await api.resolve(ref, compatibility || '');
      if (detail.name && detail.name !== name) {
        navigate('detail', { name: detail.name });
        return;
      }
      if (onServiceResolved) onServiceResolved();
    } catch (e) {
      resolveError = { title: resolveErrorTitle(e.status), message: e.message, ref };
    }
    resolving = false;
    loading = false;
  }

  function resolveErrorTitle(status) {
    if (status === 403) return 'Authentication failed';
    if (status === 404) return 'Artifact not found';
    if (status === 422) return 'Invalid reference';
    if (status === 502) return 'Registry unreachable';
    return 'Failed to resolve';
  }

  function phaseClass(phase) {
    if (phase === 'Healthy') return 'ok';
    if (phase === 'Degraded') return 'warn';
    if (phase === 'Invalid') return 'err';
    return 'neutral';
  }

  function complianceClass(score) {
    if (score >= 80) return 'score-ok';
    if (score >= 50) return 'score-warn';
    return 'score-err';
  }

  function methodClass(method) {
    const m = method?.toUpperCase();
    if (m === 'GET') return 'badge-ok';
    if (m === 'POST') return 'badge-info';
    if (m === 'PUT' || m === 'PATCH') return 'badge-warn';
    if (m === 'DELETE') return 'badge-err';
    return 'badge-neutral';
  }

  function svcExists(svcName) {
    return services.some((s) => s.name === svcName);
  }

  onMount(() => { load(); });
</script>

{#if resolving}
  <div class="state-box"><div class="spinner"></div><h3>Resolving remote dependency…</h3></div>
{:else if resolveError}
  <div class="state-box">
    <h3>{resolveError.title}</h3>
    <p>{resolveError.message}</p>
    <code>{resolveError.ref}</code>
    <a href="#/" class="btn" style="margin-top:12px">Back to overview</a>
  </div>
{:else if loading}
  <div class="state-box"><div class="spinner"></div><p>Loading…</p></div>
{:else if error}
  <div class="state-box">
    <h3>Service not found</h3>
    <p>{error}</p>
    <a href="#/" class="btn" style="margin-top:12px">Back to overview</a>
  </div>
{:else if detail}

  <!-- Breadcrumb -->
  <nav class="breadcrumb" aria-label="Breadcrumb">
    <a href="#/">Services</a>
    <span class="sep">/</span>
    <span>{detail.name}</span>
  </nav>

  <!-- Header -->
  <header class="detail-header">
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
      {#each sources() as src}
        <span class="source-dot source-dot-{src}" title={src}></span>
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

  <!-- Overview: conditions, runtime, scaling -->
  <section class="section">
    <button type="button" class="section-toggle" onclick={() => toggle('overview')}>
      <span class="section-title">Overview</span>
      <span class="toggle-icon">{openSections.overview ? '−' : '+'}</span>
    </button>
    {#if openSections.overview}
      <div class="section-body">
        <!-- Conditions -->
        {#if detail.conditions?.length > 0}
          <div class="subsection">
            <h3>Conditions</h3>
            <div class="table-wrap">
              <table>
                <thead><tr><th>Check</th><th>Status</th><th>Reason</th><th>Message</th></tr></thead>
                <tbody>
                  {#each detail.conditions as cond}
                    <tr>
                      <td><strong>{cond.type}</strong></td>
                      <td>
                        {#if cond.status === 'True'}
                          <span class="badge badge-ok">Pass</span>
                        {:else if cond.status === 'False'}
                          <span class="badge badge-{cond.severity === 'warning' ? 'warn' : 'err'}">Fail</span>
                        {:else}
                          <span class="badge badge-neutral">{cond.status}</span>
                        {/if}
                      </td>
                      <td class="text-2">{cond.reason || '—'}</td>
                      <td class="text-2">{cond.message || '—'}</td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          </div>
        {/if}

        <!-- Runtime + Scaling cards -->
        <div class="cards-row">
          {#if hasRuntime}
            <div class="card">
              <h3>Runtime</h3>
              <dl class="kv-grid">
                {#if detail.runtime.workload}<dt>Workload</dt><dd>{detail.runtime.workload}</dd>{/if}
                {#if detail.runtime.stateType}<dt>State</dt><dd>{detail.runtime.stateType}</dd>{/if}
                {#if detail.runtime.upgradeStrategy}<dt>Upgrade</dt><dd>{detail.runtime.upgradeStrategy}</dd>{/if}
                {#if detail.runtime.gracefulShutdownSeconds != null}<dt>Graceful shutdown</dt><dd>{detail.runtime.gracefulShutdownSeconds}s</dd>{/if}
                {#if detail.runtime.healthPath}<dt>Health</dt><dd>{detail.runtime.healthInterface}:{detail.runtime.healthPath}</dd>{/if}
                {#if detail.runtime.metricsPath}<dt>Metrics</dt><dd>{detail.runtime.metricsInterface}:{detail.runtime.metricsPath}</dd>{/if}
                {#if detail.runtime.persistenceScope}<dt>Persistence</dt><dd>{detail.runtime.persistenceScope} / {detail.runtime.persistenceDurability || '—'}</dd>{/if}
                {#if detail.runtime.dataCriticality}<dt>Data criticality</dt><dd>{detail.runtime.dataCriticality}</dd>{/if}
              </dl>
            </div>
          {/if}
          {#if detail.scaling}
            <div class="card">
              <h3>Scaling</h3>
              <dl class="kv-grid">
                {#if detail.scaling.replicas != null}<dt>Replicas</dt><dd>{detail.scaling.replicas}</dd>{/if}
                {#if detail.scaling.min != null}<dt>Min</dt><dd>{detail.scaling.min}</dd>{/if}
                {#if detail.scaling.max != null}<dt>Max</dt><dd>{detail.scaling.max}</dd>{/if}
              </dl>
            </div>
          {/if}
          {#if detail.metadata && Object.keys(detail.metadata).length > 0}
            <div class="card">
              <h3>Metadata</h3>
              <dl class="kv-grid">
                {#each Object.entries(detail.metadata) as [k, v]}
                  <dt>{k}</dt><dd>{v}</dd>
                {/each}
              </dl>
            </div>
          {/if}
        </div>
      </div>
    {/if}
  </section>

  <!-- Interfaces -->
  {#if hasInterfaces}
    <section class="section">
      <button type="button" class="section-toggle" onclick={() => toggle('interfaces')}>
        <span class="section-title">Interfaces <span class="tab-count">{detail.interfaces.length}</span></span>
        <span class="toggle-icon">{openSections.interfaces ? '−' : '+'}</span>
      </button>
      {#if openSections.interfaces}
        <div class="section-body">
          {#each detail.interfaces as iface}
            <div class="card iface-card">
              <div class="iface-header">
                <strong>{iface.name}</strong>
                <span class="badge badge-info">{iface.type}</span>
                {#if iface.port != null}<span class="pill">:{iface.port}</span>{/if}
                {#if iface.visibility}<span class="pill">{iface.visibility}</span>{/if}
                {#if iface.hasContractFile}<span class="pill" title={iface.contractFile}>has contract</span>{/if}
              </div>
              {#if iface.endpoints?.length > 0}
                <div class="table-wrap">
                  <table>
                    <thead><tr><th>Method</th><th>Path</th><th>Summary</th></tr></thead>
                    <tbody>
                      {#each iface.endpoints as ep}
                        <tr>
                          <td><span class="badge {methodClass(ep.method)}">{ep.method}</span></td>
                          <td><code>{ep.path}</code></td>
                          <td class="text-2">{ep.summary || ''}</td>
                        </tr>
                      {/each}
                    </tbody>
                  </table>
                </div>
              {/if}
              {#if iface.contractContent}
                <details class="contract-content">
                  <summary>Contract content</summary>
                  <pre>{iface.contractContent}</pre>
                </details>
              {/if}
            </div>
          {/each}
        </div>
      {/if}
    </section>
  {/if}

  <!-- Dependencies -->
  {#if hasDeps || dependents.length > 0 || crossRefs}
    <section class="section">
      <button type="button" class="section-toggle" onclick={() => toggle('dependencies')}>
        <span class="section-title">Dependencies <span class="tab-count">{(detail.dependencies?.length || 0) + dependents.length}</span></span>
        <span class="toggle-icon">{openSections.dependencies ? '−' : '+'}</span>
      </button>
      {#if openSections.dependencies}
        <div class="section-body">
          <!-- Dependency graph -->
          {#if graphData}
            <div class="dep-graph-box">
              <GraphCanvas {graphData} focusId={name} height={300} onNavigate={(n) => navigate('detail', { name: n })} />
            </div>
          {/if}

          <!-- Depends on -->
          {#if detail.dependencies?.length > 0}
            <div class="subsection">
              <h3>Depends on</h3>
              <div class="table-wrap">
                <table>
                  <thead><tr><th>Service</th><th>Ref</th><th>Required</th><th>Compatibility</th></tr></thead>
                  <tbody>
                    {#each detail.dependencies as dep}
                      <tr>
                        <td>
                          {#if svcExists(dep.name)}
                            <a href={serviceUrl(dep.name)}>{dep.name}</a>
                          {:else}
                            {dep.name} <span class="badge badge-neutral">external</span>
                          {/if}
                        </td>
                        <td><code class="text-3">{dep.ref}</code></td>
                        <td>{dep.required ? 'Yes' : 'No'}</td>
                        <td>{dep.compatibility || '—'}</td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
            </div>
          {/if}

          <!-- Dependents -->
          {#if dependents.length > 0}
            <div class="subsection">
              <h3>Depended on by</h3>
              <div class="table-wrap">
                <table>
                  <thead><tr><th>Service</th><th>Phase</th><th>Required</th></tr></thead>
                  <tbody>
                    {#each dependents as dep}
                      <tr>
                        <td><a href={serviceUrl(dep.name)}>{dep.name}</a></td>
                        <td><span class="badge badge-{phaseClass(dep.phase)}"><span class="badge-dot"></span>{dep.phase}</span></td>
                        <td>{dep.required ? 'Yes' : 'No'}</td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
            </div>
          {/if}

          <!-- Cross-references -->
          {#if crossRefs?.references?.length > 0 || crossRefs?.referencedBy?.length > 0}
            <div class="subsection">
              <h3>Cross-references</h3>
              {#if crossRefs.references?.length > 0}
                <p class="text-2" style="margin-bottom:8px">References:</p>
                <div class="table-wrap">
                  <table>
                    <thead><tr><th>Service</th><th>Type</th><th>Phase</th></tr></thead>
                    <tbody>
                      {#each crossRefs.references as ref}
                        <tr>
                          <td><a href={serviceUrl(ref.name)}>{ref.name}</a></td>
                          <td><span class="pill">{ref.refType}</span></td>
                          <td><span class="badge badge-{phaseClass(ref.phase)}"><span class="badge-dot"></span>{ref.phase || 'Unknown'}</span></td>
                        </tr>
                      {/each}
                    </tbody>
                  </table>
                </div>
              {/if}
              {#if crossRefs.referencedBy?.length > 0}
                <p class="text-2" style="margin: 12px 0 8px">Referenced by:</p>
                <div class="table-wrap">
                  <table>
                    <thead><tr><th>Service</th><th>Type</th><th>Phase</th></tr></thead>
                    <tbody>
                      {#each crossRefs.referencedBy as ref}
                        <tr>
                          <td><a href={serviceUrl(ref.name)}>{ref.name}</a></td>
                          <td><span class="pill">{ref.refType}</span></td>
                          <td><span class="badge badge-{phaseClass(ref.phase)}"><span class="badge-dot"></span>{ref.phase || 'Unknown'}</span></td>
                        </tr>
                      {/each}
                    </tbody>
                  </table>
                </div>
              {/if}
            </div>
          {/if}
        </div>
      {/if}
    </section>
  {/if}

  <!-- Configuration -->
  {#if hasConfig}
    <section class="section">
      <button type="button" class="section-toggle" onclick={() => toggle('config')}>
        <span class="section-title">Configuration</span>
        <span class="toggle-icon">{openSections.config ? '−' : '+'}</span>
      </button>
      {#if openSections.config}
        <div class="section-body">

          {#if cfg.schema}<p class="text-2">Schema: <code>{cfg.schema}</code></p>{/if}
          {#if cfg.ref}
            <p class="text-2">Ref: <a href={serviceUrl(cfg.ref.split('/').pop().split(':')[0])}>{cfg.ref}</a></p>
          {/if}
          {#if cfg.values?.length > 0}
            <div class="table-wrap">
              <table>
                <thead><tr><th>Key</th><th>Value</th><th>Type</th></tr></thead>
                <tbody>
                  {#each cfg.values as v}
                    <tr>
                      <td><code>{v.key}</code></td>
                      <td>{v.value === '(any)' ? '—' : v.value}</td>
                      <td><span class="pill">{v.type}</span></td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          {:else if cfg.valueKeys?.length > 0}
            <div class="table-wrap">
              <table>
                <thead><tr><th>Key</th></tr></thead>
                <tbody>
                  {#each cfg.valueKeys as key}
                    <tr><td><code>{key}</code></td></tr>
                  {/each}
                </tbody>
              </table>
            </div>
          {/if}
          {#if cfg.secretKeys?.length > 0}
            <div class="subsection">
              <h3>Secret Keys</h3>
              <div class="table-wrap">
                <table>
                  <thead><tr><th>Key</th><th>Type</th></tr></thead>
                  <tbody>
                    {#each cfg.secretKeys as key}
                      <tr><td><code>{key}</code></td><td><span class="pill" style="color:var(--c-warn)">secret</span></td></tr>
                    {/each}
                  </tbody>
                </table>
              </div>
            </div>
          {/if}
        </div>
      {/if}
    </section>
  {/if}

  <!-- Policy -->
  {#if hasPolicy}
    <section class="section">
      <button type="button" class="section-toggle" onclick={() => toggle('policy')}>
        <span class="section-title">Policy</span>
        <span class="toggle-icon">{openSections.policy ? '−' : '+'}</span>
      </button>
      {#if openSections.policy}
        <div class="section-body">

          {#if pol.schema}<p class="text-2">Schema: <code>{pol.schema}</code></p>{/if}
          {#if pol.ref}
            <p class="text-2">Ref: <a href={serviceUrl(pol.ref.split('/').pop().split(':')[0])}>{pol.ref}</a></p>
          {/if}
          {#if pol.values?.length > 0}
            <div class="table-wrap">
              <table>
                <thead><tr><th>Key</th><th>Value</th><th>Type</th></tr></thead>
                <tbody>
                  {#each pol.values as v}
                    <tr>
                      <td><code>{v.key}</code></td>
                      <td>{v.value === '(any)' ? '—' : v.value}</td>
                      <td><span class="pill">{v.type}</span></td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          {/if}
          {#if pol.content}
            <details><summary>Raw content</summary><pre>{pol.content}</pre></details>
          {/if}
        </div>
      {/if}
    </section>
  {/if}

  <!-- Validation -->
  {#if hasValidation}
    <section class="section">
      <button type="button" class="section-toggle" onclick={() => toggle('validation')}>
        <span class="section-title">Validation</span>
        <span class="toggle-icon">{openSections.validation ? '−' : '+'}</span>
      </button>
      {#if openSections.validation}
        <div class="section-body">
          {#if detail.validation?.errors?.length > 0}
            <div class="subsection">
              <h3 style="color:var(--c-err)">Errors</h3>
              {#each detail.validation.errors as issue}
                <div class="insight insight-critical">
                  <code>{issue.path}</code> <strong>[{issue.code}]</strong> {issue.message}
                </div>
              {/each}
            </div>
          {/if}
          {#if detail.validation?.warnings?.length > 0}
            <div class="subsection">
              <h3 style="color:var(--c-warn)">Warnings</h3>
              {#each detail.validation.warnings as issue}
                <div class="insight insight-warning">
                  <code>{issue.path}</code> <strong>[{issue.code}]</strong> {issue.message}
                </div>
              {/each}
            </div>
          {/if}
        </div>
      {/if}
    </section>
  {/if}

  <!-- Runtime Diff -->
  {#if hasRuntimeDiff}
    <section class="section">
      <button type="button" class="section-toggle" onclick={() => toggle('runtimeDiff')}>
        <span class="section-title">Contract vs Runtime</span>
        <span class="toggle-icon">{openSections.runtimeDiff ? '−' : '+'}</span>
      </button>
      {#if openSections.runtimeDiff}
        <div class="section-body">
          <div class="table-wrap">
            <table>
              <thead><tr><th>Field</th><th>Declared</th><th>Observed</th><th>Status</th></tr></thead>
              <tbody>
                {#each detail.runtimeDiff as row}
                  <tr>
                    <td><strong>{row.field}</strong><br><code class="text-3">{row.contractPath}</code></td>
                    <td>{row.declaredValue || '—'}</td>
                    <td>{row.observedValue || '—'}</td>
                    <td>
                      {#if row.status === 'match'}<span class="badge badge-ok">Match</span>
                      {:else if row.status === 'mismatch'}<span class="badge badge-err">Mismatch</span>
                      {:else}<span class="badge badge-neutral">{row.status}</span>
                      {/if}
                    </td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        </div>
      {/if}
    </section>
  {/if}

  <!-- Observed Runtime -->
  {#if hasObserved}
    <section class="section">
      <button type="button" class="section-toggle" onclick={() => toggle('observed')}>
        <span class="section-title">Observed Runtime</span>
        <span class="toggle-icon">{openSections.observed ? '−' : '+'}</span>
      </button>
      {#if openSections.observed}
        <div class="section-body">

          <div class="card">
            <dl class="kv-grid">
              {#if obs.workloadKind}<dt>Workload kind</dt><dd>{obs.workloadKind}</dd>{/if}
              {#if obs.deploymentStrategy}<dt>Strategy</dt><dd>{obs.deploymentStrategy}</dd>{/if}
              {#if obs.containerImages?.length > 0}<dt>Images</dt><dd>{obs.containerImages.join(', ')}</dd>{/if}
              {#if obs.hasPVC != null}<dt>Has PVC</dt><dd>{obs.hasPVC ? 'Yes' : 'No'}</dd>{/if}
              {#if obs.hasEmptyDir != null}<dt>Has EmptyDir</dt><dd>{obs.hasEmptyDir ? 'Yes' : 'No'}</dd>{/if}
              {#if obs.terminationGracePeriodSeconds != null}<dt>Termination grace</dt><dd>{obs.terminationGracePeriodSeconds}s</dd>{/if}
              {#if obs.healthProbeInitialDelaySeconds != null}<dt>Health probe delay</dt><dd>{obs.healthProbeInitialDelaySeconds}s</dd>{/if}
            </dl>
          </div>
        </div>
      {/if}
    </section>
  {/if}

  <!-- Version History -->
  {#if versions.length > 0}
    <section class="section">
      <div class="section-title">Version History <span class="tab-count">{versions.length}</span></div>
      <div class="table-wrap">
        <table>
          <thead><tr><th>Version</th><th>Classification</th><th>Source</th><th>Created</th></tr></thead>
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
                <td>{#if ver.source}<span class="source-dot source-dot-{ver.source}" title={ver.source}></span>{:else}—{/if}</td>
                <td class="text-2">{ver.createdAt ? new Date(ver.createdAt).toLocaleDateString() : '—'}</td>
              </tr>
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

  .detail-header { margin-bottom: var(--sp-5); }
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

  .section-toggle {
    display: flex; align-items: center; justify-content: space-between;
    width: 100%; background: none; border: none; padding: 0; cursor: pointer;
    font: inherit; color: var(--c-text); text-align: left;
  }
  .section-toggle:hover .section-title { color: var(--c-accent); }
  .toggle-icon { color: var(--c-text-3); font-size: var(--text-lg); }

  .section-body { margin-top: var(--sp-3); }
  .subsection { margin-top: var(--sp-4); }
  .subsection h3 { margin-bottom: var(--sp-2); }

  .cards-row { display: flex; flex-wrap: wrap; gap: var(--sp-3); margin-top: var(--sp-3); }
  .cards-row .card { flex: 1; min-width: 240px; }
  .cards-row .card h3 { margin-bottom: var(--sp-2); }

  .iface-card { margin-bottom: var(--sp-3); }
  .iface-header { display: flex; align-items: center; gap: var(--sp-2); margin-bottom: var(--sp-2); flex-wrap: wrap; }

  .contract-content { margin-top: var(--sp-2); }
  .contract-content summary { cursor: pointer; color: var(--c-text-3); font-size: var(--text-sm); }

  .dep-graph-box {
    border: 1px solid var(--c-border);
    border-radius: var(--radius-sm);
    margin-bottom: var(--sp-4);
    overflow: hidden;
  }

  .text-2 { color: var(--c-text-2); }
  .text-3 { color: var(--c-text-3); }
  .text-err { color: var(--c-err); font-size: var(--text-xs); }
</style>
