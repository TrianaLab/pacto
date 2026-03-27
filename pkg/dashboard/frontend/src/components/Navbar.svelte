<script>
  import {
    services, sourcesInfo, enabledSources, searchTerm,
    appVersion, navigateTo, isSourceEnabled, toggleSourceClick,
  } from '../lib/stores.js';
  import { getSources, sourceTooltips } from '../lib/helpers.js';
  import PhaseBadge from './PhaseBadge.svelte';
  import SourcePill from './SourcePill.svelte';

  let { spinning = false, autoReloadEnabled = false, onRefresh, onToggleAutoReload } = $props();

  let searchInputEl = $state(null);
  let dropdownOpen = $state(false);
  let dropdownIndex = $state(-1);
  let localSearch = $state('');

  function onInput() {
    localSearch = searchInputEl?.value || '';
    searchTerm.set(localSearch);
    dropdownOpen = !!localSearch;
    dropdownIndex = -1;
  }

  function onFocus() {
    if (localSearch) dropdownOpen = true;
  }

  function onKeydown(e) {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      dropdownIndex = Math.min(dropdownIndex + 1, matches.length - 1);
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      dropdownIndex = Math.max(dropdownIndex - 1, -1);
    } else if (e.key === 'Enter' && dropdownIndex >= 0 && matches[dropdownIndex]) {
      e.preventDefault();
      closeDropdown();
      navigateTo('detail', matches[dropdownIndex].name);
    } else if (e.key === 'Escape') {
      closeDropdown();
    }
  }

  function closeDropdown() {
    dropdownOpen = false;
    dropdownIndex = -1;
  }

  function handleClickOutside(e) {
    if (!e.target.closest('.topbar-search')) closeDropdown();
  }

  let matches = $derived.by(() => {
    if (!localSearch) return [];
    const term = localSearch.toLowerCase();
    return ($services || [])
      .filter((s) => {
        const sources = getSources(s);
        const text = [s.name, s.owner || '', s.version || '', sources.join(' ')].join(' ').toLowerCase();
        return text.includes(term);
      })
      .slice(0, 8);
  });

  let enabledSourcesList = $derived(
    ($sourcesInfo || []).filter((s) => s.enabled)
  );

  function toggleTheme() {
    const r = document.documentElement;
    const c = r.getAttribute('data-theme');
    let isDark;
    if (c) isDark = c === 'dark';
    else isDark = window.matchMedia('(prefers-color-scheme:dark)').matches;
    const n = isDark ? 'light' : 'dark';
    r.setAttribute('data-theme', n);
    localStorage.setItem('pacto-theme', n);
  }

  function toggleSourcePill(type) {
    enabledSources.update((cur) => toggleSourceClick(type, cur));
  }
</script>

<svelte:document onclick={handleClickOutside} />

<nav class="topbar">
  <div class="topbar-left">
    <button type="button" class="topbar-logo" onclick={() => navigateTo('list')}>
      <svg class="topbar-logo-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M20.24 12.24a6 6 0 0 0-8.49-8.49L5 10.5V19h8.5z"/><line x1="16" y1="8" x2="2" y2="22"/><line x1="17.5" y1="15" x2="9" y2="15"/></svg>
      Pacto
      {#if $appVersion}
        <span class="version-badge">{$appVersion}</span>
      {/if}
    </button>
  </div>

  <div class="topbar-search">
    <svg class="search-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" width="15" height="15"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
    <input
      bind:this={searchInputEl}
      type="text"
      placeholder="Search services..."
      oninput={onInput}
      onfocus={onFocus}
      onkeydown={onKeydown}
    />
    {#if dropdownOpen}
      <div class="search-dropdown open">
        {#if matches.length === 0}
          <div class="search-dropdown-empty">No services match &ldquo;{localSearch}&rdquo;</div>
        {:else}
          {#each matches as svc, i}
            <button
              class="search-dropdown-item"
              class:active={i === dropdownIndex}
              onclick={() => { closeDropdown(); navigateTo('detail', svc.name); }}
              onmouseenter={() => (dropdownIndex = i)}
              role="option"
              tabindex="-1"
              aria-selected={i === dropdownIndex}
            >
              <span class="sdi-name">{svc.name}</span>
              <span class="sdi-meta">{svc.version || ''}</span>
              <PhaseBadge phase={svc.phase} />
              <span class="sdi-meta">
                {#each getSources(svc) as src}
                  <SourcePill type={src} />
                {/each}
              </span>
            </button>
          {/each}
        {/if}
      </div>
    {/if}
  </div>

  <div class="topbar-right">
    <div class="source-pills">
      {#each enabledSourcesList as src}
        {@const active = isSourceEnabled(src.type, $enabledSources)}
        <span class="topbar-btn-wrap">
          <button
            class="source-pill source-pill-{src.type}"
            style="cursor:pointer;opacity:{active ? 1 : 0.35};border:none;font-family:inherit"
            onclick={() => toggleSourcePill(src.type)}
          >
            <span class="pill-dot"></span>
            {src.type.toUpperCase()}
          </button>
          <span class="topbar-tooltip">{sourceTooltips[src.type] || src.type}</span>
        </span>
      {/each}
    </div>

    <span class="topbar-btn-wrap">
      <button class="topbar-btn" class:spinning onclick={onRefresh} aria-label="Refresh">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" width="16" height="16"><polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/></svg>
      </button>
      <span class="topbar-tooltip">Refresh data</span>
    </span>

    <span class="topbar-btn-wrap">
      <button class="topbar-btn" class:active={autoReloadEnabled} onclick={onToggleAutoReload} aria-label="Toggle auto-reload">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" width="16" height="16"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>
      </button>
      <span class="topbar-tooltip">Auto-reload every 10s ({autoReloadEnabled ? 'on' : 'off'})</span>
    </span>

    <span class="topbar-btn-wrap">
      <button class="theme-toggle" onclick={toggleTheme} aria-label="Toggle theme">
        <svg class="theme-icon-sun" viewBox="0 0 24 24"><circle cx="12" cy="12" r="5"/><line x1="12" y1="1" x2="12" y2="3"/><line x1="12" y1="21" x2="12" y2="23"/><line x1="4.22" y1="4.22" x2="5.64" y2="5.64"/><line x1="18.36" y1="18.36" x2="19.78" y2="19.78"/><line x1="1" y1="12" x2="3" y2="12"/><line x1="21" y1="12" x2="23" y2="12"/><line x1="4.22" y1="19.78" x2="5.64" y2="18.36"/><line x1="18.36" y1="5.64" x2="19.78" y2="4.22"/></svg>
        <svg class="theme-icon-moon" viewBox="0 0 24 24"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>
      </button>
      <span class="topbar-tooltip">Switch dark/light theme</span>
    </span>
  </div>
</nav>
