<script>
  import { navigate, serviceUrl } from './lib/router.ts';
  import { phaseClass, sourceTooltip } from './lib/format.ts';

  let {
    services = [], sourcesInfo = [], version = '', discovering = false,
    autoReload = false, refreshing = false, onRefresh, onToggleAutoReload, onToggleTheme,
  } = $props();

  let query = $state('');
  let showResults = $state(false);
  let selectedIdx = $state(-1);
  let searchInputEl = $state(null);

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
      .filter((s) => s.name.toLowerCase().includes(q) || (s.owner || '').toLowerCase().includes(q))
      .slice(0, 8);
  });

  function onInput(e) {
    query = e.target.value;
    showResults = !!query;
    selectedIdx = -1;
  }

  function onKeydown(e) {
    if (e.key === 'ArrowDown') { e.preventDefault(); selectedIdx = Math.min(selectedIdx + 1, matches.length - 1); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); selectedIdx = Math.max(selectedIdx - 1, -1); }
    else if (e.key === 'Enter' && selectedIdx >= 0 && matches[selectedIdx]) {
      e.preventDefault(); closeSearch(); navigate('detail', { name: matches[selectedIdx].name });
    }
    else if (e.key === 'Escape') closeSearch();
  }

  function closeSearch() { showResults = false; selectedIdx = -1; query = ''; searchInputEl?.blur(); }

  function pick(name) { closeSearch(); navigate('detail', { name }); }

  function handleClickOutside(e) {
    if (!e.target.closest('.search-box')) closeSearch();
  }

  const enabledSources = $derived(sourcesInfo.filter((s) => s.enabled));
</script>

<svelte:document onclick={handleClickOutside} onkeydown={handleGlobalKeydown} />

<nav class="navbar">
  <div class="navbar-left">
    <a href="#/" class="navbar-brand">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="18" height="18"><path d="M20.24 12.24a6 6 0 0 0-8.49-8.49L5 10.5V19h8.5z"/><line x1="16" y1="8" x2="2" y2="22"/><line x1="17.5" y1="15" x2="9" y2="15"/></svg>
      Pacto
      {#if version}<span class="version-tag">{version}</span>{/if}
    </a>
  </div>

  <div class="search-box">
    <svg class="search-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="14" height="14"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
    <input
      bind:this={searchInputEl}
      type="text"
      placeholder="Search services…"
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
        {#if matches.length === 0}
          <div class="search-empty">No results for "{query}"</div>
        {:else}
          {#each matches as svc, i}
            <a
              href={serviceUrl(svc.name)}
              class="search-result"
              class:selected={i === selectedIdx}
              role="option"
              aria-selected={i === selectedIdx}
              onclick={(e) => { e.preventDefault(); pick(svc.name); }}
              onmouseenter={() => { selectedIdx = i; }}
            >
              <span class="search-result-name">{svc.name}</span>
              {#if svc.version}<span class="search-result-meta">{svc.version}</span>{/if}
              <span class="badge badge-{phaseClass(svc.phase)}"><span class="badge-dot"></span>{svc.phase}</span>
            </a>
          {/each}
        {/if}
      </div>
    {/if}
  </div>

  <div class="navbar-right">
    {#each enabledSources as src}
      <span class="source-tag" data-tip={sourceTooltip(src.type)} data-tip-align="right"><span class="source-dot source-dot-{src.type}"></span>{src.type}</span>
    {/each}
    {#if discovering}
      <span class="pill" style="font-size:10px">discovering…</span>
    {/if}

    <button type="button" class="btn-ghost" class:spinning={refreshing} onclick={onRefresh} aria-label="Refresh" data-tip="Refresh data" data-tip-align="right">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="15" height="15"><polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/></svg>
    </button>
    <button type="button" class="btn-ghost" class:active={autoReload} onclick={onToggleAutoReload} aria-label="Toggle auto-reload" data-tip="Auto-reload ({autoReload ? 'on' : 'off'})" data-tip-align="right">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="15" height="15"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>
    </button>
    <button type="button" class="btn-ghost" onclick={onToggleTheme} aria-label="Toggle theme" data-tip="Toggle theme" data-tip-align="right">
      <svg class="theme-sun" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="15" height="15"><circle cx="12" cy="12" r="5"/><line x1="12" y1="1" x2="12" y2="3"/><line x1="12" y1="21" x2="12" y2="23"/><line x1="4.22" y1="4.22" x2="5.64" y2="5.64"/><line x1="18.36" y1="18.36" x2="19.78" y2="19.78"/><line x1="1" y1="12" x2="3" y2="12"/><line x1="21" y1="12" x2="23" y2="12"/><line x1="4.22" y1="19.78" x2="5.64" y2="18.36"/><line x1="18.36" y1="5.64" x2="19.78" y2="4.22"/></svg>
      <svg class="theme-moon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="15" height="15"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>
    </button>
  </div>
</nav>


<style>
  .navbar {
    display: flex; align-items: center; gap: 16px;
    padding: 0 32px; height: 48px;
    border-bottom: 1px solid var(--c-border);
    background: var(--c-surface);
    position: sticky; top: 0; z-index: 100;
  }
  .navbar-left { flex-shrink: 0; }
  .navbar-brand {
    display: flex; align-items: center; gap: 6px;
    font-size: 15px; font-weight: 700; letter-spacing: -0.03em;
    color: var(--c-text); text-decoration: none;
  }
  .navbar-brand:hover { text-decoration: none; color: var(--c-text); }
  .navbar-brand svg { color: var(--c-accent); }
  .version-tag {
    font-size: 10px; font-weight: 500; color: var(--c-text-3);
    background: var(--c-bg); border: 1px solid var(--c-border);
    padding: 1px 6px; border-radius: 100px;
  }
  .search-box {
    position: relative; flex: 1; max-width: 420px;
  }
  .search-icon {
    position: absolute; left: 10px; top: 50%; transform: translateY(-50%);
    color: var(--c-text-3); pointer-events: none;
  }
  .search-box input {
    width: 100%; padding: 6px 12px 6px 30px;
    border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    background: var(--c-bg); color: var(--c-text);
    font: inherit; font-size: var(--text-sm);
  }
  .search-box input:focus { border-color: var(--c-accent); outline: none; }
  .search-box input:focus + .search-kbd { display: none; }
  .search-kbd {
    position: absolute; right: 8px; top: 50%; transform: translateY(-50%);
    padding: 1px 5px; border-radius: 3px;
    background: var(--c-surface-hover); border: 1px solid var(--c-border);
    font-family: var(--font-sans); font-size: 10px; color: var(--c-text-3);
    pointer-events: none; line-height: 1.6;
  }
  .search-results {
    position: absolute; top: 100%; left: 0; right: 0;
    margin-top: 4px; background: var(--c-surface);
    border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    box-shadow: var(--shadow-md); max-height: 320px; overflow-y: auto; z-index: 200;
    animation: slideDown 150ms ease-out both;
  }
  .search-empty { padding: 12px 16px; color: var(--c-text-3); font-size: var(--text-sm); }
  .search-result {
    display: flex; align-items: center; gap: 8px;
    padding: 8px 16px; text-decoration: none; color: var(--c-text);
    font-size: var(--text-sm); cursor: pointer;
  }
  .search-result { transition: background var(--transition); }
  .search-result:hover, .search-result.selected { background: var(--c-surface-hover); text-decoration: none; }
  .search-result-name { font-weight: 500; }
  .search-result-meta { color: var(--c-text-3); font-size: var(--text-xs); }
  .navbar-right {
    display: flex; align-items: center; gap: 8px; margin-left: auto; flex-shrink: 0;
  }
  .source-tag {
    display: inline-flex; align-items: center; gap: 4px;
    font-size: 10px; font-weight: 600; text-transform: uppercase;
    color: var(--c-text-3);
    padding: 2px 8px; border-radius: var(--radius-xs);
    transition: background var(--transition), color var(--transition);
  }
  .source-tag:hover { background: var(--c-surface-hover); color: var(--c-text-2); }
  .navbar-right :global(.btn-ghost) {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 30px;
    height: 30px;
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
</style>
