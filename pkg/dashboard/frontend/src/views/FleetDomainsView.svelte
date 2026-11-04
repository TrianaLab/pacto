<script>
  import { onDestroy, untrack } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader, productListState } from '../lib/productLoader.svelte.ts';
  import { fleetOverviewUrl, fleetDomainsUrl, fleetServicesUrl } from '../lib/router.ts';
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

  // The business-domain list: /api/fleet/entities?kinds=domain, with search, order and
  // paging in the URL. The services that declare no domain are a separate question
  // (businessDomain=(none)), counted with a second, one-row request and shown as the
  // No domain row under the list, in a neutral tone.
  let { text = '', offset = '', limit = '', sort = '', refreshTick = 0 } = $props();

  const PAGE_SIZE = 25;
  const pageOffset = $derived(urlInt(offset));
  const pageLimit = $derived(urlInt(limit) || PAGE_SIZE);
  const order = $derived(sort || 'name');
  const anyFilter = $derived(!!text);

  let textDraft = $state(untrack(() => text));
  let submittedText = null;
  $effect(() => { if (text !== submittedText) textDraft = text; submittedText = null; });

  const loader = createProductLoader(() => api.fleetEntities({ kinds: ['domain'], text: text || undefined, offset: pageOffset || undefined, limit: pageLimit, sort: order }));
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
  // Genuinely empty with complete knowledge: say how to declare a domain. An empty list
  // under incomplete knowledge keeps the shared "none known" state instead.
  const noneDeclared = $derived(state.kind.startsWith('empty') && !knowledge.incomplete);

  // The No domain count. A failure here costs only this row; the list stays.
  const noneLoader = createProductLoader(() => api.fleetEntities({ kinds: ['service'], businessDomain: '(none)', limit: 1 }));
  $effect(() => { noneLoader.sync(`none@@${refreshTick}`, 'none'); });
  onDestroy(() => noneLoader.destroy());
  const noDomain = $derived(noneLoader.data?.total ?? 0);

  const pageHref = (off, lim) => fleetDomainsUrl({ text: text || undefined, offset: off, limit: limitParam(lim, limit, PAGE_SIZE), sort: sort || undefined });
  function submitSearch(e) { e.preventDefault(); submittedText = textDraft; location.hash = fleetDomainsUrl({ text: textDraft, sort: sort || undefined }); }
  function clearAll() { location.hash = fleetDomainsUrl(); }
  const chips = $derived(text ? [{ key: 'text', label: 'Search', value: text }] : []);
  const plural = (n, one, many) => `${formatCount(n)} ${n === 1 ? one : many}`;
</script>

<div class="product-page">
  <Breadcrumbs trail={[{ label: 'Overview', href: fleetOverviewUrl() }, { label: 'Domains' }]} />
  <PageHeader
    title="Domains"
    count={list ? plural(total, 'domain', 'domains') : ''}
    subtitle="The business domain each service declares in its contract, as service.domain."
    asOf={list?.meta?.asOf}
    {revalidating} paging={view.paging}
  />

  <div class="dv-filters">
    <form class="lv-search" onsubmit={submitSearch} role="search">
      <input class="input" type="search" bind:value={textDraft} placeholder="Search domains..." aria-label="Search domains" />
      <button type="submit" class="btn">Search</button>
    </form>
    <ListSort value={order} options={[['name', 'Name'], ['services', 'Most services first']]} href={(v) => fleetDomainsUrl({ text: text || undefined, sort: v === 'name' ? undefined : v })} testid="domain-sort" />
  </div>
  <ActiveFilterChips {chips} onRemove={clearAll} onClear={clearAll} />

  {#if knowledge.incomplete && (state.kind === 'ready' || state.kind === 'filtered-empty')}
    <KnowledgeBanner {knowledge} noun="list" />
  {/if}

  {#if refreshError}
    <StaleRefreshNotice noun="domain list" onRetry={load} />
  {/if}

  {#if pastEnd(list, count)}
    <Pager page={list} href={pageHref} count={0} noun="domains" testid="domain" />
  {:else if noneDeclared}
    <div class="dv-empty" role="status">
      <h2 class="t-section-title">No service declares a domain yet.</h2>
      <p class="t-body-2">Set service.domain in a contract (pactoVersion 2.4) to group services by domain.</p>
    </div>
  {:else if state.kind !== 'ready'}
    <ProductEmptyState {state} noun="domains" onRetry={load} onClearFilters={anyFilter ? clearAll : null} {skeletonRows} />
  {:else}
    <ul class="lv-list pl-list pl-list--facts pl-list--domains" data-testid="domain-list">
      {#each list.entities as ref (ref.key)}
        <li class="lv-item pl-row" animate:flip={{ duration: dur(MOTION.row) }}><EntityLink {ref} showStatus={false} showKind={false}><span class="pl-num">{plural(ref.services ?? 0, 'service', 'services')} · {plural(ref.targets ?? 0, 'operational target', 'operational targets')}</span></EntityLink></li>
      {/each}
    </ul>
    <Pager page={list} href={pageHref} label="Domain pages" noun="domains" testid="domain" />
  {/if}

  {#if noDomain > 0}
    <p class="dv-none" data-testid="domain-none">
      <a href={fleetServicesUrl({ businessDomain: '(none)' })}>No domain: {plural(noDomain, 'service', 'services')}</a>
      <span class="dv-none-note">Services with no declared domain.</span>
    </p>
  {/if}
</div>

<style>
  .dv-filters { display: flex; gap: var(--sp-3); flex-wrap: wrap; align-items: flex-end; }
  .lv-search { display: flex; gap: var(--sp-2); max-width: 420px; flex: 1; min-width: 220px; }
  .lv-search .input { flex: 1; }
  .dv-empty { padding: var(--sp-6) var(--sp-4); text-align: center; color: var(--c-text-2); }
  .dv-empty h2, .dv-empty p { margin: 0 0 var(--sp-2); }
  /* No domain is a neutral fact, never a healthy one. */
  .dv-none {
    display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--sp-1) var(--sp-3); margin: 0;
    padding: var(--sp-2) var(--sp-3); border: 1px dashed var(--c-border); border-radius: var(--radius-sm);
    background: var(--c-surface-inset); color: var(--c-text-2);
  }
  .dv-none a { color: var(--c-text); font-weight: 600; text-decoration: underline; }
  .dv-none-note { font-size: var(--text-sm); color: var(--c-text-3); }
</style>
