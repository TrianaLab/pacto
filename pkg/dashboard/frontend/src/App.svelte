<script>
  import { onMount } from 'svelte';
  import { parseHash } from './lib/router.ts';
  import { syncFromHash } from './lib/filters.svelte.ts';
  import { initTooltipPlacement } from './lib/tooltips.ts';
  import { api } from './lib/api.ts';
  import Navbar from './Navbar.svelte';
  import ServiceListView from './views/ServiceListView.svelte';
  import ServiceDetailView from './views/ServiceDetailView.svelte';
  import GraphPageView from './views/GraphPageView.svelte';
  import DiffView from './views/DiffView.svelte';
  import OwnersView from './views/OwnersView.svelte';
  import OwnerDetailView from './views/OwnerDetailView.svelte';
  import ReadinessView from './views/ReadinessView.svelte';

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

  const POLL_FAST = 2000;   // during discovery
  const POLL_NORMAL = 10000;

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

      const needsServices = route.view === 'list' || route.view === 'graph' || route.view === 'diff' || route.view === 'owners' || route.view === 'owner-detail' || route.view === 'readiness';

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
      const wasDiscovering = discovering;
      discovering = srcData?.discovering || false;
      appVersion = health?.version || appVersion;
      refreshTick++;

      // Adjust polling speed: fast during discovery, normal otherwise
      if (autoReload) {
        const shouldBeFast = discovering;
        const wasFast = wasDiscovering;
        if (shouldBeFast !== wasFast) {
          clearInterval(reloadTimer);
          reloadTimer = setInterval(loadGlobal, shouldBeFast ? POLL_FAST : POLL_NORMAL);
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
      reloadTimer = setInterval(loadGlobal, discovering ? POLL_FAST : POLL_NORMAL);
    } else {
      clearInterval(reloadTimer);
      reloadTimer = null;
    }
  }

  function toggleTheme() {
    const root = document.documentElement;
    const current = root.getAttribute('data-theme');
    let isDark;
    if (current) isDark = current === 'dark';
    else isDark = matchMedia('(prefers-color-scheme: dark)').matches;
    const next = isDark ? 'light' : 'dark';
    root.setAttribute('data-theme', next);
    localStorage.setItem('pacto-theme', next);
  }

  onMount(() => {
    window.addEventListener('hashchange', onHashChange);
    const teardownTips = initTooltipPlacement();
    loadGlobal();
    // Start with fast polling; loadGlobal adjusts interval based on discovery state
    if (!(globalThis).__PACTO_STATIC__) { reloadTimer = setInterval(loadGlobal, POLL_FAST); }
    return () => {
      window.removeEventListener('hashchange', onHashChange);
      teardownTips();
      if (reloadTimer) clearInterval(reloadTimer);
    };
  });
</script>

<Navbar
  {services}
  {sourcesInfo}
  view={route.view}
  version={appVersion}
  {discovering}
  {autoReload}
  {refreshing}
  onRefresh={() => loadGlobal(true)}
  onToggleAutoReload={toggleAutoReload}
  onToggleTheme={toggleTheme}
/>

{#if loadError && services.length > 0}
  <div class="backend-banner" role="alert">
    <span>{loadError} Showing the last known data — retrying…</span>
    <button type="button" class="banner-retry" onclick={() => loadGlobal(true)}>Retry now</button>
  </div>
{/if}

<main class="container">
  {#if route.view === 'detail'}
    {#key route.params.name + '@@' + (route.params.version || '')}
      <ServiceDetailView name={route.params.name} version={route.params.version || null} {services} {refreshTick} onServiceResolved={loadGlobal} />
    {/key}
  {:else if route.view === 'diff'}
    <DiffView
      name={route.params.name || ''}
      initialFrom={route.params.fromVer || route.params.from || ''}
      initialTo={route.params.toVer || route.params.to || ''}
      initialFromName={route.params.fromName || route.params.name || ''}
      initialToName={route.params.toName || route.params.name || ''}
      {services}
    />
  {:else if route.view === 'graph'}
    <GraphPageView {services} {sourcesInfo} />
  {:else if route.view === 'readiness'}
    <ReadinessView {services} {initialLoading} />
  {:else if route.view === 'owners'}
    <OwnersView {services} {initialLoading} />
  {:else if route.view === 'owner-detail'}
    {#key route.params.owner}
      <OwnerDetailView owner={route.params.owner} {services} {initialLoading} />
    {/key}
  {:else}
    <ServiceListView {services} {sourcesInfo} {discovering} {initialLoading} {loadError} onRetry={() => loadGlobal(true)} />
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
</style>
