<script>
  import { onDestroy, untrack } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader, productListState } from '../lib/productLoader.svelte.ts';
  import { fleetOverviewUrl, fleetOfferingsUrl } from '../lib/router.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import KnowledgeBanner from '../components/KnowledgeBanner.svelte';
  import EntityLink from '../components/EntityLink.svelte';
  import ProductEmptyState from '../components/ProductEmptyState.svelte';
  import StaleRefreshNotice from '../components/StaleRefreshNotice.svelte';
  import ActiveFilterChips from '../components/ActiveFilterChips.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import Pager from '../components/Pager.svelte';
  import ListSort from '../components/ListSort.svelte';
  import { urlInt, limitParam, pastEnd } from '../lib/paging.ts';
  import { formatCount } from '../lib/format.ts';
  import { flip } from 'svelte/animate';
  import { MOTION, dur } from '../lib/motion.ts';

  // The platform-offering list: /api/fleet/entities?kinds=offering, with search, order
  // and paging in the URL. Each row is one service.offering and counts its providers.
  let { text = '', offset = '', limit = '', sort = '', refreshTick = 0 } = $props();

  const PAGE_SIZE = 25;
  const pageOffset = $derived(urlInt(offset));
  const pageLimit = $derived(urlInt(limit) || PAGE_SIZE);
  const order = $derived(sort || 'name');
  const anyFilter = $derived(!!text);

  let textDraft = $state(untrack(() => text));
  let submittedText = null;
  $effect(() => { if (text !== submittedText) textDraft = text; submittedText = null; });

  const loader = createProductLoader(() => api.fleetEntities({ kinds: ['offering'], text: text || undefined, offset: pageOffset || undefined, limit: pageLimit, sort: order }));
  const queryIdentity = $derived(`${text}@@${pageOffset}@@${pageLimit}@@${order}`);
  const listIdentity = $derived(`${text}@@${order}`);
  $effect(() => { loader.sync(`${queryIdentity}@@${refreshTick}`, queryIdentity, listIdentity); });
  onDestroy(() => loader.destroy());
  function load() { loader.refresh(); }

  const view = productListState(loader, (d) => d.entities, () => ({ tag: queryIdentity, family: listIdentity, filtered: anyFilter, pageLimit }));
  const list = $derived(view.data);
  const knowledge = $derived(view.knowledge);
  const count = $derived(view.count);
  const state = $derived(view.state);
  const skeletonRows = $derived(view.skeletonRows);
  const refreshError = $derived(view.refreshError);
  const revalidating = $derived(view.revalidating);
  const total = $derived(list?.total ?? 0);
  // Genuinely empty with complete knowledge (empty-fleet): say how to declare an
  // offering. With incomplete knowledge "none declared" would be a guess.
  const noneDeclared = $derived(state.kind === 'empty-fleet');

  const pageHref = (off, lim) => fleetOfferingsUrl({ text: text || undefined, offset: off, limit: limitParam(lim, limit, PAGE_SIZE), sort: sort || undefined });
  function submitSearch(e) { e.preventDefault(); submittedText = textDraft; location.hash = fleetOfferingsUrl({ text: textDraft, sort: sort || undefined }); }
  function clearAll() { location.hash = fleetOfferingsUrl(); }
  const chips = $derived(text ? [{ key: 'text', label: 'Search', value: text }] : []);
  const plural = (n, one, many) => `${formatCount(n)} ${n === 1 ? one : many}`;
</script>

<div class="product-page">
  <Breadcrumbs trail={[{ label: 'Overview', href: fleetOverviewUrl() }, { label: 'Offerings' }]} />
  <PageHeader
    title="Platform offerings"
    count={list ? plural(total, 'offering', 'offerings') : ''}
    subtitle="Services that platform teams publish for other teams to use, grouped by the service.offering their contracts declare."
    asOf={list?.meta?.asOf}
    {revalidating} paging={view.paging}
  />

  <div class="ov-filters">
    <form class="lv-search" onsubmit={submitSearch} role="search">
      <input class="input" type="search" bind:value={textDraft} placeholder="Search offerings..." aria-label="Search offerings" />
      <button type="submit" class="btn">Search</button>
    </form>
    <ListSort value={order} options={[['name', 'Name'], ['services', 'Most providers first']]} href={(v) => fleetOfferingsUrl({ text: text || undefined, sort: v === 'name' ? undefined : v })} testid="offering-sort" />
  </div>
  <ActiveFilterChips {chips} onRemove={clearAll} onClear={clearAll} />

  {#if knowledge.incomplete && (state.kind === 'ready' || state.kind === 'filtered-empty')}
    <KnowledgeBanner {knowledge} noun="list" />
  {/if}

  {#if refreshError}
    <StaleRefreshNotice noun="offering list" onRetry={load} />
  {/if}

  {#if pastEnd(list, count)}
    <Pager page={list} href={pageHref} count={0} noun="offerings" testid="offering" />
  {:else if noneDeclared}
    <div class="ov-empty" role="status">
      <h2 class="t-section-title">No contract declares a platform offering yet.</h2>
      <p class="t-body-2">Set service.offering in a contract (pactoVersion 2.5) to list it here.</p>
    </div>
  {:else if state.kind !== 'ready'}
    <ProductEmptyState {state} noun="offerings" onRetry={load} onClearFilters={anyFilter ? clearAll : null} {skeletonRows} />
  {:else}
    <!-- ponytail: an offering row has the domain row's shape, so it reuses its layout. -->
    <ul class="lv-list pl-list pl-list--facts pl-list--domains" data-testid="offering-list">
      {#each list.entities as ref (ref.key)}
        <li class="lv-item pl-row" animate:flip={{ duration: dur(MOTION.row) }}><EntityLink {ref} showStatus={false} showKind={false}><span class="pl-num">{plural(ref.services ?? 0, 'provider', 'providers')} · {plural(ref.targets ?? 0, 'operational target', 'operational targets')}</span></EntityLink></li>
      {/each}
    </ul>
    <Pager page={list} href={pageHref} label="Offering pages" noun="offerings" testid="offering" />
  {/if}
</div>

<style>
  .ov-filters { display: flex; gap: var(--sp-3); flex-wrap: wrap; align-items: flex-end; }
  .lv-search { display: flex; gap: var(--sp-2); max-width: 420px; flex: 1; min-width: 220px; }
  .lv-search .input { flex: 1; }
  .ov-empty { padding: var(--sp-6) var(--sp-4); text-align: center; color: var(--c-text-2); }
  .ov-empty h2, .ov-empty p { margin: 0 0 var(--sp-2); }
</style>
