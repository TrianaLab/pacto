<script>
  import { ownersUrl, readinessUrl, fleetUrl, fleetOverviewUrl, compareDiffUrl } from './lib/router.ts';
  import { sourceTooltip } from './lib/format.ts';
  import SourceDot from './components/SourceDot.svelte';

  let {
    services = [], sourcesInfo = [], capabilities = null, version = '', discovering = false, view = 'list',
    autoReload = false, refreshing = false, fleetSearch = false,
    onRefresh, onToggleAutoReload, onToggleTheme, onOpenSearch,
  } = $props();

  // A6: the visible search affordance opens the global fleet EntitySearch on
  // fleet-capable hosts (keyboard shortcut '/') and falls back to the command palette
  // (Cmd/Ctrl-K) otherwise. Its label and shortcut hint communicate the actual action.
  const isMac = typeof navigator !== 'undefined' && navigator.platform?.includes('Mac');
  const searchLabel = $derived(fleetSearch ? 'Search the fleet' : 'Open command palette');
  const searchPlaceholder = $derived(fleetSearch ? 'Search the fleet...' : 'Search...');
  const searchKbd = $derived(fleetSearch ? '/' : (isMac ? '⌘K' : 'Ctrl+K'));

  // Persistent primary nav. `views` lists the route.view values that light the
  // item. The Operational Graph is now the single graph/topology capability (the
  // former standalone "Graph" folded into it, still reachable as a deep link);
  // Impact is a contextual deep route launched from Compare or a selected
  // service/revision, not a top-level empty page. `cap`, when set, requires that
  // capability to be enabled on the host — so a fleet-less host never shows a dead
  // Operational Graph tab.
  // The Services primary destination is the product service list (/fleet/services) on
  // fleet-capable hosts, falling back to the legacy list (#/) otherwise; both mark the
  // Services tab active. Until capabilities are known (null), show everything; once
  // known, hide items whose required capability the host does not serve.
  const nav = $derived(
    [
      { label: 'Overview', href: fleetOverviewUrl(), views: ['fleet-overview', 'fleet-entity', 'fleet-attention', 'impact'], cap: 'fleet' },
      { label: 'Services', href: capabilities?.fleet ? '#/fleet/services' : '#/', views: ['list', 'detail', 'fleet-services'] },
      { label: 'Operational Graph', href: fleetUrl(), views: ['fleet', 'graph'], cap: 'fleet' },
      { label: 'Owners', href: capabilities?.fleet ? '#/fleet/owners' : ownersUrl(), views: ['owners', 'owner-detail', 'fleet-owners'] },
      { label: 'Readiness', href: readinessUrl(), views: ['readiness'] },
      { label: 'Compare', href: compareDiffUrl(), views: ['diff'] },
    ].filter((item) => !item.cap || capabilities === null || capabilities[item.cap]),
  );
  const isActive = (item) => item.views.includes(view);

  let mobileMenuOpen = $state(false);
  let hamburgerEl = $state(null);
  let drawerEl = $state(null);

  // Spin the brand mark on click (also navigates home via the href). Reduced-motion safe.
  function spinLogo(e) {
    const el = e.currentTarget.querySelector('.brand-mark');
    if (!el || window.matchMedia('(prefers-reduced-motion: reduce)').matches) return;
    el.classList.remove('spin');
    void el.offsetWidth; // reflow so the animation restarts on every click
    el.classList.add('spin');
  }

  function toggleMenu() {
    mobileMenuOpen ? closeMenu() : (mobileMenuOpen = true);
  }
  // Closing the overlay ALWAYS returns focus to the control that opened it, so a
  // keyboard/screen-reader user is never dropped at the top of the document.
  function closeMenu(restoreFocus = true) {
    if (!mobileMenuOpen) return;
    mobileMenuOpen = false;
    // Restore focus AFTER the drawer is torn down (a microtask), so the browser
    // does not blur the hamburger during the reactive DOM removal.
    if (restoreFocus) queueMicrotask(() => hamburgerEl?.focus());
  }

  // When the drawer opens, move focus into it (first link) so the overlay is
  // immediately operable by keyboard, matching the command palette's behavior.
  $effect(() => {
    if (mobileMenuOpen && drawerEl) {
      queueMicrotask(() => drawerEl?.querySelector('a, button')?.focus());
    }
  });

  // Focus trap + Escape for the open drawer (an overlay, per WAI-ARIA): Escape
  // closes and restores focus; Tab cycles within the drawer's focusable elements.
  function onDrawerKeydown(e) {
    if (e.key === 'Escape') { e.preventDefault(); closeMenu(); return; }
    if (e.key !== 'Tab') return;
    const focusable = Array.from(drawerEl?.querySelectorAll('a, button') || []).filter((el) => !el.disabled);
    if (focusable.length === 0) return;
    const idx = focusable.indexOf(document.activeElement);
    const next = e.shiftKey
      ? (idx <= 0 ? focusable.length - 1 : idx - 1)
      : (idx >= focusable.length - 1 ? 0 : idx + 1);
    e.preventDefault();
    focusable[next]?.focus();
  }

  function handleClickOutside(e) {
    // A click outside the navbar closes the drawer WITHOUT stealing focus back to
    // the hamburger (the user is interacting elsewhere).
    if (mobileMenuOpen && !e.target.closest('.navbar')) closeMenu(false);
  }

  const enabledSources = $derived(sourcesInfo.filter((s) => s.enabled));
</script>

<svelte:document onclick={handleClickOutside} />

<nav class="navbar">
  <div class="navbar-left">
    <a href="#/" class="navbar-brand" onclick={spinLogo}>
      <svg class="brand-mark" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" width="20" height="20"><path d="M10 6 5 12 10 18"/><path d="M14 6 19 12 14 18"/><circle cx="12" cy="12" r="1.9" fill="currentColor" stroke="none"/></svg>
      Pacto
      {#if version}<span class="version-tag">{version}</span>{/if}
    </a>
  </div>

  <nav class="navbar-nav navbar-nav-desktop" aria-label="Primary">
    {#each nav as item}
      <a href={item.href} class="nav-link" class:active={isActive(item)} aria-current={isActive(item) ? 'page' : undefined}>{item.label}</a>
    {/each}
  </nav>

  <button type="button" class="search-box search-trigger" onclick={onOpenSearch} aria-label={searchLabel} data-testid="navbar-search">
    <svg class="search-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="15" height="15"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
    <span class="search-trigger-text">{searchPlaceholder}</span>
    <kbd class="search-kbd">{searchKbd}</kbd>
  </button>

  <!-- Desktop right section -->
  <div class="navbar-right navbar-right-desktop">
    {#each enabledSources as src}
      <span class="source-tag" data-tip={sourceTooltip(src.type)} data-tip-align="right"><SourceDot source={src.type} />{src.type}</span>
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
  <button
    type="button"
    class="hamburger"
    class:open={mobileMenuOpen}
    bind:this={hamburgerEl}
    onclick={toggleMenu}
    aria-label="Menu"
    aria-haspopup="true"
    aria-expanded={mobileMenuOpen}
    aria-controls="mobile-drawer"
  >
    <span></span><span></span><span></span>
  </button>
</nav>

<!-- Mobile drawer: an overlay with a focus trap, Escape-to-close and focus
     restore. It is only in the DOM while open, so its navigation is never present
     in the accessibility tree when collapsed. -->
{#if mobileMenuOpen}
  <div class="mobile-drawer" id="mobile-drawer" bind:this={drawerEl} onkeydown={onDrawerKeydown}>
    <nav class="mobile-nav" aria-label="Primary">
      {#each nav as item}
        <a href={item.href} class="mobile-nav-link" class:active={isActive(item)} aria-current={isActive(item) ? 'page' : undefined} onclick={() => closeMenu(false)}>{item.label}</a>
      {/each}
    </nav>
    <div class="mobile-drawer-section">
      {#each enabledSources as src}
        <span class="source-tag"><SourceDot source={src.type} />{src.type}</span>
      {/each}
      {#if discovering}
        <span class="pill" style="font-size:11px">discovering…</span>
      {/if}
    </div>
    <div class="mobile-drawer-actions">
      <button type="button" class="btn btn-sm" class:spinning={refreshing} onclick={() => { onRefresh(); closeMenu(false); }}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="14" height="14"><polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/></svg>
        Refresh
      </button>
      <button type="button" class="btn btn-sm" class:active={autoReload} onclick={onToggleAutoReload}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="14" height="14"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>
        Auto-reload {autoReload ? 'on' : 'off'}
      </button>
      <button type="button" class="btn btn-sm" onclick={() => { onToggleTheme(); closeMenu(false); }}>
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
  .navbar-brand svg { color: var(--c-accent); transform-origin: 50% 50%; }
  .navbar-brand svg.spin { animation: brand-spin 0.6s ease; }
  @keyframes brand-spin { from { transform: rotate(0); } to { transform: rotate(360deg); } }
  @media (prefers-reduced-motion: reduce) { .navbar-brand svg.spin { animation: none; } }
  .version-tag {
    font-size: var(--text-xs); font-weight: 500; color: var(--c-text-3);
    background: var(--c-bg); border: 1px solid var(--c-border);
    padding: 2px 8px; border-radius: 100px;
  }
  .navbar-nav {
    display: flex; align-items: center; gap: 2px; flex-shrink: 0;
  }
  .nav-link {
    padding: 6px 10px; border-radius: var(--radius-xs);
    font-size: var(--text-sm); font-weight: 500; color: var(--c-text-3);
    text-decoration: none; white-space: nowrap;
    transition: color var(--transition), background var(--transition);
  }
  .nav-link:hover { color: var(--c-text); background: var(--c-surface-hover); text-decoration: none; }
  .nav-link.active { color: var(--c-accent); background: var(--c-accent-bg); }

  .search-box {
    position: relative; flex: 1; max-width: 480px;
  }
  .search-icon {
    position: absolute; left: 12px; top: 50%; transform: translateY(-50%);
    color: var(--c-text-3); pointer-events: none;
  }
  .search-kbd {
    position: absolute; right: 10px; top: 50%; transform: translateY(-50%);
    padding: 2px 7px; border-radius: 3px;
    background: var(--c-surface-hover); border: 1px solid var(--c-border);
    font-family: var(--font-sans); font-size: var(--text-xs); color: var(--c-text-3);
    pointer-events: none; line-height: 1.6;
  }
  .search-trigger {
    display: flex; align-items: center; gap: var(--sp-2);
    padding: 8px 14px 8px 34px; min-height: var(--touch-min);
    border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    background: var(--c-bg); color: var(--c-text-3);
    font: inherit; font-size: var(--text-sm); cursor: pointer; text-align: left;
    position: relative;
  }
  .search-trigger:hover { border-color: var(--c-text-3); }
  .search-trigger-text { flex: 1; }
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
  .mobile-nav {
    display: flex; flex-direction: column; gap: 2px;
    margin-bottom: var(--sp-3); padding-bottom: var(--sp-3);
    border-bottom: 1px solid var(--c-border);
  }
  .mobile-nav-link {
    padding: var(--sp-2) var(--sp-3); border-radius: var(--radius-xs);
    min-height: var(--touch-min); display: flex; align-items: center;
    font-size: var(--text-sm); font-weight: 500; color: var(--c-text-2); text-decoration: none;
  }
  .mobile-nav-link.active { color: var(--c-accent); background: var(--c-accent-bg); }

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
    .navbar-nav-desktop { display: none; }
    .hamburger { display: flex; }
    .mobile-drawer { display: block; }
    .search-kbd { display: none; }
    .search-box { max-width: none; }
  }
</style>
