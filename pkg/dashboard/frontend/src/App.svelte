<script>
  import { onMount } from 'svelte';
  import { parseHash } from './lib/router.js';
  import { api } from './lib/api.js';
  import Navbar from './Navbar.svelte';
  import ServiceListView from './views/ServiceListView.svelte';
  import ServiceDetailView from './views/ServiceDetailView.svelte';
  import GraphPageView from './views/GraphPageView.svelte';
  import DiffView from './views/DiffView.svelte';

  let route = $state(parseHash(location.hash));
  let services = $state([]);
  let sourcesInfo = $state([]);
  let discovering = $state(false);
  let appVersion = $state('');
  let autoReload = $state(true);
  let reloadTimer = $state(null);
  let refreshing = $state(false);
  let refreshTick = $state(0);

  function onHashChange() {
    route = parseHash(location.hash);
  }

  async function loadGlobal() {
    refreshing = true;
    try {
      const [svcList, srcData, health] = await Promise.all([
        api.services(),
        api.sources().catch(() => ({ sources: [], discovering: false })),
        api.health().catch(() => ({})),
      ]);
      services = svcList || [];
      sourcesInfo = srcData.sources || [];
      discovering = srcData.discovering || false;
      appVersion = health.version || '';
      refreshTick++;
    } catch {
      // keep stale data
    }
    refreshing = false;
  }

  function toggleAutoReload() {
    autoReload = !autoReload;
    if (autoReload) {
      reloadTimer = setInterval(loadGlobal, 10000);
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
    // Start auto-reload by default
    reloadTimer = setInterval(loadGlobal, 10000);
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
  onRefresh={loadGlobal}
  onToggleAutoReload={toggleAutoReload}
  onToggleTheme={toggleTheme}
/>

<main class="container">
  {#if route.view === 'detail'}
    {#key route.params.name}
      <ServiceDetailView name={route.params.name} {services} {refreshTick} onServiceResolved={loadGlobal} />
    {/key}
  {:else if route.view === 'diff'}
    <DiffView name={route.params.name} initialFrom={route.params.from} initialTo={route.params.to} />
  {:else if route.view === 'graph'}
    <GraphPageView {services} {sourcesInfo} />
  {:else}
    <ServiceListView {services} {sourcesInfo} {discovering} />
  {/if}
</main>
