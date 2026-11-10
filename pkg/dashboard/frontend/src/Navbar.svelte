<script>
  import { fleetOverviewUrl } from './lib/router.ts';
  import { sourceTooltip } from './lib/format.ts';
  import { prefersReducedMotion } from './lib/motion.ts';
  import { navGroups, legacyNav, navCurrent } from './lib/nav.ts';
  import SourceDot from './components/SourceDot.svelte';
  import IdentityBar from './components/IdentityBar.svelte';
  import NavList from './components/NavList.svelte';

  let {
    services = [], sourcesInfo = [], capabilities = null, version = '', discovering = false, view = 'list',
    entityKind = '', autoReload = false, refreshing = false, fleetSearch = false,
    attentionTotal = null, sourceCounts = null,
    onRefresh, onToggleAutoReload, onToggleTheme, onOpenSearch,
  } = $props();

  // A6: the visible search affordance opens the global fleet EntitySearch on
  // fleet-capable hosts (keyboard shortcut '/') and falls back to the command palette
  // (Cmd/Ctrl-K) otherwise. Its label and shortcut hint communicate the actual action.
  const isMac = typeof navigator !== 'undefined' && navigator.platform?.includes('Mac');
  const searchLabel = $derived(fleetSearch ? 'Search services, revisions and targets' : 'Search: open command palette');
  const searchPlaceholder = $derived(fleetSearch ? 'Search services, revisions and targets…' : 'Search services, owners and pages');
  const searchKbd = $derived(fleetSearch ? '/' : (isMac ? '⌘K' : 'Ctrl+K'));

  // SPEC 20 (B2): a Fleet host -- or one whose capabilities are not known yet, the
  // common case -- carries its destinations in the grouped sidebar (Sidebar.svelte),
  // and under 768px in this bar's Menu drawer as the same grouped list. Only a host
  // confirmed NON-fleet (the offline `pacto doc` export) keeps its own items in this
  // bar: four items do not need a sidebar.
  const fleetUI = $derived(capabilities?.fleet !== false);
  const groups = $derived(navGroups(capabilities));
  const legacy = legacyNav();

  // The Pacto brand/logo is the application HOME affordance. On a Fleet-capable host it
  // goes to the canonical Operational Overview (fleetOverviewUrl, never a hardcoded
  // second copy of the route); otherwise it keeps the legacy landing. It follows the
  // SAME capability policy as the Services destination (line above): the fleet Overview
  // only once fleet is confirmed available, and the legacy landing while capabilities
  // are still unresolved (capabilities === null) -- so the logo is never a dead route.
  const homeHref = $derived(capabilities?.fleet ? fleetOverviewUrl() : '#/');

  let mobileMenuOpen = $state(false);
  let hamburgerEl = $state(null);
  let drawerEl = $state(null);

  // Spin the brand mark on click (also navigates home via the href). Reduced-motion safe.
  function spinLogo(e) {
    const el = e.currentTarget.querySelector('.brand-mark');
    if (!el || prefersReducedMotion()) return;
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
  // "Kubernetes — live cluster runtime data" reads as a name and a label, so the name is
  // printed once instead of the wire type above a sentence that repeats it.
  function sourceParts(type) {
    const [name, ...rest] = sourceTooltip(type).split(' — ');
    return { name: rest.length ? name : type, desc: rest.join(' — ') };
  }
  // The popover is native and survives a hash navigation, so following its own link left
  // it open over the page it opened. Any navigation closes it.
  function closeSourcesPop() {
    try { document.getElementById('navbar-sources')?.hidePopover?.(); } catch { /* not open */ }
  }
</script>

<svelte:document onclick={handleClickOutside} />
<svelte:window onhashchange={closeSourcesPop} />

<!-- The application chrome is a BANNER, not a navigation landmark. It used to be a
     bare <nav>, which meant every page exposed two navigation landmarks -- one named
     "Primary" and one unnamed wrapping it -- and no banner at all. A screen-reader
     user landing on any route got "navigation, navigation" and no way to tell the
     site chrome from the page's own nav. -->
<header class="navbar">
  <div class="navbar-left">
    <a href={homeHref} class="navbar-brand" onclick={spinLogo}>
      <svg class="brand-mark" data-motion viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" width="20" height="20"><path d="M10 6 5 12 10 18"/><path d="M14 6 19 12 14 18"/><circle cx="12" cy="12" r="1.9" fill="currentColor" stroke="none"/></svg>
      Pacto
      {#if version}<span class="version-tag" title={version}>{version}</span>{/if}
    </a>
  </div>

  {#if !fleetUI}
    <nav class="navbar-nav navbar-nav-desktop" aria-label="Primary">
      {#each legacy as item}
        <a href={item.href} class="nav-link" class:active={!!navCurrent(item, view, '')} aria-current={navCurrent(item, view, '')}>{item.label}</a>
      {/each}
    </nav>
  {/if}

  <button type="button" class="search-box search-trigger" onclick={onOpenSearch} aria-label={searchLabel} aria-keyshortcuts={fleetSearch ? '/' : 'Control+K Meta+K'} data-testid="navbar-search">
    <svg class="search-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="15" height="15"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
    <span class="search-trigger-text" title={searchPlaceholder}>{searchPlaceholder}</span>
    <kbd class="search-kbd" aria-hidden="true">{searchKbd}</kbd>
  </button>

  <!-- Desktop right section -->
  <div class="navbar-right navbar-right-desktop">
    <!-- One "Data sources" control instead of a badge per source: four badges beside the
         nav and the search left the search truncated at 1440px, and a fifth tab
         (Delivery) made it worse. The popover is native (light dismiss, Escape, focus
         return), so there is no state or focus code of our own. -->
    {#if !fleetUI && (enabledSources.length > 0 || discovering)}
      <button type="button" class="sources-trigger" popovertarget="navbar-sources" data-testid="navbar-sources">
        <span class="sources-dots" aria-hidden="true">
          {#each enabledSources as src}<span class="source-dot source-dot-{src.type}"></span>{/each}
        </span>
        Data sources
        {#if discovering}<span class="sources-busy">discovering…</span>{/if}
      </button>
      <div id="navbar-sources" class="sources-pop" popover data-testid="navbar-sources-panel">
        <p class="sources-pop-title t-label">Data sources</p>
        <ul class="sources-pop-list">
          {#each enabledSources as src}
            {@const sp = sourceParts(src.type)}
            <li><SourceDot source={src.type} /><span><strong>{sp.name}</strong>{#if sp.desc}<span class="t-meta sources-pop-desc">{sp.desc}</span>{/if}</span></li>
          {/each}
        </ul>
        {#if discovering}<p class="t-meta sources-pop-note">Still discovering services.</p>{/if}
      </div>
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
    <IdentityBar />
  </div>

  <!-- Under 768px the sidebar gives way to one Menu button and its drawer (SPEC 20). -->
  <button
    type="button"
    class="btn hamburger"
    id="nav-menu"
    bind:this={hamburgerEl}
    onclick={toggleMenu}
    aria-haspopup="true"
    aria-expanded={mobileMenuOpen}
    aria-controls="mobile-drawer"
  >Menu</button>
</header>

<!-- Mobile drawer: an overlay with a focus trap, Escape-to-close and focus
     restore. It is only in the DOM while open, so its navigation is never present
     in the accessibility tree when collapsed. -->
{#if mobileMenuOpen}
  <!-- svelte-ignore a11y_no_static_element_interactions (the drawer is no widget: its keydown only traps focus among the links inside and closes on Escape) -->
  <div class="mobile-drawer" id="mobile-drawer" bind:this={drawerEl} onkeydown={onDrawerKeydown}>
    <div class="mobile-nav">
      {#if fleetUI}
        <NavList {groups} {view} {entityKind} {attentionTotal} {sourceCounts} prefix="d" onNavigate={() => closeMenu(false)} />
      {:else}
        <nav aria-label="Primary">
          {#each legacy as item}
            <a href={item.href} class="mobile-nav-link" class:active={!!navCurrent(item, view, '')} aria-current={navCurrent(item, view, '')} onclick={() => closeMenu(false)}>{item.label}</a>
          {/each}
        </nav>
      {/if}
    </div>
    <IdentityBar drawer />
    <div class="mobile-drawer-section">
      {#each enabledSources as src}
        <span class="source-tag"><SourceDot source={src.type} />{src.type}</span>
      {/each}
      {#if discovering}
        <span class="pill" style="font-size: var(--text-xs)">discovering…</span>
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
    font-family: var(--font-display); font-size: 1.15rem; font-weight: 700; letter-spacing: -0.01em;
    color: var(--c-text); text-decoration: none;
  }
  .navbar-brand:hover { text-decoration: none; color: var(--c-text); }
  .navbar-brand svg { color: var(--c-accent); transform-origin: 50% 50%; }
  .navbar-brand :global(svg.spin) { animation: brand-spin 0.6s ease; }
  @keyframes brand-spin { from { transform: rotate(0); } to { transform: rotate(360deg); } }
  .version-tag {
    font-size: var(--text-xs); font-weight: 500; color: var(--c-text-3);
    background: var(--c-bg); border: 1px solid var(--c-border);
    padding: 2px 8px; border-radius: 100px;
    /* A long build string must never push the menu button off a phone screen. */
    max-width: 12ch; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  /* The tabs scroll inside the bar instead of pushing it past the window. Their width
     depends on the fonts the machine has (a fallback sans can be far wider than Inter),
     so no breakpoint can promise they fit: when they do not, the bar keeps the search,
     the controls and the person menu on screen and the last tabs scroll into view. */
  .navbar-nav {
    display: flex; align-items: center; gap: 2px;
    min-width: 0; overflow-x: auto; scrollbar-width: thin;
    /* Room for the 4px focus ring, which the scroll box would otherwise clip. */
    padding: 4px; margin: 0 -4px;
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
  /* One line that truncates: wrapping a squeezed placeholder spills it out of the bar. */
  .search-trigger { min-width: 9rem; }
  .search-trigger-text { flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  /* In the trigger the key hint is a flex item after the text, never laid over it. */
  .search-trigger { padding-right: 10px; }
  .search-trigger .search-kbd { position: static; transform: none; flex-shrink: 0; }
  .navbar-right {
    display: flex; align-items: center; gap: var(--sp-2); margin-left: auto; flex-shrink: 0;
  }
  .source-tag :global(.source-dot) { flex-shrink: 0; }
  .source-tag {
    display: inline-flex; align-items: center; gap: 5px;
    color: var(--c-text-3);
    padding: 4px 10px; border-radius: var(--radius-xs);
    transition: background var(--transition), color var(--transition);
    font: var(--role-label-font); font-variant-caps: var(--role-label-caps); letter-spacing: var(--role-label-tracking);
  }
  .source-tag:hover { background: var(--c-surface-hover); color: var(--c-text-2); }
  .sources-trigger {
    display: inline-flex; align-items: center; gap: var(--sp-2); min-height: 36px;
    padding: 0 var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    background: none; color: var(--c-text-2); font: inherit; font-size: var(--text-sm); font-weight: 500;
    white-space: nowrap; cursor: pointer;
  }
  .sources-trigger:hover { color: var(--c-text); background: var(--c-surface-hover); }
  .sources-dots { display: inline-flex; }
  .sources-dots :global(.source-dot) { width: 8px; height: 8px; border-radius: 50%; font-size: 0; }
  .sources-dots :global(.source-dot + .source-dot) { margin-left: 3px; }
  .sources-busy { font-size: var(--text-xs); color: var(--c-text-3); }
  /* Parked under the bar's right edge, where its trigger is. Fixed coordinates rather
     than anchor positioning, so it lands the same in every browser. */
  .sources-pop {
    inset: auto; position: fixed; top: calc(var(--navbar-h) + var(--sp-1)); right: var(--sp-8);
    width: min(340px, calc(100vw - 2 * var(--sp-4))); margin: 0; padding: var(--sp-3) var(--sp-4);
    border: 1px solid var(--c-border); border-radius: var(--radius-md);
    background: var(--c-surface); color: var(--c-text); box-shadow: var(--shadow-md);
  }
  .sources-pop-title { margin: 0 0 var(--sp-2); }
  .sources-pop-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .sources-pop-list li { display: flex; align-items: flex-start; gap: var(--sp-2); font-size: var(--text-sm); }
  .sources-pop-list li > span:last-child { display: flex; flex-direction: column; min-width: 0; }
  .sources-pop-desc { overflow-wrap: anywhere; }
  .sources-pop-note { margin: var(--sp-2) 0 0; }
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
    transition: var(--motion-feedback); transition-property: var(--motion-tint);
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

  /* The Menu button: a labelled button, hidden on desktop. */
  .hamburger { display: none; margin-left: auto; flex-shrink: 0; }

  /* Mobile drawer — hidden on desktop */
  .mobile-drawer {
    display: none;
    position: sticky; top: var(--navbar-h); z-index: 99;
    background: var(--c-surface); border-bottom: 1px solid var(--c-border);
    padding: var(--sp-4);
    /* Usable at a 375px-high landscape phone: the list scrolls inside the drawer. */
    max-block-size: calc(100vh - var(--navbar-h)); max-block-size: calc(100dvh - var(--navbar-h));
    overflow-y: auto; overscroll-behavior: contain;
    animation: slideDown 150ms ease-out both;
  }
  .mobile-nav {
    margin-bottom: var(--sp-3); padding-bottom: var(--sp-3);
    border-bottom: 1px solid var(--c-border);
  }
  .mobile-nav > nav { display: flex; flex-direction: column; gap: 2px; }
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

  /* ─── Tight ─── */
  /* The legacy host's four items and its sources control share this bar with the
     search; below 1440px it sheds the version tag and some padding first. */
  @media (max-width: 1440px) {
    .navbar { padding: 0 var(--sp-4); }
    .nav-link { padding: 6px 5px; }
    .version-tag { display: none; }
    .search-trigger { min-width: 7rem; }
  }

  /* ─── Phone ─── */
  /* Under 768px the sidebar is gone (SPEC 20): the destinations, the controls and the
     person menu move into the Menu drawer. */
  @media (max-width: 767.98px) {
    .navbar {
      padding: 0 var(--sp-4);
      gap: var(--sp-2);
    }
    .navbar-right-desktop { display: none; }
    .navbar-nav-desktop { display: none; }
    .hamburger { display: inline-flex; align-items: center; }
    .mobile-drawer { display: block; }
    .search-kbd { display: none; }
    /* At 390px a flex:1 text field between the brand and the hamburger had no room for
       its own placeholder, which wrapped onto two lines inside a squeezed box -- the one
       control on the bar that looked broken. Collapse it to the standard magnifier
       button: same tap target, same overlay, and the accessible name still comes from
       aria-label, so nothing is lost for a screen reader. */
    .search-box { max-width: none; flex: 0 0 auto; margin-left: auto; }
    /* min-width: 0 drops the desktop 9rem floor, which kept the magnifier wide enough to
       push the menu button off a 320px screen. */
    .search-trigger { width: var(--touch-min); min-width: 0; padding: 0; justify-content: center; }
    .search-trigger .search-icon { position: static; transform: none; }
    .search-trigger-text { display: none; }
  }
  @media (max-width: 480px) {
    .version-tag { display: none; }
  }
  /* SPEC 23, 24.4: 44px targets on a touch screen; the padding grows, not the font. */
  @media (pointer: coarse) {
    .hamburger { min-height: 44px; }
    .navbar-right :global(.btn-ghost) { width: 44px; height: 44px; }
    .search-trigger, .sources-trigger, .mobile-nav-link { min-height: 44px; }
    .search-trigger { min-width: 44px; }
  }
</style>
