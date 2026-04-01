<script>
  import { navigate, serviceUrl, ownerUrl } from './lib/router.ts';
  import { statusClass, sourceTooltip, ownerMatchesFilter, ownerKey, ownerDisplay, complianceClass } from './lib/format.ts';

  let {
    services = [], sourcesInfo = [], version = '', discovering = false,
    autoReload = false, refreshing = false, onRefresh, onToggleAutoReload, onToggleTheme,
  } = $props();

  let query = $state('');
  let showResults = $state(false);
  let selectedIdx = $state(-1);
  let searchInputEl = $state(null);
  let mobileMenuOpen = $state(false);

  function handleGlobalKeydown(e) {
    if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
      e.preventDefault();
      searchInputEl?.focus();
      showResults = !!query;
    }
  }

  let matches = $derived.by(() => {
    if (!query) return [];
    const q = query.toLowerCase();
    return services
      .filter((s) => s.name.toLowerCase().includes(q) || ownerMatchesFilter(s.owner, q))
      .slice(0, 8);
  });

  // Unique owners matching the query, with summary stats
  let ownerMatches = $derived.by(() => {
    if (!query) return [];
    const q = query.toLowerCase();
    // Build per-owner aggregation
    const agg = new Map();
    for (const s of services) {
      const key = ownerKey(s.owner);
      if (!key) continue;
      let o = agg.get(key);
      if (!o) {
        o = { key, services: 0, compliant: 0, warning: 0, nonCompliant: 0, totalBlast: 0, scores: [] };
        agg.set(key, o);
      }
      o.services++;
      const st = s.contractStatus;
      if (st === 'Compliant') o.compliant++;
      else if (st === 'Warning') o.warning++;
      else if (st === 'NonCompliant') o.nonCompliant++;
      o.totalBlast += s.blastRadius || 0;
      if (s.complianceScore != null) o.scores.push(s.complianceScore);
    }
    const result = [];
    for (const [key, o] of agg) {
      if (!key.toLowerCase().includes(q)) continue;
      o.compliancePercent = o.scores.length > 0 ? Math.round(o.scores.reduce((a, b) => a + b, 0) / o.scores.length) : -1;
      result.push(o);
      if (result.length >= 4) break;
    }
    return result;
  });

  function onInput(e) {
    query = e.target.value;
    showResults = !!query;
    selectedIdx = -1;
  }

  let totalResults = $derived(ownerMatches.length + matches.length);

  function onKeydown(e) {
    if (e.key === 'ArrowDown') { e.preventDefault(); selectedIdx = Math.min(selectedIdx + 1, totalResults - 1); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); selectedIdx = Math.max(selectedIdx - 1, -1); }
    else if (e.key === 'Enter' && selectedIdx >= 0) {
      e.preventDefault();
      if (selectedIdx < ownerMatches.length) {
        pickOwner(ownerMatches[selectedIdx].key);
      } else {
        const svcIdx = selectedIdx - ownerMatches.length;
        if (matches[svcIdx]) pick(matches[svcIdx].name);
      }
    }
    else if (e.key === 'Escape') closeSearch();
  }

  function closeSearch() { showResults = false; selectedIdx = -1; query = ''; searchInputEl?.blur(); }

  function pick(name) { closeSearch(); navigate('detail', { name }); }

  function pickOwner(key) { closeSearch(); location.hash = ownerUrl(key); }

  function handleClickOutside(e) {
    if (!e.target.closest('.search-box')) closeSearch();
    if (mobileMenuOpen && !e.target.closest('.navbar')) mobileMenuOpen = false;
  }

  const enabledSources = $derived(sourcesInfo.filter((s) => s.enabled));
</script>

<svelte:document onclick={handleClickOutside} onkeydown={handleGlobalKeydown} />

<nav class="navbar">
  <div class="navbar-left">
    <a href="#/" class="navbar-brand">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="20" height="20"><path d="M20.24 12.24a6 6 0 0 0-8.49-8.49L5 10.5V19h8.5z"/><line x1="16" y1="8" x2="2" y2="22"/><line x1="17.5" y1="15" x2="9" y2="15"/></svg>
      Pacto
      {#if version}<span class="version-tag">{version}</span>{/if}
    </a>
  </div>

  <div class="search-box">
    <svg class="search-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="15" height="15"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
    <input
      bind:this={searchInputEl}
      type="text"
      placeholder="Search..."
      value={query}
      oninput={onInput}
      onfocus={() => { if (query) showResults = true; }}
      onkeydown={onKeydown}
      aria-label="Search services"
    />
    <kbd class="search-kbd">
      {navigator.platform?.includes('Mac') ? '⌘' : 'Ctrl+'}K
    </kbd>
    {#if showResults}
      <div class="search-results" role="listbox">
        {#if ownerMatches.length === 0 && matches.length === 0}
          <div class="search-empty">No results for "{query}"</div>
        {:else}
          {#if ownerMatches.length > 0}
            <div class="search-group-label">Owners</div>
            {#each ownerMatches as om, i}
              <a
                href={ownerUrl(om.key)}
                class="search-result"
                class:selected={i === selectedIdx}
                role="option"
                aria-selected={i === selectedIdx}
                onclick={(e) => { e.preventDefault(); pickOwner(om.key); }}
                onmouseenter={() => { selectedIdx = i; }}
              >
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="14" height="14" style="flex-shrink:0; color:var(--c-text-3)"><path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/></svg>
                <span class="search-result-name">{om.key}</span>
                <span class="search-result-meta">{om.services} svc{om.services !== 1 ? 's' : ''}</span>
                {#if om.compliancePercent >= 0}<span class="search-score {complianceClass(om.compliancePercent)}">{om.compliancePercent}%</span>{/if}
                {#if om.warning > 0}<span class="search-stat search-stat-warn">{om.warning}w</span>{/if}
                {#if om.nonCompliant > 0}<span class="search-stat search-stat-err">{om.nonCompliant}nc</span>{/if}
              </a>
            {/each}
          {/if}
          {#if matches.length > 0}
            {#if ownerMatches.length > 0}<div class="search-group-label">Services</div>{/if}
            {#each matches as svc, i}
              {@const idx = ownerMatches.length + i}
              <a
                href={serviceUrl(svc.name)}
                class="search-result"
                class:selected={idx === selectedIdx}
                role="option"
                aria-selected={idx === selectedIdx}
                onclick={(e) => { e.preventDefault(); pick(svc.name); }}
                onmouseenter={() => { selectedIdx = idx; }}
              >
                <span class="search-result-name">{svc.name}</span>
                {#if svc.version}<span class="search-result-meta">{svc.version}</span>{/if}
                <span class="badge badge-{statusClass(svc.contractStatus)}"><span class="badge-dot"></span>{svc.contractStatus}</span>
              </a>
            {/each}
          {/if}
        {/if}
      </div>
    {/if}
  </div>

  <!-- Desktop right section -->
  <div class="navbar-right navbar-right-desktop">
    {#each enabledSources as src}
      <span class="source-tag" data-tip={sourceTooltip(src.type)} data-tip-align="right"><span class="source-dot source-dot-{src.type}"></span>{src.type}</span>
    {/each}
    {#if discovering}
      <span class="pill" style="font-size:11px">discovering…</span>
    {/if}

    <button type="button" class="btn-ghost" class:spinning={refreshing} onclick={onRefresh} aria-label="Refresh" data-tip="Refresh data" data-tip-align="right">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="16" height="16"><polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/></svg>
    </button>
    <button type="button" class="btn-ghost" class:active={autoReload} onclick={onToggleAutoReload} aria-label="Toggle auto-reload" data-tip="Auto-reload ({autoReload ? 'on' : 'off'})" data-tip-align="right">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="16" height="16"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>
    </button>
    <button type="button" class="btn-ghost" onclick={onToggleTheme} aria-label="Toggle theme" data-tip="Toggle theme" data-tip-align="right">
      <svg class="theme-sun" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="16" height="16"><circle cx="12" cy="12" r="5"/><line x1="12" y1="1" x2="12" y2="3"/><line x1="12" y1="21" x2="12" y2="23"/><line x1="4.22" y1="4.22" x2="5.64" y2="5.64"/><line x1="18.36" y1="18.36" x2="19.78" y2="19.78"/><line x1="1" y1="12" x2="3" y2="12"/><line x1="21" y1="12" x2="23" y2="12"/><line x1="4.22" y1="19.78" x2="5.64" y2="18.36"/><line x1="18.36" y1="5.64" x2="19.78" y2="4.22"/></svg>
      <svg class="theme-moon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="16" height="16"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>
    </button>
  </div>

  <!-- Mobile hamburger -->
  <button type="button" class="hamburger" class:open={mobileMenuOpen} onclick={() => mobileMenuOpen = !mobileMenuOpen} aria-label="Menu">
    <span></span><span></span><span></span>
  </button>
</nav>

<!-- Mobile drawer -->
{#if mobileMenuOpen}
  <div class="mobile-drawer" role="menu">
    <div class="mobile-drawer-section">
      {#each enabledSources as src}
        <span class="source-tag"><span class="source-dot source-dot-{src.type}"></span>{src.type}</span>
      {/each}
      {#if discovering}
        <span class="pill" style="font-size:11px">discovering…</span>
      {/if}
    </div>
    <div class="mobile-drawer-actions">
      <button type="button" class="btn btn-sm" class:spinning={refreshing} onclick={() => { onRefresh(); mobileMenuOpen = false; }}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="14" height="14"><polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/></svg>
        Refresh
      </button>
      <button type="button" class="btn btn-sm" class:active={autoReload} onclick={onToggleAutoReload}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="14" height="14"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>
        Auto-reload {autoReload ? 'on' : 'off'}
      </button>
      <button type="button" class="btn btn-sm" onclick={() => { onToggleTheme(); mobileMenuOpen = false; }}>
        <svg class="theme-sun" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="14" height="14"><circle cx="12" cy="12" r="5"/><line x1="12" y1="1" x2="12" y2="3"/><line x1="12" y1="21" x2="12" y2="23"/><line x1="4.22" y1="4.22" x2="5.64" y2="5.64"/><line x1="18.36" y1="18.36" x2="19.78" y2="19.78"/><line x1="1" y1="12" x2="3" y2="12"/><line x1="21" y1="12" x2="23" y2="12"/><line x1="4.22" y1="19.78" x2="5.64" y2="18.36"/><line x1="18.36" y1="5.64" x2="19.78" y2="4.22"/></svg>
        <svg class="theme-moon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="14" height="14"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>
        Theme
      </button>
    </div>
  </div>
{/if}


<style>
  .navbar {
    display: flex; align-items: center; gap: var(--sp-4);
    padding: 0 var(--sp-8); height: var(--navbar-h);
    border-bottom: 1px solid var(--c-border);
    background: var(--c-surface);
    position: sticky; top: 0; z-index: 100;
  }
  .navbar-left { flex-shrink: 0; }
  .navbar-brand {
    display: flex; align-items: center; gap: 8px;
    font-size: 1rem; font-weight: 700; letter-spacing: -0.03em;
    color: var(--c-text); text-decoration: none;
  }
  .navbar-brand:hover { text-decoration: none; color: var(--c-text); }
  .navbar-brand svg { color: var(--c-accent); }
  .version-tag {
    font-size: var(--text-xs); font-weight: 500; color: var(--c-text-3);
    background: var(--c-bg); border: 1px solid var(--c-border);
    padding: 2px 8px; border-radius: 100px;
  }
  .search-box {
    position: relative; flex: 1; max-width: 480px;
  }
  .search-icon {
    position: absolute; left: 12px; top: 50%; transform: translateY(-50%);
    color: var(--c-text-3); pointer-events: none;
  }
  .search-box input {
    width: 100%; padding: 8px 14px 8px 34px;
    min-height: var(--touch-min);
    border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    background: var(--c-bg); color: var(--c-text);
    font: inherit; font-size: var(--text-sm);
  }
  .search-box input:focus { border-color: var(--c-accent); outline: none; }
  .search-box input:focus + .search-kbd { display: none; }
  .search-kbd {
    position: absolute; right: 10px; top: 50%; transform: translateY(-50%);
    padding: 2px 7px; border-radius: 3px;
    background: var(--c-surface-hover); border: 1px solid var(--c-border);
    font-family: var(--font-sans); font-size: var(--text-xs); color: var(--c-text-3);
    pointer-events: none; line-height: 1.6;
  }
  .search-results {
    position: absolute; top: 100%; left: 0; right: 0;
    margin-top: 4px; background: var(--c-surface);
    border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    box-shadow: var(--shadow-md); max-height: 360px; overflow-y: auto; z-index: 200;
    animation: slideDown 150ms ease-out both;
  }
  .search-empty { padding: var(--sp-3) var(--sp-4); color: var(--c-text-3); font-size: var(--text-sm); }
  .search-result {
    display: flex; align-items: center; gap: var(--sp-2);
    padding: var(--sp-3) var(--sp-4); text-decoration: none; color: var(--c-text);
    font-size: var(--text-sm); cursor: pointer;
    min-height: var(--touch-min);
  }
  .search-result { transition: background var(--transition); }
  .search-result:hover, .search-result.selected { background: var(--c-surface-hover); text-decoration: none; }
  .search-result-name { font-weight: 500; min-width: 0; overflow: hidden; text-overflow: ellipsis; }
  .search-result-meta { color: var(--c-text-3); font-size: var(--text-xs); }
  .search-group-label {
    padding: 6px var(--sp-4) 2px; font-size: 10px; font-weight: 600;
    text-transform: uppercase; letter-spacing: 0.05em; color: var(--c-text-3);
  }
  .search-score {
    font-size: var(--text-xs); font-weight: 600; margin-left: auto;
  }
  .search-stat {
    font-size: 10px; font-weight: 600; padding: 1px 5px;
    border-radius: var(--radius-xs);
  }
  .search-stat-warn { color: var(--c-warn); }
  .search-stat-err { color: var(--c-err); }
  .navbar-right {
    display: flex; align-items: center; gap: var(--sp-2); margin-left: auto; flex-shrink: 0;
  }
  .source-tag {
    display: inline-flex; align-items: center; gap: 5px;
    font-size: var(--text-xs); font-weight: 600; text-transform: uppercase;
    color: var(--c-text-3);
    padding: 4px 10px; border-radius: var(--radius-xs);
    transition: background var(--transition), color var(--transition);
  }
  .source-tag:hover { background: var(--c-surface-hover); color: var(--c-text-2); }
  .navbar-right :global(.btn-ghost) {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 36px;
    height: 36px;
    padding: 0;
    border-radius: var(--radius-sm);
    background: none;
    border: 1px solid transparent;
    color: var(--c-text-3);
    cursor: pointer;
    transition: all var(--transition);
  }
  .navbar-right :global(.btn-ghost:hover) {
    color: var(--c-text);
    background: var(--c-surface-hover);
    border-color: var(--c-border);
  }
  .active { color: var(--c-accent) !important; }
  .spinning svg { animation: spin 0.8s linear infinite; }

  /* Theme toggle */
  :global([data-theme="light"]) .theme-sun { display: none; }
  :global([data-theme="light"]) .theme-moon { display: block; }
  .theme-moon { display: none; }
  .theme-sun { display: block; }

  /* Hamburger — hidden on desktop */
  .hamburger {
    display: none;
    flex-direction: column; justify-content: center; gap: 4px;
    width: 36px; height: 36px;
    background: none; border: none; cursor: pointer; padding: 8px;
    margin-left: auto;
  }
  .hamburger span {
    display: block; width: 100%; height: 2px;
    background: var(--c-text-2); border-radius: 1px;
    transition: transform 200ms ease, opacity 200ms ease;
  }
  .hamburger.open span:nth-child(1) { transform: translateY(6px) rotate(45deg); }
  .hamburger.open span:nth-child(2) { opacity: 0; }
  .hamburger.open span:nth-child(3) { transform: translateY(-6px) rotate(-45deg); }

  /* Mobile drawer — hidden on desktop */
  .mobile-drawer {
    display: none;
    position: sticky; top: var(--navbar-h); z-index: 99;
    background: var(--c-surface); border-bottom: 1px solid var(--c-border);
    padding: var(--sp-4);
    animation: slideDown 150ms ease-out both;
  }
  .mobile-drawer-section {
    display: flex; flex-wrap: wrap; gap: var(--sp-2);
    margin-bottom: var(--sp-3);
  }
  .mobile-drawer-actions {
    display: flex; flex-wrap: wrap; gap: var(--sp-2);
  }

  /* ─── Mobile ─── */
  @media (max-width: 768px) {
    .navbar {
      padding: 0 var(--sp-4);
      gap: var(--sp-2);
    }
    .navbar-right-desktop { display: none; }
    .hamburger { display: flex; }
    .mobile-drawer { display: block; }
    .search-kbd { display: none; }
    .search-box { max-width: none; }
  }
</style>
