<script>
  import { onMount } from 'svelte';
  import { api } from './lib/api.js';
  import {
    currentView, currentService, services, graphData,
    sourcesInfo, discovering, appVersion, navigateTo, initFromHash,
  } from './lib/stores.js';
  import Navbar from './components/Navbar.svelte';
  import ServiceList from './components/ServiceList.svelte';
  import ServiceDetail from './components/ServiceDetail.svelte';

  let autoReloadEnabled = $state(localStorage.getItem('pacto-auto-reload') === 'true');
  let autoReloadTimer = $state(null);
  let discoveryTimer = $state(null);
  let spinning = $state(false);

  function startAutoReload() {
    stopAutoReload();
    autoReloadTimer = setInterval(doRefresh, 10000);
  }

  function stopAutoReload() {
    if (autoReloadTimer) {
      clearInterval(autoReloadTimer);
      autoReloadTimer = null;
    }
  }

  function toggleAutoReload() {
    autoReloadEnabled = !autoReloadEnabled;
    localStorage.setItem('pacto-auto-reload', String(autoReloadEnabled));
    if (autoReloadEnabled) startAutoReload();
    else stopAutoReload();
  }

  async function doRefresh() {
    if (document.hidden) return;
    spinning = true;
    setTimeout(() => (spinning = false), 600);
    await loadOverviewData();
  }

  async function loadOverviewData() {
    try {
      const [svcList, srcResp, graph] = await Promise.all([
        api.listServices(),
        api.getSources().catch(() => ({ sources: [], discovering: false })),
        api.getGraph().catch(() => null),
      ]);
      services.set(svcList || []);
      applySourcesResponse(srcResp);
      graphData.set(graph);
      scheduleDiscoveryRefresh();
    } catch {
      // keep stale data
    }
  }

  function applySourcesResponse(r) {
    if (Array.isArray(r)) {
      sourcesInfo.set(r);
      discovering.set(false);
    } else if (r?.sources) {
      sourcesInfo.set(r.sources);
      discovering.set(!!r.discovering);
    } else {
      sourcesInfo.set([]);
      discovering.set(false);
    }
  }

  function scheduleDiscoveryRefresh() {
    let disc;
    discovering.subscribe((v) => (disc = v))();
    if (disc && !discoveryTimer) {
      discoveryTimer = setInterval(doRefresh, 2000);
    } else if (!disc && discoveryTimer) {
      clearInterval(discoveryTimer);
      discoveryTimer = null;
    }
  }

  onMount(() => {
    initFromHash();

    // Fetch app version
    api.getHealth().then((d) => {
      if (d.version) appVersion.set(d.version);
    }).catch(() => {});

    // Load initial data
    loadOverviewData();

    if (autoReloadEnabled) startAutoReload();

    // Handle popstate for browser back/forward
    const onPopState = () => {
      const hash = location.hash;
      if (hash.startsWith('#service/')) {
        const svc = decodeURIComponent(hash.substring(9));
        currentView.set('detail');
        currentService.set(svc);
      } else if (hash === '#graph') {
        currentView.set('list');
      } else {
        currentView.set('list');
        currentService.set(null);
      }
    };
    window.addEventListener('popstate', onPopState);

    return () => {
      window.removeEventListener('popstate', onPopState);
      stopAutoReload();
      if (discoveryTimer) clearInterval(discoveryTimer);
    };
  });
</script>

<Navbar {spinning} {autoReloadEnabled} onRefresh={doRefresh} onToggleAutoReload={toggleAutoReload} />

<div class="container">
  {#if $currentView === 'detail' && $currentService}
    <ServiceDetail name={$currentService} />
  {:else}
    <ServiceList />
  {/if}
</div>
