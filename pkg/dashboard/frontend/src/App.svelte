<script>
  import { onMount } from 'svelte';
  import { parseHash, legacyRedirectTarget, replaceHash, fleetOverviewUrl } from './lib/router.ts';
  import { syncFromHash } from './lib/filters.svelte.ts';
  import { toggleTheme } from './lib/theme.svelte.ts';
  import { initTooltipPlacement } from './lib/tooltips.ts';
  import { syncPageTitle } from './lib/pageTitle.ts';
  import { initScrollRestore } from './lib/scrollRestore.ts';
  import { api } from './lib/api.ts';
  import Navbar from './Navbar.svelte';
  import CommandPalette from './CommandPalette.svelte';
  import EntitySearch from './EntitySearch.svelte';
  import ServiceListView from './views/ServiceListView.svelte';
  import ServiceDetailView from './views/ServiceDetailView.svelte';
  import GraphPageView from './views/GraphPageView.svelte';
  import DiffView from './views/DiffView.svelte';
  import OwnersView from './views/OwnersView.svelte';
  import OwnerDetailView from './views/OwnerDetailView.svelte';
  import ReadinessView from './views/ReadinessView.svelte';
  import GraphView from './views/GraphView.svelte';
  import FleetOverview from './views/FleetOverview.svelte';
  import FleetServicesView from './views/FleetServicesView.svelte';
  import FleetOwnersView from './views/FleetOwnersView.svelte';
  import FleetSourcesView from './views/FleetSourcesView.svelte';
  import FleetEntityListView from './views/FleetEntityListView.svelte';
  import FleetEntityView from './views/FleetEntityView.svelte';
  import FleetAttentionView from './views/FleetAttentionView.svelte';
  import ChangeAnalysisView from './views/ChangeAnalysisView.svelte';
  import LegacyEntityRedirect from './views/LegacyEntityRedirect.svelte';

  let route = $state(parseHash(location.hash));
  let services = $state([]);
  let sourcesInfo = $state([]);
  let discovering = $state(false);
  let appVersion = $state('');
  let autoReload = $state(true);
  let reloadTimer = $state(null);
  let refreshing = $state(false);
  let refreshTick = $state(0);
  let initialLoading = $state(true);
  let loadError = $state(null);
  let paletteOpen = $state(false);
  let searchOpen = $state(false);
  // Which optional capabilities the host serves, so the navbar never exposes a
  // capability the running host has not registered. null = not yet known (show
  // everything until the first probe resolves).
  let capabilities = $state(null);

  const POLL_FAST = 2000;   // during discovery
  const POLL_NORMAL = 10000;
  let reloadInterval = POLL_FAST; // the currently-active poll interval (ms)

  function onHashChange() {
    route = parseHash(location.hash);
    // Reflect external hash changes (back/forward, shared links) into the shared
    // filter store so the views' filtered set stays in sync with the URL.
    syncFromHash();
  }

  async function loadGlobal(forceRefresh = false) {
    refreshing = true;
    try {
      if (forceRefresh) {
        await api.refresh().catch(() => {});
      }

      // The legacy services plane is a NON-Fleet-host concern only. Compare and Readiness
      // used to fetch it on every host; on a Fleet host they are now Change analysis and
      // the revision/attention readiness surfaces, which are Product-API-only. So no
      // product route triggers api.services() any more (Part 1.5).
      const needsServices = capabilities?.fleet !== true &&
        (route.view === 'list' || route.view === 'graph' || route.view === 'owners' ||
         route.view === 'owner-detail' || route.view === 'diff' || route.view === 'readiness');

      let servicesFailed = false;
      const [svcList, srcData, health, caps] = await Promise.all([
        needsServices ? api.services().catch(() => { servicesFailed = true; return null; }) : Promise.resolve(null),
        api.sources().catch(() => null),
        api.health().catch(() => null),
        capabilities === null ? api.capabilities().catch(() => null) : Promise.resolve(null),
      ]);
      if (caps) capabilities = caps;
      // A failed services fetch means the backend is unreachable or erroring — not
      // that the fleet is genuinely empty. Keep any stale data and flag the error
      // so views show "can't reach backend" instead of the "no sources" setup screen.
      loadError = servicesFailed ? 'Can’t reach the Pacto backend.' : null;
      if (!servicesFailed && svcList !== null) services = svcList || [];
      if (srcData) sourcesInfo = srcData.sources || [];
      discovering = srcData?.discovering || false;
      appVersion = health?.version || appVersion;
      refreshTick++;

      // Reconcile polling speed to the current discovery state: fast during
      // discovery, normal otherwise. Compare against the ACTIVE interval (not the
      // previous discovery flag) so the first settle to "not discovering" also slows
      // the poll from the initial fast rate instead of staying stuck at 2s.
      if (autoReload) {
        const desired = discovering ? POLL_FAST : POLL_NORMAL;
        if (reloadInterval !== desired) {
          reloadInterval = desired;
          clearInterval(reloadTimer);
          reloadTimer = setInterval(loadGlobal, desired);
        }
      }
    } catch {
      // keep stale data, but surface that the refresh failed
      loadError = 'Can’t reach the Pacto backend.';
    }
    refreshing = false;
    initialLoading = false;
  }

  function toggleAutoReload() {
    autoReload = !autoReload;
    if (autoReload) {
      reloadInterval = discovering ? POLL_FAST : POLL_NORMAL;
      reloadTimer = setInterval(loadGlobal, reloadInterval);
    } else {
      clearInterval(reloadTimer);
      reloadTimer = null;
    }
  }

  // toggleTheme lives in the reactive theme store so D3 charts re-render on toggle.

  function isTypingTarget(e) {
    const t = e.target;
    // SELECT is included: native selects use '/' and letter keys for type-ahead, so a
    // global shortcut must not hijack them (Phase 5, requirement 8.5). contenteditable
    // is covered via isContentEditable.
    return t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.tagName === 'SELECT' || t.isContentEditable);
  }

  // A6: the primary search affordance (the visible Search button and '/') opens the
  // global fleet EntitySearch; Cmd/Ctrl-K opens the command palette. Only when fleet
  // capability is EXPLICITLY false does the affordance fall back to the command
  // palette rather than opening a dead fleet search. While capability is still
  // unprobed (null) we open fleet search optimistically (the common case; it fails
  // gracefully), so the shortcut is never dead-on-arrival on a fleet host.
  const fleetSearch = $derived(capabilities?.fleet !== false);

  // Host class for the dual-UI boundary (Part 1). A Fleet-capable host serves the
  // product IA and redirects legacy routes to it; a non-Fleet host (the offline `pacto
  // doc` export) serves the legacy UI as its ONLY UI. `null` = capabilities not yet
  // known: render a neutral loading state for legacy-equivalent routes rather than
  // committing to either UI (so a Fleet host never flashes the superseded screen).
  const fleetHost = $derived(capabilities?.fleet === true);
  const legacyHost = $derived(capabilities?.fleet === false);

  // On a Fleet-capable host, canonicalize a legacy URL that has a product equivalent to
  // its canonical product route -- a replace (no history push), so Back never bounces
  // and a reload stays on the product URL. Static 1:1 routes and the catch-all legacy
  // list (incl. unknown hashes) redirect here; name-bearing legacy detail URLs are
  // migrated by LegacyEntityRedirect, which resolves the name through the Product API.
  $effect(() => {
    const _ = route; // re-run on every navigation
    if (capabilities?.fleet !== true) return;
    const target = legacyRedirectTarget(location.hash);
    if (target) { replaceHash(target); return; }
    if (route.view === 'list') replaceHash(fleetOverviewUrl());
  });

  function openSearch() {
    if (fleetSearch) searchOpen = true;
    else paletteOpen = true;
  }

  function handlePaletteKeydown(e) {
    if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
      // Never stack the palette over the open Search overlay (requirement 8.5); the
      // palette's own Escape/Cmd-K toggles it closed when it is the active overlay.
      if (searchOpen) return;
      e.preventDefault();
      paletteOpen = !paletteOpen;
    } else if (e.key === '/' && !isTypingTarget(e) && !paletteOpen && !searchOpen) {
      // '/' is the discovery shortcut for the primary search affordance; Cmd/Ctrl-K
      // stays the command palette.
      e.preventDefault();
      openSearch();
    }
  }

  function handlePaletteAction(id) {
    if (id === 'theme') toggleTheme();
    else if (id === 'refresh') loadGlobal(true);
    else if (id === 'autoreload') toggleAutoReload();
  }

  let mainEl = $state(null);

  onMount(() => {
    window.addEventListener('hashchange', onHashChange);
    window.addEventListener('keydown', handlePaletteKeydown);
    const teardownTips = initTooltipPlacement();
    // Every route used to be titled "Pacto Dashboard". The title now follows the
    // page's own h1, so a tab, a history entry and a screen reader all name the
    // page the user is on.
    const teardownTitle = syncPageTitle(mainEl);
    // Async content means the browser's own scroll restoration always loses: it
    // restores against an empty page. We own it instead.
    const teardownScroll = initScrollRestore();
    loadGlobal();
    // Start with fast polling; loadGlobal adjusts interval based on discovery state
    if (!(globalThis).__PACTO_STATIC__) { reloadTimer = setInterval(loadGlobal, POLL_FAST); }
    return () => {
      window.removeEventListener('hashchange', onHashChange);
      window.removeEventListener('keydown', handlePaletteKeydown);
      teardownTips();
      teardownTitle();
      teardownScroll();
      if (reloadTimer) clearInterval(reloadTimer);
    };
  });
</script>

<Navbar
  {services}
  {sourcesInfo}
  {capabilities}
  view={route.view}
  entityKind={route.params.kind || ''}
  version={appVersion}
  {discovering}
  {autoReload}
  {refreshing}
  onRefresh={() => loadGlobal(true)}
  onToggleAutoReload={toggleAutoReload}
  onToggleTheme={toggleTheme}
  {fleetSearch}
  onOpenSearch={openSearch}
/>

<CommandPalette
  open={paletteOpen}
  {services}
  fleet={fleetHost}
  onClose={() => (paletteOpen = false)}
  onAction={handlePaletteAction}
/>

<EntitySearch open={searchOpen} onClose={() => (searchOpen = false)} />

{#if loadError && services.length > 0}
  <div class="backend-banner" role="alert">
    <span>{loadError} Showing the last known data — retrying…</span>
    <button type="button" class="banner-retry" onclick={() => loadGlobal(true)}>Retry now</button>
  </div>
{/if}

{#snippet migrating()}
  <p class="app-loading" role="status">Loading…</p>
{/snippet}

<main class="container" bind:this={mainEl}>
  {#if route.view === 'detail'}
    <!-- Service detail: the product entity page on a Fleet host (resolved via the
         Product API); the legacy view only on a non-Fleet host, where it is the only UI. -->
    {#if legacyHost}
      {#key route.params.name + '@@' + (route.params.version || '')}
        <ServiceDetailView name={route.params.name} version={route.params.version || null} {services} {refreshTick} onServiceResolved={loadGlobal} />
      {/key}
    {:else if fleetHost}
      {#key route.params.name + '@@' + (route.params.version || '')}
        <LegacyEntityRedirect kind="service" name={route.params.name} version={route.params.version || ''} />
      {/key}
    {:else}
      {@render migrating()}
    {/if}
  {:else if route.view === 'diff'}
    <!-- Legacy Compare: name+version keyed, so it is superseded on a Fleet host by the
         canonical Change analysis workspace (redirected via the effect). It remains the
         only compare screen on the non-Fleet doc export. -->
    {#if legacyHost}
      <DiffView
        name={route.params.name || ''}
        initialFrom={route.params.fromVer || route.params.from || ''}
        initialTo={route.params.toVer || route.params.to || ''}
        initialFromName={route.params.fromName || route.params.name || ''}
        initialToName={route.params.toName || route.params.name || ''}
        {services}
      />
    {:else}
      {@render migrating()}
    {/if}
  {:else if route.view === 'graph'}
    <!-- Legacy standalone graph: superseded by the Operational Graph on a Fleet host
         (redirected via the effect); retained for the non-Fleet doc export deep link. -->
    {#if legacyHost}
      <GraphPageView {services} {sourcesInfo} />
    {:else}
      {@render migrating()}
    {/if}
  {:else if route.view === 'readiness'}
    <!-- Legacy Readiness: a service-name-keyed third definition of preparedness. On a
         Fleet host readiness is a DIMENSION -- declared on the revision page that owns it
         and triaged as the Needs-attention readiness category -- so this route redirects
         there (via the effect) rather than mounting a competing screen. -->
    {#if legacyHost}
      <ReadinessView {services} {initialLoading} />
    {:else}
      {@render migrating()}
    {/if}
  {:else if route.view === 'fleet-overview'}
    <FleetOverview {refreshTick} />
  {:else if route.view === 'fleet-services'}
    <FleetServicesView
      text={route.params.text || ''}
      owner={route.params.owner || ''}
      status={route.params.status || ''}
      domain={route.params.domain || ''}
      offset={route.params.offset || ''}
      {refreshTick}
    />
  {:else if route.view === 'fleet-owners'}
    <FleetOwnersView text={route.params.text || ''} offset={route.params.offset || ''} {refreshTick} />
  {:else if route.view === 'fleet-sources'}
    <FleetSourcesView
      text={route.params.text || ''}
      sourceHealth={route.params.sourceHealth || ''}
      offset={route.params.offset || ''}
      {refreshTick}
    />
  {:else if route.view === 'fleet-entity-list'}
    <FleetEntityListView
      kind={route.params.kind || 'revision'}
      service={route.params.service || ''}
      text={route.params.text || ''}
      status={route.params.status || ''}
      scope={route.params.scope || ''}
      offset={route.params.offset || ''}
      {refreshTick}
    />
  {:else if route.view === 'fleet-entity'}
    {#key route.params.kind + '@@' + route.params.key}
      <FleetEntityView kind={route.params.kind} entityKey={route.params.key} {refreshTick} />
    {/key}
  {:else if route.view === 'fleet-attention'}
    <FleetAttentionView
      category={route.params.category || ''}
      severity={route.params.severity || ''}
      status={route.params.status || ''}
      owner={route.params.owner || ''}
      source={route.params.source || ''}
      service={route.params.service || ''}
      staleOnly={route.params.staleOnly || ''}
      offset={route.params.offset || ''}
      {refreshTick}
    />
  {:else if route.view === 'fleet'}
    <GraphView params={route.params} {refreshTick} />
  {:else if route.view === 'changes'}
    <ChangeAnalysisView params={route.params} />
  {:else if route.view === 'owners'}
    <!-- Owners: product owners on a Fleet host (redirected via the effect); legacy list
         only on a non-Fleet host. -->
    {#if legacyHost}
      <OwnersView {services} {initialLoading} />
    {:else}
      {@render migrating()}
    {/if}
  {:else if route.view === 'owner-detail'}
    {#if legacyHost}
      {#key route.params.owner}
        <OwnerDetailView owner={route.params.owner} {services} {initialLoading} />
      {/key}
    {:else if fleetHost}
      {#key route.params.owner}
        <LegacyEntityRedirect kind="owner" name={route.params.owner} />
      {/key}
    {:else}
      {@render migrating()}
    {/if}
  {:else}
    <!-- Catch-all legacy list: the only UI on a non-Fleet host; a Fleet host redirects
         it (and any unknown hash) to the operational overview via the effect. -->
    {#if legacyHost}
      <ServiceListView {services} {sourcesInfo} {discovering} {initialLoading} {loadError} onRetry={() => loadGlobal(true)} />
    {:else}
      {@render migrating()}
    {/if}
  {/if}
</main>

<style>
  .backend-banner {
    display: flex; align-items: center; justify-content: center; gap: var(--sp-3);
    flex-wrap: wrap;
    padding: var(--sp-2) var(--sp-3);
    background: var(--c-err-bg); color: var(--c-err);
    font-size: var(--text-sm); font-weight: 500;
    border-bottom: 1px solid color-mix(in srgb, var(--c-err) 30%, transparent);
  }
  .banner-retry {
    background: none; border: 1px solid currentColor; border-radius: var(--radius-xs);
    color: inherit; font: inherit; padding: 3px 10px; cursor: pointer;
  }
  .banner-retry:hover { background: color-mix(in srgb, var(--c-err) 12%, transparent); }
  .app-loading { color: var(--c-text-3); padding: var(--sp-4) 0; }
</style>
