<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { decideViewState, snapshotKnowledge } from '../lib/knowledgeState.ts';
  import { statusLabel, STATUS_FILTER_OPTIONS } from '../lib/format.ts';
  import { fleetOverviewUrl, fleetServicesUrl, hashForHref, fleetEntityUrl } from '../lib/router.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import EntityCombobox from '../components/EntityCombobox.svelte';
  import KnowledgeBanner from '../components/KnowledgeBanner.svelte';
  import EntityLink from '../components/EntityLink.svelte';
  import ProductEmptyState from '../components/ProductEmptyState.svelte';
  import ActiveFilterChips from '../components/ActiveFilterChips.svelte';
  import DistributionBar from '../components/viz/DistributionBar.svelte';
  import { complianceSegments, tallyStatuses } from '../lib/distributions.ts';

  // The product Services list (requirement C / A3). It is the canonical destination of
  // the backend EntryPointServices href (/fleet/services) and the primary Navbar
  // Services link on fleet-capable hosts. It consumes /api/fleet/entities?kinds=service
  // through the generated SDK facade -- NEVER the legacy preloaded /api/services list
  // and never a FleetSnapshot reconstruction. Filters and the page offset live in the
  // URL, so the filtered/paged list is deep-linkable and back/forward-restorable.
  let { text = '', owner = '', status = '', domain = '', offset = '', refreshTick = 0 } = $props();

  const PAGE_SIZE = 25;
  const pageOffset = $derived(Math.max(0, Math.trunc(Number(offset) || 0)));
  const anyFilter = $derived(!!(text || owner || status || domain));

  // The search box is a local draft synced from the URL; it commits on submit so
  // typing does not spam browser history. It re-syncs when the URL text changes
  // (external navigation / back-forward), which never clobbers mid-typing because the
  // prop only changes on a committed navigation.
  let textDraft = $state(text);
  $effect(() => { textDraft = text; });

  // One reusable, race-safe loader (requirement E): the fetcher reads the current
  // params at request time; sync(key) dedupes the initial load and the generation
  // guard prevents an older response overwriting a newer route/filter/refresh.
  const loader = createProductLoader(() => api.fleetEntities({
    kinds: ['service'],
    text: text || undefined,
    owner: owner || undefined,
    status: status || undefined,
    domain: domain || undefined,
    offset: pageOffset || undefined,
    limit: PAGE_SIZE,
  }));
  $effect(() => {
    loader.sync(`${text}@@${owner}@@${status}@@${domain}@@${pageOffset}@@${refreshTick}`);
  });
  onDestroy(() => loader.destroy());
  function load() { loader.refresh(); }

  const list = $derived(loader.data);
  const loading = $derived(loader.loading);
  const error = $derived(loader.error);
  const knowledge = $derived(snapshotKnowledge(list?.meta));
  const count = $derived(list?.entities?.length ?? 0);
  const state = $derived(decideViewState({ loading, error, itemCount: count, filtered: anyFilter, knowledge }));

  const total = $derived(list?.total ?? 0);
  const shownFrom = $derived(total === 0 ? 0 : (list?.offset ?? pageOffset) + 1);
  const shownTo = $derived((list?.offset ?? pageOffset) + count);
  const hasPrev = $derived((list?.offset ?? pageOffset) > 0);
  const hasNext = $derived(list?.nextOffset != null);
  const prevOffset = $derived(Math.max(0, (list?.offset ?? pageOffset) - PAGE_SIZE));

  // Every filter change resets the offset to page 1; a patch value of '' clears that
  // filter (`?? current` only falls back on null/undefined, never on the empty string).
  function urlWith(patch, off = 0) {
    return fleetServicesUrl({
      text: patch.text ?? text,
      owner: patch.owner ?? owner,
      status: patch.status ?? status,
      domain: patch.domain ?? domain,
      offset: off,
    });
  }
  function apply(patch) { location.hash = urlWith(patch); }
  function submitSearch(e) { e.preventDefault(); apply({ text: textDraft }); }
  function clearAll() { location.hash = fleetServicesUrl(); }

  // A picked suggestion goes to that exact service, by the canonical href the backend
  // put on the reference (falling back to its canonical key) -- never to a filter
  // rebuilt from the visible name, which two domains can share.
  function openSuggestion(ref) {
    location.hash = ref.href ? hashForHref(ref.href) : fleetEntityUrl(ref.kind, ref.key);
  }
  // An owner suggestion is a filter, not a destination: it commits the owner the
  // backend actually knows, spelled exactly as the snapshot spells it.
  function pickOwner(ref) { apply({ owner: ref.label ?? '' }); }

  const chips = $derived([
    text ? { key: 'text', label: 'Search', value: text } : null,
    owner ? { key: 'owner', label: 'Owner', value: owner } : null,
    status ? { key: 'status', label: 'Status', value: statusLabel(status) } : null,
    domain ? { key: 'domain', label: 'Domain', value: domain } : null,
  ].filter(Boolean));
  // Counted from the rendered page, never presented as the whole population:
  // DistributionBar is given the page size as its denominator and the scope note states
  // it in words. The bucketing itself is the SHARED one, so this page and the backend's
  // own tallies can never split the same status two different ways.
  const pageSegments = $derived(complianceSegments(tallyStatuses((list?.entities ?? []).map((r) => r.status))));

  function removeChip(key) { apply({ [key]: '' }); }
</script>

<div class="svc-view">
  <Breadcrumbs trail={[{ label: 'Overview', href: fleetOverviewUrl() }, { label: 'Services' }]} />
  <div class="sv-head">
    <h1>Services</h1>
    {#if list}<span class="sv-total">{total} service{total === 1 ? '' : 's'}</span>{/if}
  </div>

  <div class="sv-filters">
    <!-- Suggestions come from the backend Entities query, restricted to services, so
         what is offered is what exists rather than what happens to be on this page.
         Nothing commits until the user picks a suggestion, presses Enter or submits:
         typing must not write a history entry per keystroke. -->
    <form class="sv-search" onsubmit={submitSearch} role="search">
      <EntityCombobox
        id="svc-search"
        bind:value={textDraft}
        kinds={['service']}
        placeholder="Search services..."
        label="Search services by name, key or domain"
        testid="svc-search"
        onselect={openSuggestion}
      />
      <button type="submit" class="btn">Search</button>
    </form>
    <!-- Not a wrapping <label>: a click inside one is forwarded to its control, which
         would swallow the click on a suggestion. The association is by `for` instead. -->
    <div class="sv-field">
      <label for="svc-owner">Owner</label>
      <!-- Owner is a real backend entity kind derived from the whole snapshot, so its
           suggestions are the complete owner population. Domain below stays free text:
           there is no authoritative domain facet to draw from, and suggesting domains
           off the 25 rows on screen would quietly present a page as the fleet. -->
      <EntityCombobox
        id="svc-owner"
        value={owner}
        kinds={['owner']}
        placeholder="team or DRI"
        label="Filter by owner"
        testid="svc-owner"
        onselect={pickOwner}
        oncommit={(v) => { if (v !== owner) apply({ owner: v }); }}
      />
    </div>
    <label class="sv-field">
      <span>Status</span>
      <select value={status} aria-label="Filter by compliance status" onchange={(e) => apply({ status: e.currentTarget.value })}>
        <option value="">Any status</option>
        <!-- The wire enum is the option VALUE; the option TEXT is the word the badges in
             the list below use. A picker offering "NonCompliant" above rows badged "Not
             compliant" asks the user to believe those are two different states. -->
        {#each STATUS_FILTER_OPTIONS as s}<option value={s}>{statusLabel(s)}</option>{/each}
      </select>
    </label>
    <label class="sv-field">
      <span>Domain</span>
      <input class="input" type="text" value={domain} placeholder="exact domain" aria-label="Filter by domain"
        onchange={(e) => apply({ domain: e.currentTarget.value.trim() })} />
    </label>
  </div>

  <ActiveFilterChips {chips} onRemove={removeChip} onClear={clearAll} />

  {#if knowledge.incomplete && (state.kind === 'ready' || state.kind === 'filtered-empty')}
    <KnowledgeBanner {knowledge} noun="list" />
  {/if}

  <!-- PAGE-SCOPED, and it says so. The Entities endpoint answers "which services match"
       and pages the answer; it does not tally the whole match, so this bar counts the
       services actually on screen. The fleet-wide compliance distribution lives on the
       Overview, where the backend computes it over the complete population. -->
  {#if state.kind === 'ready' && pageSegments.some((x) => x.value > 0)}
    <DistributionBar
      title="Compliance on this page"
      level={2}
      scopeNote={`This page only — ${count} of ${total} matching services.`}
      segments={pageSegments}
      total={count}
    />
  {/if}

  {#if state.kind !== 'ready'}
    <ProductEmptyState {state} noun="services" onRetry={load} onClearFilters={anyFilter ? clearAll : null} />
  {:else}
    <ul class="sv-list" data-testid="service-list">
      {#each list.entities as ref (ref.kind + '::' + ref.key)}
        <li class="sv-item">
          <EntityLink {ref} showStatus={true} />
        </li>
      {/each}
    </ul>

    <nav class="sv-pager" aria-label="Service pages">
      <span class="sv-range">Showing {shownFrom}–{shownTo} of {total}</span>
      <div class="sv-pager-btns">
        {#if hasPrev}
          <a class="sv-page" href={urlWith({}, prevOffset)} data-testid="svc-prev" rel="prev">Previous</a>
        {:else}
          <span class="sv-page disabled" aria-disabled="true">Previous</span>
        {/if}
        {#if hasNext}
          <a class="sv-page" href={urlWith({}, list.nextOffset)} data-testid="svc-next" rel="next">Next</a>
        {:else}
          <span class="sv-page disabled" aria-disabled="true">Next</span>
        {/if}
      </div>
    </nav>
  {/if}
</div>

<style>
  .svc-view { display: flex; flex-direction: column; gap: var(--sp-4); }
  .sv-head { display: flex; align-items: baseline; gap: var(--sp-3); }
  .sv-head h1 { margin: 0; }
  .sv-total { color: var(--c-text-3); }
  .sv-filters { display: flex; gap: var(--sp-3); flex-wrap: wrap; align-items: flex-end; }
  .sv-search { display: flex; gap: var(--sp-2); flex: 1; min-width: 220px; }
  /* The suggestion popup is absolutely positioned inside its field, so a field that
     clips or stacks below its neighbour would hide it. */
  .sv-search, .sv-field { position: relative; }
  .sv-field { display: flex; flex-direction: column; gap: 2px; min-width: 180px; font-size: var(--text-xs); color: var(--c-text-3); }
  /* Text inputs use the shared .input from styles/components.css so the two
     comboboxes -- whose inputs live in a child component and are out of reach of this
     scoped block -- and the plain Domain field are one control, not two look-alikes. */
  /* The Search control is the shared .btn from styles/components.css. Each list view
     used to carry its own byte-identical copy, which is how one product ends up with
     four Search buttons in three flavours. */
  .sv-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .sv-item {
    display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap;
    padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    background: var(--c-surface);
  }
  .sv-pager {
    display: flex; align-items: center; justify-content: space-between; gap: var(--sp-3);
    flex-wrap: wrap; margin-top: var(--sp-2);
  }
  .sv-range { color: var(--c-text-3); font-size: var(--text-sm); }
  .sv-pager-btns { display: flex; gap: var(--sp-2); }
  .sv-page {
    padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    font-size: var(--text-sm); color: var(--c-text); text-decoration: none; background: var(--c-surface);
    min-height: var(--touch-min); display: inline-flex; align-items: center;
  }
  .sv-page:hover { border-color: var(--c-accent); text-decoration: none; }
  .sv-page.disabled { color: var(--c-text-3); opacity: 0.5; pointer-events: none; }
</style>
