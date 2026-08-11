<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { decideViewState, snapshotKnowledge } from '../lib/knowledgeState.ts';
  import { statusLabel, STATUS_FILTER_OPTIONS } from '../lib/format.ts';
  import { fleetOverviewUrl, fleetServicesUrl, fleetOwnersUrl, hashForHref, fleetEntityUrl } from '../lib/router.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import EntityCombobox from '../components/EntityCombobox.svelte';
  import KnowledgeBanner from '../components/KnowledgeBanner.svelte';
  import EntityLink from '../components/EntityLink.svelte';
  import ProductEmptyState from '../components/ProductEmptyState.svelte';
  import StaleRefreshNotice from '../components/StaleRefreshNotice.svelte';
  import ActiveFilterChips from '../components/ActiveFilterChips.svelte';
  import DistributionBar from '../components/viz/DistributionBar.svelte';
  import HorizontalBars from '../components/viz/HorizontalBars.svelte';
  import { complianceSegments, ownershipSegments, statusHrefs, bucketLabel, OWNERSHIP_STATES } from '../lib/distributions.ts';
  import PageHeader from '../components/PageHeader.svelte';

  // The product Services list (requirement C / A3). It is the canonical destination of
  // the backend EntryPointServices href (/fleet/services) and the primary Navbar
  // Services link on fleet-capable hosts. It consumes /api/fleet/entities?kinds=service
  // through the generated SDK facade -- NEVER the legacy preloaded /api/services list
  // and never a FleetSnapshot reconstruction. Filters and the page offset live in the
  // URL, so the filtered/paged list is deep-linkable and back/forward-restorable.
  let { text = '', owner = '', ownership = '', status = '', domain = '', offset = '', refreshTick = 0 } = $props();

  const PAGE_SIZE = 25;
  const pageOffset = $derived(Math.max(0, Math.trunc(Number(offset) || 0)));
  const anyFilter = $derived(!!(text || owner || ownership || status || domain));

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
    ownership: ownership || undefined,
    status: status || undefined,
    domain: domain || undefined,
    offset: pageOffset || undefined,
    limit: PAGE_SIZE,
  }));
  // queryIdentity is the QUESTION -- every filter and the page, and deliberately not
  // refreshTick, which only asks the same question again. requestKey adds the tick so a
  // poll still fires. Retaining rows across a refresh is stale-while-revalidate;
  // retaining them across a filter or page change would be showing one query's answer
  // under another query's heading, so `list` is gated on the tag matching.
  const queryIdentity = $derived(`${text}@@${owner}@@${ownership}@@${status}@@${domain}@@${pageOffset}`);
  $effect(() => { loader.sync(`${queryIdentity}@@${refreshTick}`, queryIdentity); });
  onDestroy(() => loader.destroy());
  function load() { loader.refresh(); }

  const list = $derived(loader.dataTag === queryIdentity ? loader.data : null);
  const loading = $derived(loader.loading);
  const error = $derived(loader.error);
  const knowledge = $derived(snapshotKnowledge(list?.meta));
  const count = $derived(list?.entities?.length ?? 0);
  const state = $derived(decideViewState({ loading, error, itemCount: count, filtered: anyFilter, knowledge }));
  // A poll that failed over rows we can still show. decideViewState keeps the rows;
  // this is the half that keeps it honest, so a frozen list never reads as a live one.
  const refreshError = $derived(state.kind === 'ready' ? state.refreshError : null);

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
      ownership: patch.ownership ?? ownership,
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
    ownership ? { key: 'ownership', label: 'Ownership', value: bucketLabel(OWNERSHIP_STATES, ownership) } : null,
    status ? { key: 'status', label: 'Status', value: statusLabel(status) } : null,
    domain ? { key: 'domain', label: 'Domain', value: domain } : null,
  ].filter(Boolean));

  // The inventory. `aggregate` is computed by the backend over every service matching
  // these filters, with paging deliberately excluded, so it answers "what does this
  // filter select" rather than "what happens to be on screen".
  //
  // This page used to tally the 25 rendered rows instead. That bar was honest about its
  // scope in a caption and still wrong to draw: page 1 of a 400-service fleet is a
  // sample nobody chose, it changed shape as you paged, and it could not answer the
  // question the reader was actually asking. It is gone rather than demoted -- keeping
  // both would put two compliance bars with different denominators on one screen.
  const agg = $derived(list?.aggregate ?? null);
  const matched = $derived(agg?.matched ?? 0);
  const scopeNote = $derived(anyFilter
    ? `All ${matched} matching ${matched === 1 ? 'service' : 'services'}, not just this page.`
    : `All ${matched} ${matched === 1 ? 'service' : 'services'} in the snapshot.`);
  const complianceOfMatch = $derived(complianceSegments(agg?.serviceCompliance,
    statusHrefs((s) => urlWith({ status: s }))));
  const ownershipOfMatch = $derived(ownershipSegments(agg?.ownership, Object.fromEntries(
    OWNERSHIP_STATES.map((s) => [s.field, urlWith({ ownership: s.value })]),
  )));
  // A ranking, NOT a partition: it is the backend's top owners by service count, so the
  // rows do not add up to the matched population and are never drawn as if they did.
  // Each row narrows THIS query by that owner rather than jumping to the owner page, so
  // the filters the reader already set survive the click.
  //
  // It counts only CONSISTENTLY owned services, so the drill-down carries ownership as
  // well as the owner. `owner=x` alone means "some revision names x", which also selects
  // the services x co-owns with somebody else -- a destination bigger than the bar that
  // sent the reader there.
  const rankHref = (o) => urlWith({ owner: o.owner, ownership: 'consistent' });
  const ranked = $derived((agg?.byOwner ?? []).map((o) => ({
    label: o.owner, value: o.services, tone: 'info', href: rankHref(o),
  })));
  const rankedTargets = $derived((agg?.byOwner ?? []).map((o) => ({
    label: o.owner, value: o.targets, tone: 'info', href: rankHref(o),
  })));
  const distinctOwners = $derived(agg?.distinctOwners ?? 0);
  const rankNote = $derived.by(() => {
    const other = agg?.otherOwners ?? 0;
    const distinct = agg?.distinctOwners ?? 0;
    const shown = ranked.length;
    if (!distinct) return '';
    const tail = other > 0
      ? ` The remaining ${distinct - shown} of ${distinct} ${distinct - shown === 1 ? 'owner accounts' : 'owners account'} for ${other} more ${other === 1 ? 'service' : 'services'}.`
      : '';
    // Services with no declared owner, and services whose revisions disagree, are in
    // none of these rows -- they have no single owner to rank under. Saying so is the
    // difference between a ranking and a partition that quietly loses rows.
    return `Top ${shown} of ${distinct} declared ${distinct === 1 ? 'owner' : 'owners'} by service count.${tail} Services with no owner, or whose revisions name different owners, appear in no row here.`;
  });

  function removeChip(key) { apply({ [key]: '' }); }
</script>

<div class="product-page">
  <Breadcrumbs trail={[{ label: 'Overview', href: fleetOverviewUrl() }, { label: 'Services' }]} />
  <PageHeader title="Services" count={list ? `${total} service${total === 1 ? '' : 's'}` : ''} />

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
    <!-- Who owns it (a name, above) and whether ownership is declared at all (a state)
         are different questions, so they are different controls and they compose. -->
    <label class="sv-field">
      <span>Ownership</span>
      <select value={ownership} aria-label="Filter by declared ownership" onchange={(e) => apply({ ownership: e.currentTarget.value })}>
        <option value="">Any ownership</option>
        {#each OWNERSHIP_STATES as o}<option value={o.value}>{o.label}</option>{/each}
      </select>
    </label>
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

  {#if refreshError}
    <StaleRefreshNotice noun="services list" onRetry={load} />
  {/if}

  {#if state.kind === 'ready' && matched > 0}
    <section class="sv-inventory" aria-labelledby="sv-inv-h">
      <div class="sv-inv-head">
        <h2 id="sv-inv-h" class="t-section-title">{anyFilter ? 'What these filters select' : 'The whole service inventory'}</h2>
        <a class="sv-viewall" href={fleetOwnersUrl()}>Browse owners</a>
      </div>
      <div class="sv-inv-grid">
        <DistributionBar
          title="Compliance"
          description="Whether each service is observed to obey its contract, rolled up from the targets running it."
          {scopeNote}
          segments={complianceOfMatch}
          total={matched}
        />
        <DistributionBar
          title="Declared ownership"
          description="Ownership is authored on each contract revision, so a service is cleanly owned only when its revisions agree."
          {scopeNote}
          segments={ownershipOfMatch}
          total={matched}
        />
      </div>
      <!-- The two per-owner rankings are one disclosure away, and the two distributions
           above are not. Both answer questions this page owes the reader, but only the
           distributions answer them about the population as a whole -- the rankings ask
           a follow-up ("who carries it"), and they cost ten touch-sized rows each. Open,
           the four figures pushed the service list to 1180px on a desktop and 2346px on
           a phone: two and a half screens of analysis in front of the list a reader came
           here to search. Nothing is deleted, the count is on the summary, and the state
           is native. -->
      <details class="disclosure sv-inv-more">
        <summary>
          <span class="disclosure-caret" aria-hidden="true">&#9656;</span>
          <span>Per-owner breakdown</span>
          <span class="sv-more-count t-meta">{distinctOwners} declared {distinctOwners === 1 ? 'owner' : 'owners'}</span>
        </summary>
        <div class="sv-inv-grid">
          <HorizontalBars
            title="Services per owner"
            description="Who carries the most of this selection."
            scopeNote={rankNote}
            items={ranked}
            unit="services"
            unitOne="service"
            emptyLabel="Nothing here has a single declared owner to rank by."
          />
          <HorizontalBars
            title="Operational targets per owner"
            description="How much is actually running behind each of those owners."
            scopeNote="Same owners, in the same service-count order as above — this is not a ranking by target count."
            items={rankedTargets}
            unit="targets"
            unitOne="target"
            emptyLabel="None of these owners has anything running yet."
          />
        </div>
      </details>
    </section>
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
  .sv-inventory { display: flex; flex-direction: column; gap: var(--sp-3); padding-top: var(--sp-3); border-top: 1px solid var(--c-border); }
  .sv-inv-head { display: flex; align-items: baseline; justify-content: space-between; gap: var(--sp-3); flex-wrap: wrap; }
  .sv-inv-head h2 { margin: 0; }
  .sv-viewall { color: var(--c-accent); text-decoration: none; font-size: var(--text-sm); }
  .sv-viewall:hover { text-decoration: underline; }
  /* Same track rule as PostureBars: two charts side by side where there is room, one on
     a phone. Charts across the product line up instead of each picking a breakpoint. */
  /* TWO columns where there is room, never three. The figures come in pairs -- the two
     distributions, then the two per-owner rankings -- and at three columns the pairs
     split across rows: a ten-row chart set the height of a row holding two four-row
     bars, and the block opened with a screenful of empty space beside them. A grid per
     pair puts each on its own row, where both members are the same height. */
  .sv-inv-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 380px), 1fr)); gap: var(--sp-4); align-items: start; }
  .sv-more-count { color: var(--c-text-3); }
  .sv-inv-more[open] > .sv-inv-grid { margin-top: var(--sp-3); }
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
