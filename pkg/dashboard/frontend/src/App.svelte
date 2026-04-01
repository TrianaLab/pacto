<script>
  import { onMount } from 'svelte';
  import { parseHash } from './lib/router.ts';
  import { api } from './lib/api.ts';
  import Navbar from './Navbar.svelte';
  import ServiceListView from './views/ServiceListView.svelte';
  import ServiceDetailView from './views/ServiceDetailView.svelte';
  import GraphPageView from './views/GraphPageView.svelte';
  import DiffView from './views/DiffView.svelte';
  import OwnersView from './views/OwnersView.svelte';
  import OwnerDetailView from './views/OwnerDetailView.svelte';

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

  const POLL_FAST = 2000;   // during discovery
  const POLL_NORMAL = 10000;

  function onHashChange() {
    route = parseHash(location.hash);
  }

  async function loadGlobal(forceRefresh = false) {
    refreshing = true;
    try {
      if (forceRefresh) {
        await api.refresh().catch(() => {});
      }

      const needsServices = route.view === 'list' || route.view === 'graph' || route.view === 'diff' || route.view === 'owners' || route.view === 'owner-detail';

      const [svcList, srcData, health] = await Promise.all([
        needsServices ? api.services() : Promise.resolve(null),
        api.sources().catch(() => ({ sources: [], discovering: false })),
        api.health().catch(() => ({})),
      ]);
      if (svcList !== null) services = svcList || [];
      sourcesInfo = srcData.sources || [];
      const wasDiscovering = discovering;
      discovering = srcData.discovering || false;
      appVersion = health.version || '';
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
      // keep stale data
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
    loadGlobal();
    // Start with fast polling; loadGlobal adjusts interval based on discovery state
    reloadTimer = setInterval(loadGlobal, POLL_FAST);
    return () => {
      window.removeEventListener('hashchange', onHashChange);
      if (reloadTimer) clearInterval(reloadTimer);
    };
  });
</script>

<Navbar
  {services}
  {sourcesInfo}
  version={appVersion}
  {discovering}
  {autoReload}
  {refreshing}
  onRefresh={() => loadGlobal(true)}
  onToggleAutoReload={toggleAutoReload}
  onToggleTheme={toggleTheme}
/>

<main class="container">
  {#if route.view === 'detail'}
    {#key route.params.name}
      <ServiceDetailView name={route.params.name} {services} {refreshTick} onServiceResolved={loadGlobal} />
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
  {:else if route.view === 'owners'}
    <OwnersView {services} {initialLoading} />
  {:else if route.view === 'owner-detail'}
    {#key route.params.owner}
      <OwnerDetailView owner={route.params.owner} {services} {initialLoading} />
    {/key}
  {:else}
    <ServiceListView {services} {sourcesInfo} {discovering} {initialLoading} />
  {/if}
</main>
