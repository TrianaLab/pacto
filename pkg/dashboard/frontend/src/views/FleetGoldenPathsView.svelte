<script>
  import { onDestroy, untrack } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader, productListState } from '../lib/productLoader.svelte.ts';
  import { fleetOverviewUrl, fleetGoldenPathsUrl, fleetServicesUrl } from '../lib/router.ts';
  import { SIGNATURE_STATES } from '../lib/distributions.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import KnowledgeBanner from '../components/KnowledgeBanner.svelte';
  import EntityLink from '../components/EntityLink.svelte';
  import EntityStatusBadge from '../components/EntityStatusBadge.svelte';
  import IdentityBadge from '../components/IdentityBadge.svelte';
  import ProductEmptyState from '../components/ProductEmptyState.svelte';
  import StaleRefreshNotice from '../components/StaleRefreshNotice.svelte';
  import ActiveFilterChips from '../components/ActiveFilterChips.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import Pager from '../components/Pager.svelte';
  import ListSort from '../components/ListSort.svelte';
  import { urlInt, limitParam, pastEnd } from '../lib/paging.ts';
  import { formatCount } from '../lib/format.ts';

  // The Golden paths list: /api/fleet/entities?kinds=service&template=only, with search,
  // order and paging in the URL. A golden path is a service whose contract declares
  // service.template and that runs nowhere; each row counts the services created from it.
  let { text = '', offset = '', limit = '', sort = '', refreshTick = 0 } = $props();

  const PAGE_SIZE = 25;
  const GUIDE = 'https://pacto.run/latest/golden-paths/';
  // The fleet checks a path's signature only against the signer its own contract
  // declares, so a verified row never says who may publish golden paths.
  const SIGNATURE_NOTE = 'Verified against the signer its own contract declares. Pacto did not check it against a trust config.';
  const pageOffset = $derived(urlInt(offset));
  const pageLimit = $derived(urlInt(limit) || PAGE_SIZE);
  const order = $derived(sort || 'name');
  const anyFilter = $derived(!!text);

  let textDraft = $state(untrack(() => text));
  let submittedText = null;
  $effect(() => { if (text !== submittedText) textDraft = text; submittedText = null; });

  const loader = createProductLoader(() => api.fleetEntities({ kinds: ['service'], template: 'only', text: text || undefined, offset: pageOffset || undefined, limit: pageLimit, sort: order }));
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
  // Empty with complete knowledge: say what a golden path is. With incomplete knowledge
  // "none yet" would be a guess.
  const noneDeclared = $derived(state.kind === 'empty-fleet');

  const pageHref = (off, lim) => fleetGoldenPathsUrl({ text: text || undefined, offset: off, limit: limitParam(lim, limit, PAGE_SIZE), sort: sort || undefined });
  function submitSearch(e) { e.preventDefault(); submittedText = textDraft; location.hash = fleetGoldenPathsUrl({ text: textDraft, sort: sort || undefined }); }
  function clearAll() { location.hash = fleetGoldenPathsUrl(); }
  const chips = $derived(text ? [{ key: 'text', label: 'Search', value: text }] : []);
  const sig = (s) => SIGNATURE_STATES.find((x) => x.value === (s || 'unknown')) ?? SIGNATURE_STATES[3];
</script>

<div class="product-page">
  <Breadcrumbs trail={[{ label: 'Overview', href: fleetOverviewUrl() }, { label: 'Golden paths' }]} />
  <PageHeader
    title="Golden paths"
    count={list ? `${formatCount(total)} golden ${total === 1 ? 'path' : 'paths'}` : ''}
    subtitle="Contracts that platform teams publish for other teams to create services from. Each declares service.template and runs nowhere."
    asOf={list?.meta?.asOf}
    {revalidating} paging={view.paging}
  />

  <div class="gp-filters">
    <form class="gp-search" onsubmit={submitSearch} role="search">
      <input class="input" type="search" bind:value={textDraft} placeholder="Search golden paths..." aria-label="Search golden paths" />
      <button type="submit" class="btn">Search</button>
    </form>
    <ListSort value={order} options={[['name', 'Name'], ['health', 'Least healthy first']]} href={(v) => fleetGoldenPathsUrl({ text: text || undefined, sort: v === 'name' ? undefined : v })} testid="golden-path-sort" />
  </div>
  <ActiveFilterChips {chips} onRemove={clearAll} onClear={clearAll} />

  {#if knowledge.incomplete && (state.kind === 'ready' || state.kind === 'filtered-empty')}
    <KnowledgeBanner {knowledge} noun="list" />
  {/if}

  {#if refreshError}
    <StaleRefreshNotice noun="golden path list" onRetry={load} />
  {/if}

  {#if pastEnd(list, count)}
    <Pager page={list} href={pageHref} count={0} noun="golden paths" testid="golden-path" />
  {:else if noneDeclared}
    <div class="gp-empty" role="status" data-testid="golden-paths-empty">
      <h2 class="t-section-title">No golden paths yet: a golden path is a contract that declares service.template</h2>
      <p class="t-body-2">Publish one to a registry Pacto reads (pactoVersion 2.4) to list it here. <a href={GUIDE} target="_blank" rel="noopener noreferrer">Create services from golden paths</a></p>
    </div>
  {:else if state.kind !== 'ready'}
    <ProductEmptyState {state} noun="golden paths" onRetry={load} onClearFilters={anyFilter ? clearAll : null} {skeletonRows} />
  {:else}
    <div class="data-scroll">
      <table class="data-table" data-testid="golden-path-list">
        <caption class="visually-hidden">Golden paths</caption>
        <colgroup><col style="width: 20%" /><col style="width: 28%" /><col /><col /><col /><col /><col style="width: 9%" /></colgroup>
        <thead><tr><th scope="col">Name</th><th scope="col">Summary</th><th scope="col">Owner</th><th scope="col">Status</th><th scope="col">Signature</th><th scope="col">Source</th><th scope="col">Services created</th></tr></thead>
        <tbody>
          {#each list.entities as ref (ref.key)}
            {@const s = sig(ref.signature)}
            {@const source = ref.secondary || ref.domain || ''}
            <tr>
              <th scope="row"><span class="cell-clip" title={ref.key}><EntityLink {ref} showStatus={false} showKind={false} /></span></th>
              <td data-label="Summary">{ref.summary || ''}</td>
              <td data-label="Owner"><span class="cell-clip" title={ref.owner || undefined}>{ref.owner || 'No owner declared'}</span></td>
              <td data-label="Status"><EntityStatusBadge kind="service" status={ref.status} /></td>
              <td data-label="Signature"><span data-testid="path-signature" title={s.value === 'verified' ? SIGNATURE_NOTE : ''}><IdentityBadge label={s.label} tone={s.value === 'verified' ? 'neutral' : s.tone} /></span>{#if s.value === 'verified'}<span class="visually-hidden">{SIGNATURE_NOTE}</span>{/if}</td>
              <td data-label="Source"><span class="cell-clip" title={source}>{source}</span></td>
              <td data-label="Services created"><a data-testid="path-created" href={fleetServicesUrl({ createdFrom: ref.key })}>{formatCount(ref.created ?? 0)}</a></td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    <Pager page={list} href={pageHref} label="Golden path pages" noun="golden paths" testid="golden-path" />
  {/if}
</div>

<style>
  .gp-filters { display: flex; gap: var(--sp-3); flex-wrap: wrap; align-items: flex-end; }
  .gp-search { display: flex; gap: var(--sp-2); max-width: 420px; flex: 1; min-width: 220px; }
  .gp-search .input { flex: 1; }
  .gp-empty { padding: var(--sp-6) var(--sp-4); text-align: center; color: var(--c-text-2); }
  .gp-empty h2, .gp-empty p { margin: 0 0 var(--sp-2); }
  .gp-empty a { color: var(--c-accent); }
  .data-table td a { color: var(--c-accent); }
</style>
