<script>
  import { onMount, onDestroy, untrack, tick } from 'svelte';
  import { api, ApiError } from '../lib/api.ts';
  import { boundedMatches, NAME_LOOKUP_LIMIT } from '../lib/entityResolve.ts';
  import { shortDigest, formatCount, countOf } from '../lib/format.ts';
  import SearchPick from '../components/SearchPick.svelte';
  import { fleetChangesUrl, fleetOverviewUrl, fleetServicesUrl, hashForHref, replaceHash, parseHash } from '../lib/router.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import EntityLink from '../components/EntityLink.svelte';
  import HelpTip from '../components/HelpTip.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import Select from '../components/Select.svelte';
  import PreviewSection from '../components/PreviewSection.svelte';
  import EntityRefList from '../components/EntityRefList.svelte';
  import RequirementList from '../components/RequirementList.svelte';
  import LimitationsList from '../components/LimitationsList.svelte';
  import DiffChangesTable from '../DiffChangesTable.svelte';
  import DistributionBar from '../components/viz/DistributionBar.svelte';
  import ImpactFigure from '../components/figure/ImpactFigure.svelte';
  import { changeSegments, verdictSegments, confidenceSegments, outcomeSegments } from '../lib/distributions.ts';
  import { OUTCOMES, EFFECTS, EFFECT_META, effectOf } from '../lib/impactFigure.ts';
  import { formatRelative } from '../lib/dateFormat.ts';

  // The Change analysis workspace: ONE screen for the two halves of a single question --
  // WHAT CHANGED between two revisions of a service, and WHAT THAT CHANGE AFFECTS in the
  // operational graph. It replaces the legacy name+version Compare screen, whose service
  // identity was a display NAME (exactly the ambiguity the three-identity model exists to
  // remove); here the workflow is canonical end to end:
  //   canonical ServiceKey -> bounded product service/revision data -> canonical
  //   RevisionKeys -> api.fleetImpactByIdentity(POST) -> ProductImpact.
  // It NEVER loads the raw FleetSnapshot and NEVER calls the legacy GET /api/fleet/impact:
  // the raw snapshot is a low-level/debug contract, not the product contract. The
  // field-level semantic diff is NOT re-derived in the browser -- it arrives in the same
  // bounded ProductImpact answer, so both halves describe the exact same revision pair.
  // The revision selectors are populated from the product service EntityDetail preview
  // and, when that preview truncates, from the bounded/pageable product entities API
  // scoped to the service -- so a truncated preview is never treated as the complete
  // revision universe.
  // The same workspace asks a second question about one service (#471): what happens if
  // it becomes unavailable, is removed or regressed. That event mode answers in the same
  // ProductImpact shape from the same POST, with `event` and `serviceKey` instead of the
  // revision pair; its state (mode=event, event, rev) lives in the URL like the pair does.
  let { params = {} } = $props();
  const serviceKey = $derived(params.svc || '');
  // A legacy compare bookmark carried only a display name. It is resolved to a canonical
  // ServiceKey through the Product API (never guessed) and the URL is then canonicalized.
  const legacyName = $derived(params.name || '');

  const CONSUMER_PAGE = 100;
  // The revision selectors are populated for a two-way (old -> new) comparison, so they
  // are bounded to the most recent revisions rather than materializing an arbitrarily
  // large revision universe in the browser. When more exist, the
  // selector self-describes as incomplete instead of silently claiming completeness.
  const MAX_SELECTOR_REVISIONS = 500;
  const CONFIDENCE_EXPLAIN = {
    contractual: 'Declared dependency with a usable compatibility range.',
    declared: 'Declared dependency, but no usable compatibility range.',
    observed: 'Runtime use observed in a window.',
    corroborated: 'Declared and observed evidence agree.',
    inferred: 'Transitive effect reached through another affected service.',
    unknown: 'Required evidence is incomplete or stale.',
  };
  // Every level, one (i) away on the column header, instead of a legend under the table.
  const CONFIDENCE_HELP = Object.entries(CONFIDENCE_EXPLAIN).map(([k, v]) => `${k}: ${v}`).join(' ');

  // Service + revision universe (product API only).
  let serviceDetail = $state(null);
  let revisions = $state([]);
  let loadingRevs = $state(true);
  let loadError = $state(null);
  let snapshotId = $state('');
  let revGen = 0;
  // revisionsComplete is false when the revision universe is larger than the selector's
  // bound (more revisions exist than are listed), so the UI can say so honestly.
  let revisionsComplete = $state(true);

  // Which question: compare two revisions, or what if this service... (event mode).
  const EVENT_KINDS = [
    { value: 'unavailable', label: 'becomes unavailable' },
    { value: 'removed', label: 'is removed' },
    { value: 'regressed', label: 'regressed' },
  ];
  let mode = $state(untrack(() => (params.event || params.mode === 'event' ? 'event' : 'compare')));
  let eventKind = $state(untrack(() => (EVENT_KINDS.some((k) => k.value === params.event) ? params.event : 'unavailable')));
  let eventRev = $state(untrack(() => params.rev || ''));
  const isEvent = $derived(mode === 'event');
  const outcomeKeys = $derived(isEvent ? EFFECTS : OUTCOMES);

  // Selection.
  let fromRevKey = $state('');
  let toRevKey = $state('');
  // Seeded once: App keys this view on its params, so new params remount it.
  let includeObserved = $state(untrack(() => params.observed === '1'));
  let observedAvailable = $state(false);

  // Analysis (the POST).
  let analyzing = $state(false);
  let analyzeError = $state(null);
  let staleSnapshot = $state(false);
  let result = $state(null);
  let consumerOffset = $state(0);
  // Paging the consumers keeps the answer on screen; only a new comparison replaces it.
  // Swapping the whole result for a loader on every page reset the scroll to the top.
  let pagingConsumers = $state(false);
  let analyzeGen = 0;
  // The reader pressed Compare: the figure may tell its answer (SPEC 6, A2). A restored
  // link or a page of the table is not a request, so it shows the final frame.
  let readerAsked = $state(false);
  let limShown = $state(8);
  // The consumers table lists one outcome when a lane's link asked for it (T3): the
  // lane's count is that list's total. The figure always shows every outcome.
  let outcome = $state(untrack(() => ((params.event ? EFFECTS : OUTCOMES).includes(params.outcome) ? params.outcome : '')));
  // A restored link to one outcome opens the table on it once the answer is in.
  let showTable = untrack(() => !!outcome);

  // Service picker (only when the route carries no service key): search-first, so a
  // service beyond the first page of results is still discoverable.
  let serviceQuery = $state('');
  let serviceResults = $state([]);
  let serviceTotal = $state(0);
  let serviceSearching = $state(false);
  let serviceSearchError = $state(null);
  let serviceSearchSeq = 0;
  // A migrated legacy name that resolved to several services, or to none: the picker
  // says which, rather than silently opening the wrong service or an empty search.
  let migrateNote = $state('');

  function verdictClass(v) {
    if (v === 'compatible') return 'badge-ok';
    if (v === 'incompatible') return 'badge-err';
    return 'badge-neutral';
  }

  // pageRecentRevisions pages the service's revisions through the bounded product
  // entities API (scoped by canonical ServiceKey -- never the FleetSnapshot), stopping
  // at MAX_SELECTOR_REVISIONS so the browser never materializes an arbitrarily large
  // universe just to populate two <select>s. It reports `complete` truthfully: true
  // only when the API's own paging reached the end, false when the selector bound (or
  // the hard page bound) was hit while more remained.
  async function pageRecentRevisions(key) {
    const all = [];
    let offset = 0;
    let complete = false;
    for (let i = 0; i < 100; i++) { // hard page bound; the API is itself bounded per page
      const page = await api.fleetEntities({ kinds: ['revision'], service: key, limit: 200, offset });
      all.push(...(page.entities ?? []));
      if (page.nextOffset == null) { complete = true; break; }
      offset = page.nextOffset;
      // More exist: stay honestly incomplete, and never past the bound (a 200-row page
      // pushed after the check used to leave 600 options for a bound of 500).
      if (all.length >= MAX_SELECTOR_REVISIONS) { all.length = MAX_SELECTOR_REVISIONS; break; }
    }
    return { items: all, complete };
  }

  async function loadRevisions(key) {
    const gen = ++revGen;
    loadingRevs = true;
    loadError = null;
    result = null;
    try {
      const detail = await api.fleetEntityDetail('service', key);
      let refs = (detail.service?.revisions?.items ?? []).slice();
      let complete = !detail.service?.revisions?.truncated; // the preview was the full set
      if (detail.service?.revisions?.truncated) {
        const paged = await pageRecentRevisions(key); // canonical, bounded per page + overall
        refs = paged.items;
        complete = paged.complete;
      }
      if (gen !== revGen) return; // a newer service superseded this load
      revisionsComplete = complete;
      // ponytail: the selector shows revisions newest-first via numeric-aware label
      // collation (1.9.0 < 1.10.0) with the immutable key as a tie-break. This is a
      // display nicety only -- the canonical RevisionKey is what is analyzed, so
      // ordering is never a correctness dependency (unlike the backend sibling order).
      refs.sort((a, b) =>
        String(b.label ?? '').localeCompare(String(a.label ?? ''), undefined, { numeric: true }) ||
        String(a.key).localeCompare(String(b.key)));
      serviceDetail = detail;
      snapshotId = detail.meta?.snapshotId || '';
      revisions = refs;
      // Default to the most recent change (second-newest -> newest); a single revision
      // preselects only the "new" side. The user can pick any pair.
      if (refs.length >= 2) { fromRevKey = refs[1].key; toRevKey = refs[0].key; }
      else if (refs.length === 1) { fromRevKey = ''; toRevKey = refs[0].key; }
      else { fromRevKey = ''; toRevKey = ''; }
      // A shared analysis URL restores its exact revision pair -- but only to a revision
      // the canonical list actually contains, so a stale link degrades to the default
      // pair instead of asking the backend about a key this service does not have.
      const known = new Set(refs.map((r) => r.key));
      const restoredOld = !!params.old && known.has(params.old);
      const restoredNew = !!params.new && known.has(params.new);
      if (restoredOld) fromRevKey = params.old;
      if (restoredNew) toRevKey = params.new;
      // A shared link promises an ANSWER, not a pre-filled form: when the URL names both
      // sides and both still exist, run the comparison so the recipient sees what the
      // sender saw. A partial or stale link falls back to the selectors, which is why
      // this is gated on BOTH sides resolving.
      // An event link answers the same way: the event (and, for a regression, its
      // revision) is the whole question. `mode=event` alone is the empty event form.
      if (mode === 'event') { if (params.event && (eventKind !== 'regressed' || eventRev)) analyze(0); }
      else if (restoredOld && restoredNew) analyze(0);
    } catch (e) {
      if (gen !== revGen) return;
      loadError = e;
    } finally {
      if (gen === revGen) loadingRevs = false;
    }
  }

  async function runServiceSearch() {
    const q = serviceQuery.trim();
    const my = ++serviceSearchSeq;
    if (!q) { serviceResults = []; serviceTotal = 0; serviceSearching = false; serviceSearchError = null; return; }
    serviceSearching = true;
    serviceSearchError = null;
    try {
      const l = await api.fleetEntities({ kinds: ['service'], text: q, limit: 20 });
      if (my !== serviceSearchSeq) return; // a newer query supersedes this response
      serviceResults = l.entities ?? [];
      serviceTotal = l.total ?? serviceResults.length;
    } catch (e) {
      if (my !== serviceSearchSeq) return;
      serviceResults = []; serviceTotal = 0; serviceSearchError = e; // never rendered as "no matches"
    } finally {
      if (my === serviceSearchSeq) serviceSearching = false;
    }
  }

  // resolveLegacyName canonicalizes a migrated compare bookmark's display NAME to a
  // canonical ServiceKey through the Product Entities API, matching on the exact label or
  // key -- never a fuzzy substring. Exactly one match canonicalizes the URL (a replace, so
  // Back does not bounce); several or none fall through to the search picker with an
  // honest note. It never fabricates a key -- and it draws neither conclusion from a page
  // that did not carry every match, where a single visible "payments" may not be the only
  // one and an invisible one is not a missing one (see lib/entityResolve.ts).
  async function resolveLegacyName(name) {
    serviceQuery = name;
    const my = ++serviceSearchSeq;
    serviceSearching = true;
    try {
      const res = await api.fleetEntities({ kinds: ['service'], text: name, limit: NAME_LOOKUP_LIMIT });
      if (my !== serviceSearchSeq) return;
      const { matches: exact, complete } = boundedMatches(res, (e) => e.label === name || e.key === name);
      if (complete && exact.length === 1) { replaceHash(fleetChangesUrl(exact[0].key)); return; }
      // Only a complete page may narrow the picker to the exact matches: on a truncated one
      // the whole page is shown, so the "Showing N of M" hint stays true beside it.
      const narrowed = complete && exact.length > 0;
      serviceResults = narrowed ? exact : (res.entities ?? []);
      serviceTotal = narrowed ? exact.length : (res.total ?? serviceResults.length);
      migrateNote = !complete
        ? `More services match "${name}" than this lookup could read at once, so Pacto cannot tell which one this older link meant. Search for the one you want.`
        : exact.length > 1
          ? `Several services are named "${name}". This link is from an older URL that did not distinguish them. Pick the one you meant.`
          : `No service named "${name}" is in the current operational graph. It may have been renamed or removed.`;
    } catch (e) {
      if (my !== serviceSearchSeq) return;
      serviceResults = []; serviceTotal = 0; serviceSearchError = e;
    } finally {
      if (my === serviceSearchSeq) serviceSearching = false;
    }
  }

  $effect(() => {
    const key = serviceKey;
    if (key) loadRevisions(key);
    else if (legacyName) resolveLegacyName(legacyName);
  });
  onDestroy(() => { serviceSearchSeq++; });

  onMount(async () => {
    // The include-observed control is a placebo unless an observation source exists;
    // capabilities.observed reports it (the demo's embedded traces, a runtime source).
    try { const c = await api.capabilities(); observedAvailable = !!c?.observed; }
    catch { observedAvailable = false; }
    if (!observedAvailable) includeObserved = false;
  });

  async function analyze(offset = 0, paging = false, asked = false) {
    if (!serviceKey || (isEvent ? eventKind === 'regressed' && !eventRev.trim() : !fromRevKey || !toRevKey)) return;
    const gen = ++analyzeGen;
    if (!paging) readerAsked = asked;
    if (paging && result) pagingConsumers = true;
    else analyzing = true;
    analyzeError = null;
    staleSnapshot = false;
    try {
      const question = isEvent
        ? { event: eventKind, revision: eventKind === 'regressed' ? eventRev.trim() : undefined }
        : { fromRevisionKey: fromRevKey, toRevisionKey: toRevKey };
      const res = await api.fleetImpactByIdentity({
        snapshotId: snapshotId || undefined,
        serviceKey,
        ...question,
        includeObserved,
        limit: CONSUMER_PAGE,
        offset,
        outcome: outcome || undefined,
      });
      if (gen !== analyzeGen) return;
      result = res;
      consumerOffset = offset;
      // The analyzed pair becomes the URL, so the exact analysis is shareable and
      // restorable. A replaceState (not a push) keeps Back on the previous SCREEN rather
      // than on every selector tweak.
      history.replaceState(null, '', changesUrl(outcome));
      if (showTable) { showTable = false; await tick(); openConsumers(); }
    } catch (e) {
      if (gen !== analyzeGen) return;
      // A snapshot-id mismatch is a 409: the published snapshot changed, so the honest
      // response is "refetch and retry", never a silently wrong answer.
      if (e instanceof ApiError && e.status === 409) staleSnapshot = true;
      else analyzeError = e instanceof ApiError ? e.message : isEvent ? 'Couldn’t answer this question.' : 'Couldn’t compare these revisions.';
    } finally {
      if (gen === analyzeGen) { analyzing = false; pagingConsumers = false; }
    }
  }

  const changesUrl = (o = '') => (isEvent
    ? fleetChangesUrl(serviceKey, { event: eventKind, rev: eventKind === 'regressed' ? eventRev.trim() : '', outcome: o })
    : fleetChangesUrl(serviceKey, { old: fromRevKey, new: toRevKey, observed: includeObserved, outcome: o }));

  // Switching the question drops the other question's answer, and the URL says which
  // form is open (a replace: a mode is not a screen to go Back to).
  function setMode(/** @type {string} */ m) {
    if (m === mode) return;
    mode = m; result = null; outcome = ''; analyzeError = null; analyzeGen++;
    history.replaceState(null, '', fleetChangesUrl(serviceKey, m === 'event' ? { mode: 'event' } : {}));
  }

  // A lane's count or "N more" lists that outcome. A plain click keeps the answer on
  // screen: it pushes the link's URL (so Back returns to the whole table), pages the
  // table to that outcome and opens it. A modified click is the browser's.
  function showOutcome(/** @type {string} */ o, /** @type {MouseEvent} */ e) {
    if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    e.preventDefault();
    outcome = o;
    history.pushState(null, '', changesUrl(o));
    showTable = true;
    analyze(0, true);
  }
  function setOutcome(/** @type {string} */ o) {
    outcome = o;
    analyze(0, true);
  }
  // Back and Forward between a lane's list and the whole table: the route stays the
  // same view, so the URL's outcome is read here.
  function onHash() {
    const o = parseHash(location.hash).params?.outcome ?? '';
    if (parseHash(location.hash).view === 'changes' && o !== outcome && result) setOutcome(outcomeKeys.includes(o) ? o : '');
  }
  onMount(() => { window.addEventListener('hashchange', onHash); return () => window.removeEventListener('hashchange', onHash); });

  // "N more" in a lane of the figure opens the table: open the fold, focus its summary
  // without the browser's own jump, then scroll it under the sticky navbar (.ca-fold
  // carries the scroll margin).
  function openConsumers() {
    const d = /** @type {HTMLDetailsElement | null} */ (document.getElementById('sec-affected-consumers'));
    if (!d) return;
    d.open = true;
    d.querySelector('summary')?.focus({ preventScroll: true });
    d.scrollIntoView({ block: 'start' });
  }

  async function refetchAndAnalyze() {
    await loadRevisions(serviceKey); // picks up the new snapshot id
    staleSnapshot = false;
    analyze(0);
  }

  function pickServiceOption(key) { if (key) location.hash = fleetChangesUrl(key); }

  const consumers = $derived(result?.consumers ?? { items: [], total: 0, count: 0, offset: 0 });
  const owners = $derived(result?.owners ?? { items: [], total: 0, count: 0 });
  const activeTargets = $derived(result?.activeTargets ?? { items: [], total: 0, count: 0 });
  const limitations = $derived(result?.limitations ?? { items: [], total: 0, count: 0 });
  const changes = $derived(result?.changes ?? { items: [], total: 0, count: 0, breaking: 0, potential: 0, nonBreaking: 0 });
  // The backend renders every value to a bounded display STRING, so a single changed
  // OpenAPI schema can't make the answer unbounded. ponytail: it collapses "absent" and
  // "empty string" to '', which the table then shows as '—' -- the honest reading for the
  // added/removed rows that are the only realistic source of an absent side.
  const changeRows = $derived(changes.items.map((c) => ({
    ...c,
    oldValue: c.oldValue || null,
    newValue: c.newValue || null,
  })));

  // Breadcrumbs place the workspace inside the product shell, and root at Overview like
  // every other product page. The service crumb uses the canonical backend href.
  const trail = $derived.by(() => {
    const root = [{ label: 'Overview', href: fleetOverviewUrl() }];
    if (!serviceKey) return [...root, { label: 'Change analysis' }];
    const svc = serviceDetail?.entity;
    return [
      ...root,
      { label: 'Services', href: fleetServicesUrl() },
      svc?.href ? { label: svc.label || svc.key, href: hashForHref(svc.href) } : { label: svc?.label || serviceKey },
      { label: 'Change analysis' },
    ];
  });
  // Version-range verdicts are per consumer (the range each one declared), never per
  // field, and come from the backend tallies over EVERY consumer.
  const verdictCount = (k) => (consumers.byVerdict ?? []).find((b) => b.key === k)?.count ?? 0;
  // The figure's chips name these; the table marks them so every name is readable in full.
  const highlighted = $derived(new Set((consumers.highlights ?? []).map((c) => c.service?.key)));
  const outcomeCount = (k) => (consumers.byOutcome ?? []).find((b) => b.key === k)?.count ?? 0;
  const consumerMeta = $derived(
    !consumers.total ? 'None'
      : isEvent ? `${formatCount(consumers.total)}${outcomeCount('breaks') ? ` · ${formatCount(outcomeCount('breaks'))} break` : ''}`
        : `${formatCount(consumers.total)}${verdictCount('incompatible') ? ` · ${formatCount(verdictCount('incompatible'))} incompatible` : ''}${consumers.refused ? ` · ${formatCount(consumers.refused)} refused` : ''}`,
  );
  // What the table pages over: every consumer, or those of the outcome a lane asked for.
  const cMatching = $derived(consumers.matching ?? consumers.total ?? 0);
  const outcomeLabel = (/** @type {string} */ o) => EFFECT_META[o]?.label ?? outcomeSegments([{ key: o, count: 0 }])[0]?.label ?? o;
  const outcomeOptions = $derived([{ value: '', label: isEvent ? 'Every effect' : 'Every outcome' }, ...outcomeKeys.map((o) => ({ value: o, label: outcomeLabel(o) }))]);
  const cShownFrom = $derived(cMatching === 0 ? 0 : (consumers.offset ?? 0) + 1);
  const cShownTo = $derived((consumers.offset ?? 0) + (consumers.count ?? 0));
  const cHasPrev = $derived((consumers.offset ?? 0) > 0);
  const cHasNext = $derived(consumers.nextOffset != null);
  const canAnalyze = $derived(!!serviceKey && !analyzing && (isEvent ? eventKind !== 'regressed' || !!eventRev.trim() : !!fromRevKey && !!toRevKey));
  // A regression names a version or an artifact digest: the service's revisions are
  // offered, and any digest may be typed.
  const revisionChoices = $derived(revisions.flatMap((r) => [
    ...(r.secondary ? [{ value: r.secondary, label: `${r.version || r.label} artifact` }] : []),
    ...(r.version ? [{ value: r.version, label: 'version' }] : []),
  ]));
  // Delivery evidence about a regressed revision, and why it could not be read.
  const regression = $derived(result?.regression ?? []);
  const deliveryUnread = $derived(limitations.items.filter((l) => l.code === 'DELIVERY_EVIDENCE_UNAVAILABLE'));
  const REGRESSION_KIND = { failed: 'Failed', check: 'Check failed', rollback: 'Rolled back' };
  const EFFECT_BADGE = { breaks: 'badge-err', 'may-degrade': 'badge-warn', unknown: 'badge-neutral' };
  const MAX_EVIDENCE_LINES = 3;
  // A revision reads as its version and a short digest: the service is already named
  // above the pickers, and two revisions of one version differ only by content.
  const revisionOptions = $derived(revisions.map((r) => ({
    value: r.key,
    label: `${r.version || r.label}${r.secondary ? ` · ${shortDigest(r.secondary)}` : ''}`,
  })));
</script>

<div class="product-page">
<Breadcrumbs {trail} />

<PageHeader
  title="Change analysis"
  subtitle="What changed between two revisions and who it reaches."
/>

{#if !serviceKey}
  <!-- No service in the route: search for one (product entities API, search-first so
       any service is discoverable), then navigate to /fleet/changes/:serviceKey. It
       wears the SHARED .disco-* discovery language (styles/components.css), the same
       one the Operational graph tab lands on: both are search-first entry screens for
       the same shape of question, and rendering one as a taught discovery page and the
       other as a small bare box in the corner of an empty page is precisely how one
       product starts reading as two. -->
  <section class="discovery" data-testid="impact-service-picker">
    <form class="disco-search" role="search" onsubmit={(e) => { e.preventDefault(); runServiceSearch(); }}>
      <input id="impact-pick" type="search" bind:value={serviceQuery} oninput={runServiceSearch}
        placeholder="Search services by name…" aria-label="Search for a service to analyze" />
      <button type="submit" class="btn btn-primary">Search</button>
    </form>
    {#if migrateNote}<p class="migrate-note" role="status" data-testid="changes-migrate-note">{migrateNote}</p>{/if}
    {#if serviceSearching}
      <p class="disco-hint" role="status">Searching…</p>
    {:else if serviceSearchError}
      <div class="partial-banner" role="alert" data-testid="impact-picker-error">Search failed: {serviceSearchError instanceof ApiError ? serviceSearchError.message : 'service search is unavailable'}</div>
    {:else if serviceResults.length}
      <ul class="disco-results" data-testid="impact-picker-results">
        {#each serviceResults as s (s.key)}
          <li><button type="button" onclick={() => pickServiceOption(s.key)}>{s.label}{s.domain ? ` (${s.domain})` : ''}</button></li>
        {/each}
      </ul>
      {#if serviceTotal > serviceResults.length}<p class="disco-hint" data-testid="impact-picker-truncated">Showing {formatCount(serviceResults.length)} of {formatCount(serviceTotal)}. Refine your search to narrow it.</p>{/if}
    {:else if serviceQuery.trim()}
      <p class="disco-hint" data-testid="impact-picker-empty">No services match "{serviceQuery}".</p>
    {:else}
      <div class="disco-placeholder" data-testid="changes-discovery-placeholder" aria-hidden="true">
        <svg viewBox="0 0 120 64" class="disco-ph-glyph" role="presentation" focusable="false">
          <rect x="10" y="16" width="34" height="32" rx="6" /><rect x="76" y="16" width="34" height="32" rx="6" />
          <line x1="48" y1="32" x2="72" y2="32" /><line x1="64" y1="26" x2="72" y2="32" /><line x1="64" y1="38" x2="72" y2="32" />
        </svg>
        <p class="disco-ph-title t-section-title">Pick a service</p>
        <p class="disco-ph-sub">Then two of its revisions to compare.</p>
      </div>
    {/if}

    <p class="disco-more" data-testid="changes-discovery-more">Or <a href={fleetServicesUrl()}>browse services</a> and use Compare revisions on one.</p>
  </section>
{:else if loadingRevs}
  <EmptyState loading message="Loading service revisions…" />
{:else if loadError}
  <EmptyState error title="Couldn’t load this service" message={loadError instanceof ApiError ? loadError.message : String(loadError)} onRetry={() => loadRevisions(serviceKey)} />
{:else}
  <!-- No page navigator: the answer sits under the pickers and every part behind it is a
       fold, so the page is one screen and going back to change a revision is no scroll. -->
  <form class="workspace-controls" id="sec-revisions" onsubmit={(e) => { e.preventDefault(); analyze(0, false, true); }}>
    <fieldset class="ca-mode" data-testid="changes-mode">
      <legend class="visually-hidden">Question</legend>
      <label class="ca-mode-opt"><input type="radio" name="ca-mode" value="compare" checked={!isEvent} onchange={() => setMode('compare')} /> Compare two revisions</label>
      <label class="ca-mode-opt"><input type="radio" name="ca-mode" value="event" checked={isEvent} onchange={() => setMode('event')} /> What if this service…</label>
    </fieldset>
    <div class="svc-line">
      <!-- No "Service" caption here: EntityLink already carries the kind chip, and the
           breadcrumb above already says which service. Three labels for one fact is how
           a screen starts reading like a form instead of a sentence. -->
      {#if serviceDetail?.entity}<EntityLink ref={serviceDetail.entity} showStatus={false} />{:else}<code>{serviceKey}</code>{/if}
    </div>
    {#if isEvent}
    <div class="selectors">
      <div class="field">
        <label for="impact-event">What if this service</label>
        <select id="impact-event" value={eventKind} onchange={(e) => { eventKind = e.currentTarget.value; }}>
          {#each EVENT_KINDS as k (k.value)}<option value={k.value}>{k.label}</option>{/each}
        </select>
      </div>
      {#if eventKind === 'regressed'}
        <div class="field">
          <label for="impact-event-rev">Revision or artifact digest</label>
          <input id="impact-event-rev" type="text" inputmode="text" autocomplete="off" spellcheck="false" list="impact-event-revs"
            placeholder="sha256:… or a version" value={eventRev} oninput={(e) => { eventRev = e.currentTarget.value; }} />
          <datalist id="impact-event-revs">
            {#each revisionChoices as c (c.value)}<option value={c.value}>{c.label}</option>{/each}
          </datalist>
          <span class="t-meta">An artifact digest lets the answer cite what delivery recorded about it.</span>
        </div>
      {/if}
    </div>
    {:else}
    <div class="selectors">
      <div class="field">
        <label for="impact-old-rev">Earlier revision</label>
        <SearchPick id="impact-old-rev" value={fromRevKey} options={revisionOptions} placeholder="Select…" label="Earlier revision"
          disabled={revisions.length === 0} testid="impact-old-rev" onpick={(v) => (fromRevKey = v)} />
      </div>
      <div class="field">
        <label for="impact-new-rev">Later revision</label>
        <SearchPick id="impact-new-rev" value={toRevKey} options={revisionOptions} placeholder="Select…" label="Later revision"
          disabled={revisions.length === 0} testid="impact-new-rev" onpick={(v) => (toRevKey = v)} />
      </div>
    </div>
    {#if revisions.length < 2}
      <p class="text-dim">Fewer than two revisions, so nothing to compare yet.</p>
    {/if}
    {/if}
    {#if !revisionsComplete && !isEvent}
      <p class="text-dim" data-testid="impact-revisions-incomplete">Only the most recent revisions are listed. Open an older one from its revision page.</p>
    {/if}
    <div class="form-actions">
      <!-- An event always reads the runtime callers (an availability question is a
           runtime one), so the toggle belongs to a comparison only. -->
      {#if !isEvent}
      <span class="check-row">
        <label class="check-field">
          <input type="checkbox" bind:checked={includeObserved} disabled={!observedAvailable} />
          Include runtime callers{#if !observedAvailable} <span class="text-dim">(no runtime source)</span>{/if}
        </label>
        <HelpTip label="runtime callers" text="Also count callers seen at runtime that never declared this dependency." />
      </span>
      {:else}<span class="t-meta">Callers seen at runtime are always counted.</span>{/if}
      <button type="submit" class="btn btn-primary" disabled={!canAnalyze}>
        {#if isEvent}{analyzing ? 'Asking…' : 'Ask'}{:else}{analyzing ? 'Comparing…' : 'Compare revisions'}{/if}
      </button>
    </div>
  </form>

  {#if staleSnapshot}
    <div class="partial-banner" role="status">
      <strong>The published snapshot changed.</strong>
      <span>This result would be stale.</span>
      <button type="button" class="btn" onclick={refetchAndAnalyze}>Refresh and retry</button>
    </div>
  {/if}

  {#if analyzeError}
    <EmptyState error title={isEvent ? 'Couldn’t answer this question' : 'Couldn’t compare these revisions'} message={analyzeError} onRetry={() => analyze(consumerOffset)} />
  {:else if analyzing}
    <EmptyState loading message={isEvent ? 'Walking what depends on this service…' : 'Comparing the two revisions…'} />
  {:else if result}
    <!-- The answer, drawn and stated: the figure reads only the backend tallies and the
         ranked highlights, so paging the table below never changes it. Its card says what
         changed, who it reaches and what the gate decides; the table stays the full answer. -->
    <ImpactFigure impact={result} autoplay={readerAsked} laneHref={(o) => changesUrl(o)} onlane={showOutcome} />

    <div class="ca-stack">
    {#if result.event?.kind === 'regressed'}
      <!-- What delivery recorded about the regressed revision, each line with where it
           comes from: a signed record, or an Operation's status (not signed). -->
      <section class="ca-fold ca-evidence" id="sec-delivery-evidence" data-testid="changes-delivery-evidence" aria-labelledby="ca-evidence-h">
        <h2 id="ca-evidence-h">Delivery evidence</h2>
        {#if regression.length}
          <ul class="ca-evidence-list">
            {#each regression as r, ri (ri)}
              <li data-testid="delivery-evidence-item">
                <span class="badge {r.kind === 'rollback' ? 'badge-warn' : 'badge-err'}">{REGRESSION_KIND[r.kind] ?? r.kind}</span>
                <bdi class="mono ca-ev-where" title="{r.namespace}/{r.stage}">{r.namespace}/{r.stage}</bdi>
                {#if r.gate}<span class="t-meta">gate <bdi>{r.gate}</bdi></span>{/if}
                {#if r.message}<span class="ca-ev-msg">{r.message}</span>{/if}
                {#if r.at}<span class="t-meta">{formatRelative(r.at)}</span>{/if}
                <span class="t-meta">{#if r.record}record <code>{r.record}</code>{:else}from status, not signed{/if}</span>
              </li>
            {/each}
          </ul>
        {/if}
        {#if deliveryUnread.length}
          <LimitationsList items={deliveryUnread} />
        {:else if !regression.length}
          <p class="text-dim">Delivery recorded no failure of this revision in the Stages you may read.</p>
        {/if}
      </section>
    {/if}
    {#if !result.event}
    <!-- WHAT CHANGED: the field-level diff of the two contracts, breaking first. A
         statement about the contracts, not about what runs. -->
    <details class="ca-fold disclosure" id="sec-what-changed" data-testid="changes-what-changed">
      <summary><span class="disclosure-caret" data-motion aria-hidden="true"></span><h2>What changed</h2>
        <span class="ca-fold-meta" data-testid="changes-counts">{#if changes.total === 0}No differences{:else}{changes.breaking} breaking · {changes.potential} potentially breaking · {changes.nonBreaking} non-breaking{/if}</span>
      </summary>
      <div class="ca-fold-body">
      {#if changes.total === 0}
        <EmptyState title="No differences" message="Both revisions declare the same contract." level={3} />
      {:else}
        <!-- The mix, drawn from the backend's COMPLETE change counts (which cover
             every difference found, not the truncated table below). -->
        <DistributionBar
          title="Change mix"
          level={3}
          description="Every difference, by how much it can break a consumer."
          segments={changeSegments(changes)}
          total={changes.total}
        />
        {#if changes.truncated}
          <p class="text-dim" data-testid="changes-truncated">First {formatCount(changes.count)} of {formatCount(changes.total)}, breaking first.</p>
        {/if}
        <DiffChangesTable changes={changeRows} />
      {/if}
      </div>
    </details>
    {/if}

    <!-- WHAT IT AFFECTS: the same revision pair over the operational graph. Kept apart
         from the diff because "the contract changed" and "something running is affected"
         are different claims with different evidence. -->
    <div class="ca-folds" id="sec-what-it-affects" data-testid="changes-what-it-affects">
      <details class="ca-fold disclosure" id="sec-affected-consumers" data-testid="changes-consumers">
        <summary><span class="disclosure-caret" data-motion aria-hidden="true"></span><h2>Affected consumers</h2>
          <span class="ca-fold-meta">{consumerMeta}</span>
        </summary>
        <div class="ca-fold-body">
        {#if !consumers.total}
          <EmptyState title="No affected consumers" message={result.event ? 'Nothing in the operational graph depends on this service.' : 'Nothing in the operational graph uses this change.'} level={3} />
        {:else}
          <!-- Both rankings come from the backend tallies over EVERY consumer, so they do
               not change as the table pages. -->
          <div class="impact-viz" data-testid="impact-consumer-viz">
            {#if !result.event}
            <DistributionBar
              title="By version range"
              level={3}
              description="Does each consumer's declared range accept the new revision?"
              segments={verdictSegments(consumers.byVerdict)}
              total={consumers.total}
            />
            {/if}
            <DistributionBar
              title="By evidence"
              level={3}
              description="How each consumer is known."
              segments={confidenceSegments(consumers.byConfidence)}
              total={consumers.total}
            />
          </div>
          <div class="consumer-tools">
            <Select label={isEvent ? 'Effect' : 'Outcome'} value={outcome} options={outcomeOptions} onchange={(e) => setOutcome(e.currentTarget.value)} />
            <span class="text-3">Highlighted consumers, ranked over all {formatCount(consumers.total)}, come first in each outcome.</span>
          </div>
          {#if (consumers.items?.length ?? 0) === 0}
            <p class="text-dim" data-testid="consumers-none-of-outcome">No consumer {isEvent ? 'is in the' : 'is'} {outcomeLabel(outcome).toLowerCase()}{isEvent ? ' lane' : ''}.</p>
          {:else}
          <div class="data-scroll ca-consumers" aria-busy={pagingConsumers}>
            <!-- Grid tracks with a floor each (minmax in ch), never a percentage that can
                 collapse; below the width they need, every row is a card of labelled
                 values (data-label), chosen by the width the table gets. -->
            <table class="data-table ca-table">
              <thead>
                <tr>
                  <th>Consumer</th>
                  <th>Path <HelpTip label="Path" text="The chain of dependencies from the consumer back to the changed service." /></th>
                  <th>{isEvent ? 'Effect' : 'Verdict'}</th>
                  <th>Why <HelpTip label="Why" text="The edges that make it a consumer, where they are known from and the evidence with its age. {CONFIDENCE_HELP}" /></th>
                  <th>Runs on</th>
                  <th>Owner</th>
                </tr>
              </thead>
              <tbody>
                {#each consumers.items as c, ci (`${c.service?.key}:${ci}`)}
                  {@const ev = c.evidence ?? []}
                  {@const targets = c.targets ?? []}
                  <tr data-testid="consumer-row">
                    <td data-label="Consumer" class="consumer-cell">
                      <div class="cell-stack">
                        {#if highlighted.has(c.service?.key)}<span class="badge badge-accent">Highlighted</span>{/if}
                        <!-- A caller no contract describes has no page: its name is text. -->
                        {#if c.uncontracted}<bdi class="cell-clip" title={c.service?.label || c.service?.key}>{c.service?.label || c.service?.key}</bdi><span class="badge badge-neutral">uncontracted</span>
                        {:else}<EntityLink ref={c.service} showStatus={false} />{/if}
                        {#if c.direct}<span class="badge badge-info">Direct</span>{:else}<span class="badge badge-neutral">Transitive · depth {c.depth}</span>{/if}
                      </div>
                    </td>
                    <td data-label="Path" class="path-cell">
                      {#if (c.path?.length ?? 0) > 0}<bdi>{c.path.map((p) => p.label).join(' → ')}</bdi>{#if c.pathTruncated} …{/if}{:else}<span class="text-dim">—</span>{/if}
                    </td>
                    {#if isEvent}
                      <td data-label="Effect"><span class="badge {EFFECT_BADGE[effectOf(c)]}">{EFFECT_META[effectOf(c)].label}</span>{#if c.note}<p class="text-3 consumer-note" data-testid="consumer-note">{c.note}</p>{/if}</td>
                    {:else}
                      <!-- A refused guarantee breaks the consumer whatever its range accepts, so its
                           range verdict is never shown as a plain pass next to the refusal. -->
                      <td data-label="Verdict">{#if c.unmet?.length}<span class="badge badge-neutral" title="The range verdict. The new revision still refuses a guarantee this consumer requires.">range {c.compatibilityVerdict}</span>{:else}<span class="badge {verdictClass(c.compatibilityVerdict)}">{c.compatibilityVerdict}</span>{/if}{#if c.compatibility}<span class="consumer-range t-meta">accepts <code>{c.compatibility}</code></span>{/if}{#if c.note}<p class="text-3 consumer-note" data-testid="consumer-note">{c.note}</p>{/if}
                        {#if c.unmet?.length}
                          <div class="consumer-refused" data-testid="consumer-refused"><RequirementList unmet={c.unmet} /></div>
                        {/if}</td>
                    {/if}
                    <td data-label="Why" class="why-cell">
                      <div class="cell-badges">
                        {#each c.via ?? [] as v (v)}<span class="badge badge-neutral">{v.replace(/_/g, ' ')}</span>{/each}
                        {#if c.provenance}<span class="badge badge-info">{c.provenance}</span>{/if}
                        {#if c.direct && isEvent}<span class="badge {c.required ? 'badge-err' : 'badge-warn'}">{c.required ? 'required' : 'optional'}</span>{/if}
                        <span class="pill" title={CONFIDENCE_EXPLAIN[c.confidence] || ''}>{c.confidence}</span>
                      </div>
                      {#if ev.length}
                        <ul class="why-evidence">
                          {#each ev.slice(0, MAX_EVIDENCE_LINES) as e, ei (ei)}
                            <li><span class="why-msg">{e.message}</span>{#if e.at} <span class="t-meta">{formatRelative(e.at)}</span>{/if}{#if e.stale} <span class="badge badge-warn badge-stale" title="Older than 24h before this answer's snapshot, or without a time, so it does not count as current">stale</span>{/if}</li>
                          {/each}
                          {#if ev.length > MAX_EVIDENCE_LINES}<li class="t-meta">{countOf(ev.length - MAX_EVIDENCE_LINES, 'more fact')}</li>{/if}
                        </ul>
                      {/if}
                      {#if c.resources?.length}
                        <ul class="why-resources">
                          {#each c.resources as r, ri (ri)}
                            <li><bdi class="cell-clip" title={[r.entry, r.environment].filter(Boolean).join(' · ')}>{[r.entry, r.environment].filter(Boolean).join(' · ') || 'resource'}</bdi> <span class="badge {r.phase === 'Ready' ? 'badge-ok' : r.phase === 'Failed' ? 'badge-err' : 'badge-neutral'}">{r.phase}</span></li>
                          {/each}
                          {#if (c.resourcesTotal ?? 0) > c.resources.length}<li class="t-meta">{countOf(c.resourcesTotal - c.resources.length, 'more resource')}</li>{/if}
                        </ul>
                      {/if}
                    </td>
                    <td data-label="Runs on" class="runs-cell">
                      {#if targets.length}
                        <ul class="runs-list">
                          {#each targets.slice(0, 2) as t (t.key)}<li><EntityLink ref={t} showStatus={false} showKind={false} /></li>{/each}
                        </ul>
                        {@const more = Math.max(c.targetsTotal ?? targets.length, targets.length) - Math.min(2, targets.length)}
                        {#if more > 0}<span class="t-meta" title={targets.slice(2).map((t) => t.label || t.key).join(', ')}>+{formatCount(more)}</span>{/if}
                      {:else}<span class="text-dim">—</span>{/if}
                    </td>
                    <td data-label="Owner">
                      {#if c.ownerRef}<span class="cell-clip"><EntityLink ref={c.ownerRef} showStatus={false} showKind={false} /></span>
                      {:else if c.owner}<span class="cell-clip" title={c.owner}>{c.owner}</span>{:else}<span class="text-dim">—</span>{/if}
                    </td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
          {/if}
          <nav class="consumer-pager" aria-label="Consumer pages" data-view-total={cMatching}>
            <span class="text-3">Showing {formatCount(cShownFrom)}–{formatCount(cShownTo)} of {formatCount(cMatching)}{consumers.outcome ? ` ${outcomeLabel(consumers.outcome).toLowerCase()}` : ''}{pagingConsumers ? ' · loading…' : ''}</span>
            <div class="pager-btns">
              <button type="button" class="pg" disabled={!cHasPrev || pagingConsumers} onclick={() => analyze(Math.max(0, (consumers.offset ?? 0) - CONSUMER_PAGE), true)}>Previous</button>
              <button type="button" class="pg" disabled={!cHasNext || pagingConsumers} onclick={() => analyze(consumers.nextOffset, true)}>Next</button>
            </div>
          </nav>
        {/if}
        </div>
      </details>

      <details class="ca-fold disclosure" data-testid="changes-owners-targets">
        <summary><span class="disclosure-caret" data-motion aria-hidden="true"></span><h2>Owners and targets</h2>
          <span class="ca-fold-meta">{countOf(owners.total, 'owner')} · {countOf(activeTargets.total, 'target')}</span>
        </summary>
        <div class="ca-fold-body meta-lists">
          <PreviewSection title="Owners" level={3} role="subsection" total={owners.total} count={owners.count} truncated={owners.truncated} empty="No owners identified.">
            <EntityRefList items={owners.items} showStatus={false} />
          </PreviewSection>
          <PreviewSection title="Operational targets running this" level={3} role="subsection" total={activeTargets.total} count={activeTargets.count} truncated={activeTargets.truncated} empty="No target runs an affected revision.">
            <EntityRefList items={activeTargets.items} />
          </PreviewSection>
        </div>
      </details>

      {#if limitations.count > 0}
        <details class="ca-fold disclosure" data-testid="changes-limitations">
          <summary><span class="disclosure-caret" data-motion aria-hidden="true"></span><h2>Incomplete evidence</h2>
            <span class="ca-fold-meta">{formatCount(limitations.total)}</span>
          </summary>
          <div class="ca-fold-body">
            <!-- Eighty gaps opened at once is a wall; the first eight, then the rest on ask. -->
            <LimitationsList items={limitations.items.slice(0, limShown)} />
            {#if limitations.items.length > limShown}
              <button type="button" class="btn btn-sm ca-show-all" onclick={() => { limShown = Infinity; }}>Show all {formatCount(limitations.items.length)}</button>
            {/if}
            {#if limitations.truncated}<p class="text-dim">Showing {formatCount(limitations.count)} of {formatCount(limitations.total)}.</p>{/if}
          </div>
        </details>
      {/if}
    </div>
    </div>
  {:else}
    <!-- The selectors default to a sensible pair, so telling the user to "choose an
         earlier and a later revision" asked for something already done and left the
         actual next step (press the button) unsaid. Only ask for a selection when one
         is genuinely missing. -->
    {#if isEvent}
      {#if eventKind === 'regressed' && !eventRev.trim()}
        <EmptyState identity title="Name the revision" message="A version, or an artifact digest to read delivery evidence." />
      {:else}
        <EmptyState identity title="Ready to ask" message="Press Ask." />
      {/if}
    {:else if fromRevKey && toRevKey}
      <EmptyState identity title="Ready to compare" message="Press Compare revisions." />
    {:else}
      <EmptyState identity title="Pick two revisions" message="An earlier and a later one." />
    {/if}
  {/if}
{/if}
</div>

<style>
  .impact-viz { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 20rem), 1fr)); gap: var(--sp-4); }

  /* The discovery state is entirely the shared .disco-* language in
     styles/components.css -- nothing about it is specific to this view. */
  .migrate-note { color: var(--c-text-2); font-size: var(--text-sm); margin: 0; }

  /* The two halves of one question ("the contract changed" vs "something running is
     affected") are separate folds under one rule, never one undivided block. */

  .svc-line { display: flex; align-items: center; gap: var(--sp-2); }
  .selectors { display: grid; grid-template-columns: 1fr 1fr; gap: var(--sp-3); }
  .field { display: flex; flex-direction: column; gap: 6px; }
  .field label { color: var(--c-text-3); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); letter-spacing: var(--role-label-tracking); }
  .field :global(select) { padding: var(--sp-2) var(--sp-3); min-height: var(--touch-min); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-bg); color: var(--c-text); font: inherit; font-size: var(--text-sm); max-width: 100%; }
  /* Names ellipsize on one line (the whole name is in the link's title) instead of
     breaking mid-word down a narrow column. */
  .consumer-cell :global(.ei-label) { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; display: block; }
  .consumer-cell :global(.entity-identity) { flex-wrap: nowrap; max-width: 100%; }
  .form-actions { display: flex; align-items: center; justify-content: space-between; gap: var(--sp-3); flex-wrap: wrap; }
  .check-field { display: inline-flex; align-items: center; gap: var(--sp-2); font-size: var(--text-sm); color: var(--c-text-2); }
  .check-field input:disabled { cursor: not-allowed; }

  .consumer-refused { display: flex; flex-direction: column; gap: var(--sp-1); align-items: flex-start; margin-top: var(--sp-1); }
  .consumer-note { margin: var(--sp-1) 0 0; font-size: var(--text-xs); }
  .consumer-range { display: block; margin-top: var(--sp-1); overflow-wrap: anywhere; }
  .ca-stack { border-top: 1px solid var(--c-border); }
  .ca-fold { border-bottom: 1px solid var(--c-border); padding: var(--sp-2) 0; scroll-margin-top: calc(var(--navbar-h) + var(--sp-4)); }
  .ca-fold > summary h2 { margin: 0; }
  .ca-fold-meta { margin-left: auto; color: var(--c-text-3); font-size: var(--text-sm); }
  .ca-fold-body { display: flex; flex-direction: column; gap: var(--sp-3); padding: var(--sp-2) 0 var(--sp-3); }
  .ca-show-all { align-self: flex-start; }
  .check-row { display: inline-flex; align-items: center; gap: var(--sp-2); }
  .disco-more { margin: var(--sp-3) 0 0; font-size: var(--text-sm); color: var(--c-text-3); }

  .partial-banner { display: flex; align-items: center; gap: var(--sp-3); flex-wrap: wrap; padding: var(--sp-3) var(--sp-4); margin-bottom: var(--sp-4); border: 1px solid var(--c-warn-border); border-radius: var(--radius-sm); background: var(--c-warn-bg); color: var(--c-text-2); font-size: var(--text-sm); }
  .partial-banner strong { color: var(--c-warn); }

  /* The question switch: two radio choices that wrap, each a full touch target. */
  .ca-mode { display: flex; flex-wrap: wrap; gap: var(--sp-2) var(--sp-4); border: 0; margin: 0; padding: 0; min-width: 0; }
  .ca-mode-opt { display: inline-flex; align-items: center; gap: var(--sp-2); min-height: var(--touch-min); font-size: var(--text-sm); color: var(--c-text-2); }
  .field input[type=text] { padding: var(--sp-2) var(--sp-3); min-height: var(--touch-min); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-bg); color: var(--c-text); font: inherit; font-size: var(--text-sm); max-width: 100%; min-width: 0; }

  .ca-evidence { display: flex; flex-direction: column; gap: var(--sp-2); }
  .ca-evidence h2 { margin: 0; }
  .ca-evidence-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .ca-evidence-list li { display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--sp-1) var(--sp-2); min-width: 0; }
  .ca-ev-where, .ca-ev-msg { overflow-wrap: anywhere; min-width: 0; }

  /* The consumers table: a grid whose tracks each have a floor, wide enough for all six;
     narrower than that, every row becomes a card of labelled values. */
  .ca-consumers { container-name: dtable ca-consumers; }
  @container ca-consumers (min-width: 66rem) {
    .ca-table { display: grid; grid-template-columns: minmax(18ch, 1.3fr) minmax(14ch, 1fr) minmax(12ch, 0.8fr) minmax(26ch, 2fr) minmax(14ch, 1fr) minmax(12ch, 0.9fr); }
    .ca-table thead, .ca-table tbody, .ca-table tr { display: contents; }
  }
  @container ca-consumers (max-width: 66rem) {
    .ca-table, .ca-table tbody, .ca-table tr, .ca-table td { display: block; width: 100%; }
    .ca-table thead { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); }
    .ca-table tr { padding: var(--sp-2) 0; }
    .ca-table tr + tr { border-top: 1px solid var(--c-border); }
    .ca-table td { border: none; padding: var(--sp-1) var(--sp-3); }
    .ca-table td[data-label]::before { content: attr(data-label); display: block; color: var(--c-text-3); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); }
  }
  .why-evidence, .why-resources, .runs-list { list-style: none; margin: var(--sp-1) 0 0; padding: 0; display: flex; flex-direction: column; gap: 2px; font-size: var(--text-xs); min-width: 0; }
  .why-msg { overflow-wrap: anywhere; }
  .runs-list { margin: 0; }
  .runs-list :global(.ei-label) { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; display: block; }

  .path-cell { font-family: var(--font-mono, monospace); font-size: var(--text-xs); }
  .consumer-tools { display: flex; flex-wrap: wrap; align-items: center; gap: var(--sp-2) var(--sp-4); margin: var(--sp-3) 0; }
  .consumer-pager { display: flex; align-items: center; justify-content: space-between; gap: var(--sp-3); flex-wrap: wrap; margin-top: var(--sp-3); }
  .pager-btns { display: flex; gap: var(--sp-2); }
  .pg { padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); color: var(--c-text); font: inherit; font-size: var(--text-sm); cursor: pointer; min-height: var(--touch-min); }
  .pg:disabled { color: var(--c-text-3); opacity: 0.5; cursor: not-allowed; }

  .ca-fold-body.meta-lists { display: grid; grid-template-columns: repeat(auto-fit, minmax(240px, 1fr)); gap: var(--sp-4); }

  .text-dim { color: var(--c-text-3); }
  .text-2 { color: var(--c-text-2); }
  .text-3 { color: var(--c-text-3); font-size: var(--text-sm); }

  @media (max-width: 768px) {
    .selectors { grid-template-columns: 1fr; }
  }
</style>
