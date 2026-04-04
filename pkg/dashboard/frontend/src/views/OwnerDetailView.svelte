<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.ts';
  import { serviceUrl, ownersUrl } from '../lib/router.ts';
  import { statusClass, complianceStatusClass, complianceClass, ownerKey, sourceTooltip, extractOwnerDetail } from '../lib/format.ts';
  import GraphCanvas from '../GraphCanvas.svelte';

  let { owner = '', services = [], initialLoading = false } = $props();

  let graphData = $state(null);
  let graphLoading = $state(true);
  let expanded = $state({});

  function toggle(i) {
    expanded = { ...expanded, [i]: !expanded[i] };
  }

  // Services belonging to this owner
  let ownerServices = $derived(
    services.filter((s) => (ownerKey(s.owner) || '(unowned)') === owner)
  );

  // Status summary
  let summary = $derived.by(() => {
    let compliant = 0, warning = 0, nonCompliant = 0, reference = 0, unknown = 0;
    for (const s of ownerServices) {
      const st = s.contractStatus;
      if (st === 'Compliant') compliant++;
      else if (st === 'Warning') warning++;
      else if (st === 'NonCompliant') nonCompliant++;
      else if (st === 'Reference') reference++;
      else unknown++;
    }
    const assessed = compliant + warning + nonCompliant;
    const compliancePercent = assessed > 0 ? Math.round((compliant / assessed) * 100) : -1;
    return { compliant, warning, nonCompliant, reference, unknown, total: ownerServices.length, compliancePercent };
  });

  // Structured owner detail extracted from services
  let ownerDetail = $derived(extractOwnerDetail(owner, ownerServices));

  // Set of service names for graph focusing (passed to GraphCanvas as focusNodes)
  let ownerServiceNames = $derived(new Set(ownerServices.map((s) => s.name)));

  onMount(async () => {
    try {
      graphData = await api.graph();
    } catch {}
    graphLoading = false;
  });
</script>

<!-- Breadcrumb -->
<nav class="breadcrumb fade-in" aria-label="Breadcrumb">
  <a href="#/">Services</a>
  <span class="sep">/</span>
  <a href={ownersUrl()}>Owners</a>
  <span class="sep">/</span>
  <span>{owner}</span>
</nav>

<header class="detail-header fade-in-up">
  <h1>{owner}</h1>
  <span class="text-2">{summary.total} service{summary.total !== 1 ? 's' : ''}</span>
</header>

<!-- Ownership metadata -->
{#if ownerDetail.isStructured}
  <div class="owner-meta fade-in-up">
    <div class="meta-row">
      {#if ownerDetail.team}
        <div class="meta-item">
          <span class="meta-label">Team</span>
          <span class="meta-value">{ownerDetail.team}</span>
        </div>
      {/if}
      {#if ownerDetail.dri}
        <div class="meta-item">
          <span class="meta-label">DRI{ownerDetail.driConflict ? ' (inconsistent)' : ''}</span>
          {#if ownerDetail.driConflict}
            <span class="meta-value dri-conflict">{ownerDetail.allDris.join(', ')}</span>
          {:else}
            <span class="meta-value">{ownerDetail.dri}</span>
          {/if}
        </div>
      {/if}
    </div>
    {#if ownerDetail.contacts.length > 0}
      <div class="meta-contacts">
        <span class="meta-label">Contacts</span>
        <div class="contact-list">
          {#each ownerDetail.contacts as contact}
            <span class="contact-pill">
              <span class="contact-type">{contact.type}</span>
              <span class="contact-value">{contact.value}</span>
              {#if contact.purpose}<span class="contact-purpose">{contact.purpose}</span>{/if}
            </span>
          {/each}
        </div>
      </div>
    {/if}
  </div>
{/if}

<!-- Status summary cards -->
{#if summary.total > 0}
  <div class="summary-cards fade-in-up">
    {#if summary.compliant > 0}
      <div class="summary-card card-ok">
        <span class="summary-count">{summary.compliant}</span>
        <span class="summary-label">Compliant</span>
      </div>
    {/if}
    {#if summary.warning > 0}
      <div class="summary-card card-warn">
        <span class="summary-count">{summary.warning}</span>
        <span class="summary-label">Warning</span>
      </div>
    {/if}
    {#if summary.nonCompliant > 0}
      <div class="summary-card card-err">
        <span class="summary-count">{summary.nonCompliant}</span>
        <span class="summary-label">Non-Compliant</span>
      </div>
    {/if}
    {#if summary.reference > 0}
      <div class="summary-card card-neutral">
        <span class="summary-count">{summary.reference}</span>
        <span class="summary-label">Reference</span>
      </div>
    {/if}
    {#if summary.compliancePercent >= 0}
      <div class="summary-card">
        <span class="summary-count score {complianceClass(summary.compliancePercent)}">{summary.compliancePercent}%</span>
        <span class="summary-label">Compliant</span>
      </div>
    {/if}
  </div>
{/if}

<!-- Services list -->
{#if ownerServices.length > 0}
  <div class="section">
    <div class="section-title">Services <span class="tab-count">{ownerServices.length}</span></div>
    <div class="fade-in-up">
      {#each ownerServices as svc, i}
        <div class="detail-card">
          <button type="button" class="detail-card-header expandable" onclick={() => toggle(i)}>
            <div class="detail-card-header-left">
              <span class="expand-icon" class:open={expanded[i]}>
                <svg viewBox="0 0 12 12" fill="none"><path d="M3 4.5L6 7.5L9 4.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>
              </span>
              <span class="badge badge-{statusClass(svc.contractStatus)}"><span class="badge-dot"></span>{svc.contractStatus}</span>
              <span class="detail-card-title">{svc.name}</span>
              {#if svc.version}<span class="pill">{svc.version}</span>{/if}
            </div>
            <!-- svelte-ignore a11y_no_static_element_interactions -->
            <a href={serviceUrl(svc.name)} class="ref-link" onclick={(e) => e.stopPropagation()}>
              View details →
            </a>
          </button>

          {#if expanded[i]}
            <div class="detail-card-body">
              <div class="svc-detail-grid">
                {#if svc.complianceScore != null}
                  <div class="svc-detail-item">
                    <span class="svc-detail-label">Compliance</span>
                    <span class="score {complianceStatusClass(svc.complianceStatus)}">{svc.complianceScore}%</span>
                  </div>
                {/if}
                {#if (svc.blastRadius || 0) > 0}
                  <div class="svc-detail-item">
                    <span class="svc-detail-label">Blast radius</span>
                    <span class="blast-badge" class:blast-low={svc.blastRadius < 3} class:blast-med={svc.blastRadius >= 3 && svc.blastRadius < 5} class:blast-high={svc.blastRadius >= 5}>{svc.blastRadius}</span>
                  </div>
                {/if}
                {#if svc.checksPassed != null}
                  <div class="svc-detail-item">
                    <span class="svc-detail-label">Checks</span>
                    <span>{svc.checksPassed}/{svc.checksTotal}{#if svc.checksFailed > 0} <span class="text-err">({svc.checksFailed} failed)</span>{/if}</span>
                  </div>
                {/if}
                <div class="svc-detail-item">
                  <span class="svc-detail-label">Source</span>
                  <span>
                    {#each (svc.sources || [svc.source]) as src}
                      <span class="source-dot source-dot-{src}" data-tip={sourceTooltip(src)}></span>
                      <span class="text-3" style="font-size:var(--text-xs)">{src}</span>
                    {/each}
                  </span>
                </div>
              </div>
            </div>
          {/if}
        </div>
      {/each}
    </div>
  </div>
{:else}
  <div class="state-box">
    {#if initialLoading}
      <div class="skeleton-table fade-in">
        {#each Array(3) as _}
          <div class="skeleton-row">
            <div class="skeleton skeleton-line" style="width:30%"></div>
            <div class="skeleton skeleton-line" style="width:15%"></div>
            <div class="skeleton skeleton-line" style="width:20%"></div>
          </div>
        {/each}
      </div>
      <p style="margin-top:var(--sp-3); color:var(--c-text-3)">Loading services…</p>
    {:else}
      <h3>No services</h3>
      <p>No services found for owner "{owner}".</p>
    {/if}
  </div>
{/if}

<!-- Owner graph -->
{#if ownerServiceNames.size > 0}
  <div class="section" style="margin-top:var(--sp-5)">
    <div class="section-title">Dependency Graph</div>
    {#if graphLoading}
      <div class="fade-in" style="padding:var(--sp-3) 0">
        <div class="skeleton" style="width:100%; height:300px; border-radius:var(--radius-sm)"></div>
        <p class="text-3" style="font-size:var(--text-xs); margin-top:var(--sp-2)">Loading dependency graph…</p>
      </div>
    {:else if graphData?.nodes?.length > 0}
      <p class="text-3" style="font-size:var(--text-xs); margin-bottom:var(--sp-3)">Services owned by {owner} are highlighted; others are dimmed.</p>
      <div class="graph-wrap">
        <GraphCanvas
          {graphData}
          focusNodes={ownerServiceNames}
          height={Math.min(window.innerHeight - 300, 500)}
          onNavigate={(name) => location.hash = serviceUrl(name)}
        />
      </div>
    {:else}
      <p class="text-3" style="font-size:var(--text-xs)">No dependency data available.</p>
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

  .detail-header {
    display: flex; align-items: center; gap: var(--sp-3);
    margin-bottom: var(--sp-5); flex-wrap: wrap;
  }

  .summary-cards {
    display: flex; gap: var(--sp-3); margin-bottom: var(--sp-5); flex-wrap: wrap;
  }
  .summary-card {
    display: flex; flex-direction: column; align-items: center;
    padding: var(--sp-3) var(--sp-4);
    border-radius: var(--radius-sm);
    background: var(--c-surface); border: 1px solid var(--c-border);
    min-width: 80px;
  }
  .summary-count { font-size: 1.25rem; font-weight: 700; }
  .summary-label { font-size: var(--text-xs); color: var(--c-text-3); margin-top: 2px; }
  .card-ok { border-color: var(--c-ok-border); }
  .card-ok .summary-count { color: var(--c-ok); }
  .card-warn { border-color: var(--c-warn-border); }
  .card-warn .summary-count { color: var(--c-warn); }
  .card-err { border-color: var(--c-err-border); }
  .card-err .summary-count { color: var(--c-err); }
  .card-neutral .summary-count { color: var(--c-text-3); }

  .text-2 { color: var(--c-text-2); }
  .text-3 { color: var(--c-text-3); }
  .text-warn { color: var(--c-warn); }
  .text-err { color: var(--c-err); }

  .blast-badge {
    display: inline-flex; align-items: center; justify-content: center;
    min-width: 26px; height: 22px; padding: 0 7px;
    border-radius: var(--radius-xs);
    font-size: var(--text-xs); font-weight: 600;
  }
  .blast-low { background: var(--c-warn-bg); color: var(--c-warn); }
  .blast-med { background: var(--c-warn-bg); color: var(--c-warn); border: 1px solid color-mix(in srgb, var(--c-warn) 25%, transparent); }
  .blast-high { background: var(--c-err-bg); color: var(--c-err); border: 1px solid color-mix(in srgb, var(--c-err) 25%, transparent); }

  /* ── Owner metadata ── */
  .owner-meta {
    background: var(--c-surface); border: 1px solid var(--c-border);
    border-radius: var(--radius-sm); padding: var(--sp-4);
    margin-bottom: var(--sp-5);
  }
  .meta-row {
    display: flex; gap: var(--sp-5); flex-wrap: wrap;
    margin-bottom: var(--sp-1);
  }
  .meta-item { display: flex; flex-direction: column; gap: 2px; }
  .meta-label {
    font-size: var(--text-xs); font-weight: 500; text-transform: uppercase;
    letter-spacing: 0.05em; color: var(--c-text-3);
  }
  .meta-value { font-size: var(--text-sm); font-weight: 600; color: var(--c-text); }
  .meta-contacts { margin-top: var(--sp-3); }
  .contact-list { display: flex; flex-wrap: wrap; gap: var(--sp-2); margin-top: var(--sp-1); }
  .contact-pill {
    display: inline-flex; align-items: center; gap: 6px;
    padding: 4px 10px; border-radius: var(--radius-xs);
    background: var(--c-surface-inset); border: 1px solid var(--c-border);
    font-size: var(--text-xs);
  }
  .contact-type {
    font-weight: 600; text-transform: uppercase;
    color: var(--c-text-3); font-size: 10px; letter-spacing: 0.03em;
  }
  .contact-value { color: var(--c-text); }
  .contact-purpose {
    color: var(--c-text-3); font-style: italic;
  }
  .contact-purpose::before { content: '· '; }
  .dri-conflict { color: var(--c-warn); }

  /* ── Detail cards (unified pattern) ── */
  .detail-card {
    border: 1px solid var(--c-border);
    border-radius: var(--radius-sm);
    background: var(--c-surface);
    margin-bottom: var(--sp-2);
  }
  .detail-card-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    width: 100%;
    padding: var(--sp-3);
    background: none;
    border: none;
    font: inherit;
    color: var(--c-text);
    text-align: left;
    gap: var(--sp-3);
  }
  .detail-card-header.expandable { cursor: pointer; }
  .detail-card-header.expandable:hover { background: var(--c-surface-hover, var(--c-surface-inset)); border-radius: var(--radius-sm); }
  .detail-card-header-left {
    display: flex;
    align-items: center;
    gap: var(--sp-2);
    min-width: 0;
  }
  .expand-icon {
    display: inline-flex;
    color: var(--c-text-3);
    transition: transform 200ms ease;
    transform: rotate(-90deg);
    flex-shrink: 0;
  }
  .expand-icon.open { transform: rotate(0deg); }
  .expand-icon svg { width: 12px; height: 12px; }
  .detail-card-title { font-weight: 600; }
  .ref-link {
    font-size: var(--text-xs);
    color: var(--c-accent);
    text-decoration: none;
    white-space: nowrap;
    flex-shrink: 0;
  }
  .ref-link:hover { text-decoration: underline; }
  .detail-card-body {
    padding: 0 var(--sp-3) var(--sp-3);
    animation: slideReveal 200ms ease-out both;
  }

  .svc-detail-grid {
    display: flex;
    flex-wrap: wrap;
    gap: var(--sp-3) var(--sp-5);
  }
  .svc-detail-item {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .svc-detail-label {
    font-size: var(--text-xs);
    font-weight: 500;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--c-text-3);
  }

  .graph-wrap { position: relative; }

  .skeleton-table { width: 100%; max-width: 600px; }
  .skeleton-row { display: flex; gap: var(--sp-3); margin-bottom: var(--sp-3); }
  .skeleton-row .skeleton-line { height: 18px; border-radius: var(--radius-xs); }

  @keyframes slideReveal {
    from { opacity: 0; transform: translateY(-4px); }
    to { opacity: 1; transform: translateY(0); }
  }

  @media (max-width: 768px) {
    .summary-cards { gap: var(--sp-2); }
    .summary-card { min-width: 60px; padding: var(--sp-2) var(--sp-3); }
  }
</style>
