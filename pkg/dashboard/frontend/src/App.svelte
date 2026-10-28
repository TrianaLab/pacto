<script>
  import { onMount } from 'svelte';
  import { parseHash, legacyRedirectTarget, replaceHash, fleetOverviewUrl } from './lib/router.ts';
  import { syncFromHash } from './lib/filters.svelte.ts';
  import { toggleTheme } from './lib/theme.svelte.ts';
  import { initTooltipPlacement } from './lib/tooltips.ts';
  import { syncPageTitle } from './lib/pageTitle.ts';
  import { initScrollRestore } from './lib/scrollRestore.ts';
  import { api } from './lib/api.ts';
  import { deliverySession, loadSession } from './lib/deliverySession.svelte.ts';
  import { hostSignsIn, teamGroups } from './lib/delivery.ts';
  import Person from './components/Person.svelte';
  import SignInScreen from './components/SignInScreen.svelte';
  import CappedList from './components/CappedList.svelte';
  import Navbar from './Navbar.svelte';
  import LazyView from './components/LazyView.svelte';
  import EmptyState from './components/EmptyState.svelte';
  import CommandPalette from './CommandPalette.svelte';
  import EntitySearch from './EntitySearch.svelte';
  import ServiceListView from './views/ServiceListView.svelte';
  import OwnersView from './views/OwnersView.svelte';
  import FleetOverview from './views/FleetOverview.svelte';
  import FleetServicesView from './views/FleetServicesView.svelte';
  import FleetOwnersView from './views/FleetOwnersView.svelte';
  import FleetSourcesView from './views/FleetSourcesView.svelte';
  import FleetEntityListView from './views/FleetEntityListView.svelte';
  import FleetAttentionView from './views/FleetAttentionView.svelte';
  import LegacyEntityRedirect from './views/LegacyEntityRedirect.svelte';

  // The views that carry cytoscape, d3 and the charts load on first use.
  const once = (f) => { let p; return () => (p ??= f()); };
  const loadServiceDetailView = once(() => import('./views/ServiceDetailView.svelte'));
  const loadGraphPageView = once(() => import('./views/GraphPageView.svelte'));
  const loadDiffView = once(() => import('./views/DiffView.svelte'));
  const loadReadinessView = once(() => import('./views/ReadinessView.svelte'));
  const loadGraphView = once(() => import('./views/GraphView.svelte'));
  const loadFleetEntityView = once(() => import('./views/FleetEntityView.svelte'));
  const loadChangeAnalysisView = once(() => import('./views/ChangeAnalysisView.svelte'));
  const loadOwnerDetailView = once(() => import('./views/OwnerDetailView.svelte'));
  // Delivery views load on first use too: most visitors never open them.
  const loadDeliveryInboxView = once(() => import('./views/DeliveryInboxView.svelte'));
  const loadDeliveryEnvironmentsView = once(() => import('./views/DeliveryEnvironmentsView.svelte'));
  const loadDeliveryAutonomyView = once(() => import('./views/DeliveryAutonomyView.svelte'));
  const loadDeliveryChangeView = once(() => import('./views/DeliveryChangeView.svelte'));
  const loadDeliveryHistoryView = once(() => import('./views/DeliveryHistoryView.svelte'));
  const loadDeliveryPublishView = once(() => import('./views/DeliveryPublishView.svelte'));

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
  // The capabilities probe failed and none is known yet: every gated route would
  // otherwise wait on "Loading…" for ever, so they say so and offer a retry.
  let capError = $state(false);

  // A dashboard people sign in to shows nothing but its sign-in screen until
  // they do; the server answers no API call before that. A failed session read
  // (the offline export has none) is no sign-in to wait for.
  const session = $derived(deliverySession.data);
  const gated = $derived(!!session && session.mode === 'shared' && (session.login || session.tokenLogin) && !session.signedIn && !hostSignsIn());
  const nothingVisible = $derived(!!capabilities?.auth?.nothingVisible);

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
    // The poll fires on a fixed interval whether or not the previous pass has
    // returned. On a large fleet one pass outlives the 2s discovery interval, so
    // without this guard each slow pass stacks another concurrent pass on top of
    // it and the backend is asked the same questions several times over. A manual
    // refresh is the user asking, so it is never dropped.
    if ((refreshing && !forceRefresh) || gated) return;
    refreshing = true;
    try {
      if (forceRefresh) {
        await api.refresh().catch(() => {});
      }

      // Capabilities are probed BEFORE the rest, not alongside them. They decide
      // whether the legacy services plane is fetched at all, so racing them meant
      // the first pass on a Fleet host always read `capabilities === null`, took the
      // legacy branch and issued a whole-fleet /api/services -- the most expensive
      // call this server serves, on the one host that never reads the result. One
      // extra round trip on first load buys that back.
      // Asked again on a refresh, and on every poll while a delivery URL is
      // open without delivery, so a cluster that was briefly unreachable or
      // got the chart later shows its Delivery tab without a reload.
      if (capabilities === null || forceRefresh || (route.view.startsWith('delivery-') && capabilities.delivery !== true)) {
        const caps = await api.capabilities().catch(() => null);
        if (caps) capabilities = caps;
        capError = !caps && capabilities === null;
      }

      // The legacy services plane is a NON-Fleet-host concern only. Compare and Readiness
      // used to fetch it on every host; on a Fleet host they are now Change analysis and
      // the revision/attention readiness surfaces, which are Product-API-only. So no
      // product route triggers api.services() any more (Part 1.5).
      const needsServices = capabilities?.fleet !== true &&
        (route.view === 'list' || route.view === 'detail' || route.view === 'graph' || route.view === 'owners' ||
         route.view === 'owner-detail' || route.view === 'diff' || route.view === 'readiness');

      let servicesFailed = false;
      const [svcList, srcData, health] = await Promise.all([
        needsServices ? api.services().catch(() => { servicesFailed = true; return null; }) : Promise.resolve(null),
        api.sources().catch(() => null),
        api.health().catch(() => null),
      ]);
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
    // global shortcut must not hijack them. contenteditable
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
      // Never stack the palette over the open Search overlay; the
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

  // The text the palette handed to the entity search.
  let searchInitial = $state('');
  function handlePaletteAction(id, cmd) {
    if (id === 'search') { searchInitial = cmd?.query ?? ''; searchOpen = true; }
    else if (id === 'theme') toggleTheme();
    else if (id === 'refresh') loadGlobal(true);
    else if (id === 'autoreload') toggleAutoReload();
  }

  let mainEl = $state(null);

  // A host whose state changes outside the UI (the browser demo, when its
  // simulated promoter moves or the reader switches person) dispatches
  // pacto:refresh so every view reads again now, not at the next poll.
  function onHostRefresh() {
    loadSession();
    loadGlobal(true);
  }

  onMount(() => {
    window.addEventListener('hashchange', onHashChange);
    window.addEventListener('keydown', handlePaletteKeydown);
    window.addEventListener('pacto:refresh', onHostRefresh);
    window.addEventListener('pacto:unauthorized', loadSession);
    const teardownTips = initTooltipPlacement();
    // Every route used to be titled "Pacto Dashboard". The title now follows the
    // page's own h1, so a tab, a history entry and a screen reader all name the
    // page the user is on.
    const teardownTitle = syncPageTitle(mainEl);
    // Async content means the browser's own scroll restoration always loses: it
    // restores against an empty page. We own it instead.
    const teardownScroll = initScrollRestore();
    // Who is asking comes first: a signed-out caller gets the sign-in screen
    // and the fleet is never read.
    loadSession().then(() => {
      if (gated) return;
      loadGlobal();
      // Start with fast polling; loadGlobal adjusts interval based on discovery state
      if (!(globalThis).__PACTO_STATIC__) { reloadTimer = setInterval(loadGlobal, POLL_FAST); }
    });
    return () => {
      window.removeEventListener('hashchange', onHashChange);
      window.removeEventListener('keydown', handlePaletteKeydown);
      window.removeEventListener('pacto:refresh', onHostRefresh);
      window.removeEventListener('pacto:unauthorized', loadSession);
      teardownTips();
      teardownTitle();
      teardownScroll();
      if (reloadTimer) clearInterval(reloadTimer);
    };
  });
</script>

{#if deliverySession.loaded && !gated}
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
  delivery={capabilities?.delivery === true}
  onClose={() => (paletteOpen = false)}
  onAction={handlePaletteAction}
/>

<EntitySearch open={searchOpen} initial={searchInitial} onClose={() => { searchOpen = false; searchInitial = ''; }} />

{#if loadError && services.length > 0}
  <div class="backend-banner" role="alert">
    <span>{loadError} Showing the last known data and retrying…</span>
    <button type="button" class="banner-retry" onclick={() => loadGlobal(true)}>Retry now</button>
  </div>
{/if}

{#if capabilities?.auth?.hidden && !nothingVisible && !route.view.startsWith('delivery-')}
  <p class="permissions-note" role="status" data-testid="hidden-by-permissions">
    Some services are hidden because your Kubernetes permissions do not let you read their namespace.
  </p>
{/if}

{/if}

{#snippet migrating()}
  {#if capError}
    <div class="product-page" role="alert" data-testid="capabilities-error">
      <h1 class="t-page-title">Can’t reach the Pacto backend</h1>
      <p class="t-body">The dashboard could not ask the server what it serves, so it cannot tell which page to show.</p>
      <button type="button" class="btn" onclick={() => loadGlobal(true)}>Retry</button>
    </div>
  {:else}
    <p class="app-loading" role="status">Loading…</p>
  {/if}
{/snippet}

<main class="container" bind:this={mainEl}>
  {#if gated}
    <SignInScreen />
  {:else if !deliverySession.loaded}
    <!-- Who is asking comes first; the browser demo answers only once its engine is up. -->
    <EmptyState loading />
  {:else if nothingVisible && !route.view.startsWith('delivery-')}
    <!-- Every service is in a namespace this person's RBAC does not let them read. -->
    <div class="product-page" data-testid="nothing-visible">
      <h1 class="t-page-title">You can see no services</h1>
      <p class="t-body">
        Your Kubernetes permissions let you read none of the namespaces this dashboard's services run in.
        You are signed in as <strong>{#if session?.user}<Person user={session.user} me={{ user: session.user, name: session.name }} />{:else}{session?.name || 'an unnamed user'}{/if}</strong>{#if teamGroups(session?.groups).length}, in <CappedList items={teamGroups(session.groups)} max={5} inline label="Your groups" testid="nothing-visible-groups">{#snippet item(g)}{g}{/snippet}</CappedList>{:else}, in no groups{/if}.
      </p>
      <p class="t-body">To see a team's services, ask a cluster administrator for <code>list</code> on <code>pactos.pacto.trianalab.io</code> in that team's namespace.</p>
    </div>
  {:else if route.view === 'detail'}
    <!-- Service detail: the product entity page on a Fleet host (resolved via the
         Product API); the legacy view only on a non-Fleet host, where it is the only UI. -->
    {#if legacyHost}
      {#key route.params.name + '@@' + (route.params.version || '')}
        <LazyView load={loadServiceDetailView} name={route.params.name} version={route.params.version || null} {services} {refreshTick} onServiceResolved={loadGlobal} />
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
      <LazyView load={loadDiffView}
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
      <LazyView load={loadGraphPageView} {services} {sourcesInfo} />
    {:else}
      {@render migrating()}
    {/if}
  {:else if route.view === 'readiness'}
    <!-- Legacy Readiness: a service-name-keyed third definition of preparedness. On a
         Fleet host readiness is a property of the CONTRACT REVISION that declares it, so
         this route redirects (via the effect) to the revision inventory -- the whole
         assessed population, filterable by assessment -- rather than mounting a competing
         screen keyed by a unit that does not own the fact. -->
    {#if legacyHost}
      <LazyView load={loadReadinessView} {services} {initialLoading} />
    {:else}
      {@render migrating()}
    {/if}
  {:else if route.view === 'fleet-overview'}
    <FleetOverview {refreshTick} />
  {:else if route.view === 'fleet-services'}
    <FleetServicesView
      text={route.params.text || ''}
      owner={route.params.owner || ''}
      ownerKey={route.params.ownerKey || ''}
      ownership={route.params.ownership || ''}
      status={route.params.status || ''}
      domain={route.params.domain || ''}
      offset={route.params.offset || ''}
      limit={route.params.limit || ''}
      sort={route.params.sort || ''}
      {refreshTick}
    />
  {:else if route.view === 'fleet-owners'}
    <FleetOwnersView text={route.params.text || ''} offset={route.params.offset || ''}
      limit={route.params.limit || ''}
      sort={route.params.sort || ''} {refreshTick} />
  {:else if route.view === 'fleet-sources'}
    <FleetSourcesView
      text={route.params.text || ''}
      sourceHealth={route.params.sourceHealth || ''}
      offset={route.params.offset || ''}
      limit={route.params.limit || ''}
      sort={route.params.sort || ''}
      {refreshTick}
    />
  {:else if route.view === 'fleet-entity-list'}
    <FleetEntityListView
      kind={route.params.kind || 'revision'}
      service={route.params.service || ''}
      text={route.params.text || ''}
      status={route.params.status || ''}
      scope={route.params.scope || ''}
      readiness={route.params.readiness || ''}
      offset={route.params.offset || ''}
      limit={route.params.limit || ''}
      sort={route.params.sort || ''}
      {refreshTick}
    />
  {:else if route.view === 'fleet-entity'}
    {#key route.params.kind + '@@' + route.params.key}
      <LazyView load={loadFleetEntityView} kind={route.params.kind} entityKey={route.params.key} {refreshTick} />
    {/key}
  {:else if route.view === 'fleet-attention'}
    <FleetAttentionView
      category={route.params.category || ''}
      severity={route.params.severity || ''}
      status={route.params.status || ''}
      owner={route.params.owner || ''}
      ownerKey={route.params.ownerKey || ''}
      source={route.params.source || ''}
      service={route.params.service || ''}
      staleOnly={route.params.staleOnly || ''}
      offset={route.params.offset || ''}
      limit={route.params.limit || ''}
      {refreshTick}
    />
  {:else if route.view === 'fleet'}
    <LazyView load={loadGraphView} params={route.params} {refreshTick} />
  {:else if route.view === 'changes'}
    {#key JSON.stringify(route.params)}
      <LazyView load={loadChangeAnalysisView} params={route.params} />
    {/key}
  {:else if route.view.startsWith('delivery-')}
    <!-- Delivery: only where the cluster serves the delivery API and the dashboard
         knows who the caller is. Anywhere else the views say why they are absent. -->
    {#if capabilities?.delivery === true}
      {#if route.view === 'delivery-inbox'}
        <LazyView load={loadDeliveryInboxView} {...route.params} {refreshTick} />
      {:else if route.view === 'delivery-environments'}
        <LazyView load={loadDeliveryEnvironmentsView} {...route.params} {refreshTick} miraURL={capabilities.miraURL || ''} />
      {:else if route.view === 'delivery-autonomy'}
        <LazyView load={loadDeliveryAutonomyView} {...route.params} {refreshTick} miraURL={capabilities.miraURL || ''} />
      {:else if route.view === 'delivery-change'}
        {#key route.params.namespace + '/' + route.params.name}
          <LazyView load={loadDeliveryChangeView} namespace={route.params.namespace} name={route.params.name} {refreshTick} miraURL={capabilities.miraURL || ''} />
        {/key}
      {:else if route.view === 'delivery-history'}
        <LazyView load={loadDeliveryHistoryView} {...route.params} {refreshTick} miraURL={capabilities.miraURL || ''} />
      {:else}
        <LazyView load={loadDeliveryPublishView} {...route.params} {refreshTick} />
      {/if}
    {:else if capabilities}
      <div class="product-page" data-testid="delivery-unavailable">
        <h1 class="t-page-title">Delivery is not available here</h1>
        {#if !session || session.mode === 'open' || session.mode === 'none'}
          <p class="t-body" data-testid="delivery-unavailable-reason">Every delivery action is signed by a person, and this dashboard has no signed-in user. Delivery views appear in <code>pacto dashboard</code> on your own machine, or in a shared dashboard people sign in to.</p>
        {:else}
          <p class="t-body" data-testid="delivery-unavailable-reason">The cluster this dashboard reads does not serve the delivery API. Install pacto-operator with <code>delivery.enabled=true</code>.</p>
        {/if}
      </div>
    {:else}
      {@render migrating()}
    {/if}
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
        <LazyView load={loadOwnerDetailView} owner={route.params.owner} {services} {initialLoading} />
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
  .permissions-note {
    margin: 0; padding: var(--sp-2) var(--sp-3); text-align: center;
    font-size: var(--text-sm); color: var(--c-text-2);
    border-bottom: 1px solid var(--c-border);
  }
</style>
