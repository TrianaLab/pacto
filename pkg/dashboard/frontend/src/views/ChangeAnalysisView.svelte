<script>
  import { onMount, onDestroy, untrack } from 'svelte';
  import { api, ApiError } from '../lib/api.ts';
  import { boundedMatches, NAME_LOOKUP_LIMIT } from '../lib/entityResolve.ts';
  import { classificationClass, completenessClass, completenessLabel, shortDigest, formatCount, countOf } from '../lib/format.ts';
  import SearchPick from '../components/SearchPick.svelte';
  import { formatRelative } from '../lib/dateFormat.ts';
  import { fleetChangesUrl, fleetOverviewUrl, fleetServicesUrl, hashForHref, replaceHash } from '../lib/router.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import EntityLink from '../components/EntityLink.svelte';
  import HelpTip from '../components/HelpTip.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import PreviewSection from '../components/PreviewSection.svelte';
  import EntityRefList from '../components/EntityRefList.svelte';
  import IdentityBadge from '../components/IdentityBadge.svelte';
  import RequirementList from '../components/RequirementList.svelte';
  import LimitationsList from '../components/LimitationsList.svelte';
  import DiffChangesTable from '../DiffChangesTable.svelte';
  import DistributionBar from '../components/viz/DistributionBar.svelte';
  import { changeSegments, verdictSegments, confidenceSegments } from '../lib/distributions.ts';

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
  let limShown = $state(8);

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

  // The release gate `pacto impact` exits on: blocked (1), unverified (2), clear (0).
  const GATE = {
    blocked: { cls: 'badge-err', label: 'Blocked' },
    unverified: { cls: 'badge-warn', label: 'Unverified' },
    clear: { cls: 'badge-ok', label: 'Clear' },
  };

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
      if (restoredOld && restoredNew) analyze(0);
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

  async function analyze(offset = 0, paging = false) {
    if (!serviceKey || !fromRevKey || !toRevKey) return;
    const gen = ++analyzeGen;
    if (paging && result) pagingConsumers = true;
    else analyzing = true;
    analyzeError = null;
    staleSnapshot = false;
    try {
      const res = await api.fleetImpactByIdentity({
        snapshotId: snapshotId || undefined,
        serviceKey,
        fromRevisionKey: fromRevKey,
        toRevisionKey: toRevKey,
        includeObserved,
        limit: CONSUMER_PAGE,
        offset,
      });
      if (gen !== analyzeGen) return;
      result = res;
      consumerOffset = offset;
      // The analyzed pair becomes the URL, so the exact analysis is shareable and
      // restorable. A replaceState (not a push) keeps Back on the previous SCREEN rather
      // than on every selector tweak.
      history.replaceState(null, '', fleetChangesUrl(serviceKey, { old: fromRevKey, new: toRevKey, observed: includeObserved }));
    } catch (e) {
      if (gen !== analyzeGen) return;
      // A snapshot-id mismatch is a 409: the published snapshot changed, so the honest
      // response is "refetch and retry", never a silently wrong answer.
      if (e instanceof ApiError && e.status === 409) staleSnapshot = true;
      else analyzeError = e instanceof ApiError ? e.message : 'Couldn’t compare these revisions.';
    } finally {
      if (gen === analyzeGen) { analyzing = false; pagingConsumers = false; }
    }
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
  // The one-line answer. Version-range verdicts are per consumer (the range each one
  // declared), never per field, and come from the backend tallies over EVERY consumer.
  function classificationLabel(c) {
    const t = String(c || 'unknown').replace(/_/g, ' ').toLowerCase();
    return t.charAt(0).toUpperCase() + t.slice(1);
  }
  const verdictCount = (k) => (consumers.byVerdict ?? []).find((b) => b.key === k)?.count ?? 0;
  const answerText = $derived.by(() => {
    if (!result) return '';
    const what = changes.total === 0
      ? 'No contract differences.'
      : `${countOf(changes.total, 'change')}, ${formatCount(changes.breaking)} breaking.`;
    if (!consumers.total) return `${what} No consumer is affected.`;
    const newLabel = result.newRevision?.version || result.newRevision?.label || 'the new revision';
    const parts = [
      verdictCount('incompatible') ? `${formatCount(verdictCount('incompatible'))} ${verdictCount('incompatible') === 1 ? 'does' : 'do'} not accept ${newLabel}` : '',
      verdictCount('unknown') ? `${formatCount(verdictCount('unknown'))} unknown` : '',
    ].filter(Boolean);
    return `${what} ${countOf(consumers.total, 'consumer')} affected${parts.length ? `: ${parts.join(', ')}` : ''}.`;
  });
  const consumerMeta = $derived(
    consumers.total
      ? `${formatCount(consumers.total)}${verdictCount('incompatible') ? ` · ${formatCount(verdictCount('incompatible'))} incompatible` : ''}`
      : 'None',
  );
  const cShownFrom = $derived(consumers.total === 0 ? 0 : (consumers.offset ?? 0) + 1);
  const cShownTo = $derived((consumers.offset ?? 0) + (consumers.count ?? 0));
  const cHasPrev = $derived((consumers.offset ?? 0) > 0);
  const cHasNext = $derived(consumers.nextOffset != null);
  const canAnalyze = $derived(!!serviceKey && !!fromRevKey && !!toRevKey && !analyzing);
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
  <form class="workspace-controls" id="sec-revisions" onsubmit={(e) => { e.preventDefault(); analyze(0); }}>
    <div class="svc-line">
      <!-- No "Service" caption here: EntityLink already carries the kind chip, and the
           breadcrumb above already says which service. Three labels for one fact is how
           a screen starts reading like a form instead of a sentence. -->
      {#if serviceDetail?.entity}<EntityLink ref={serviceDetail.entity} showStatus={false} />{:else}<code>{serviceKey}</code>{/if}
    </div>
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
    {#if !revisionsComplete}
      <p class="text-dim" data-testid="impact-revisions-incomplete">Only the most recent revisions are listed. Open an older one from its revision page.</p>
    {/if}
    <div class="form-actions">
      <span class="check-row">
        <label class="check-field">
          <input type="checkbox" bind:checked={includeObserved} disabled={!observedAvailable} />
          Include runtime callers{#if !observedAvailable} <span class="text-dim">(no runtime source)</span>{/if}
        </label>
        <HelpTip label="runtime callers" text="Also count callers seen at runtime that never declared this dependency." />
      </span>
      <button type="submit" class="btn btn-primary" disabled={!canAnalyze}>
        {analyzing ? 'Comparing…' : 'Compare revisions'}
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
    <EmptyState error title="Couldn’t compare these revisions" message={analyzeError} onRetry={() => analyze(consumerOffset)} />
  {:else if analyzing}
    <EmptyState loading message="Comparing the two revisions…" />
  {:else if result}
    <!-- The answer first: what kind of change this is and who it reaches, in one line.
         Everything that backs it -- the field diff, the consumer table, owners and targets,
         the evidence gaps -- is folded below with its count, one click away. -->
    <div class="ca-answer" data-testid="changes-answer">
      <p class="ca-answer-line">
        <span class="badge {classificationClass(result.classification)}">{classificationLabel(result.classification)}</span>
        <span>{answerText}</span>
      </p>
      {#if GATE[result.gate]}
        <p class="ca-answer-line" data-testid="changes-gate">
          <span class="badge {GATE[result.gate].cls}">{GATE[result.gate].label}</span>
          <span class="text-2">{result.gateReason}</span>
        </p>
      {/if}
      <p class="impact-summary">
        {#if result.oldRevision || result.newRevision}
          <span class="text-2">{result.oldRevision?.label || '?'} → {result.newRevision?.label || '?'}</span>
        {/if}
        <span class="badge {completenessClass(result.meta?.completeness)}"><span class="badge-dot"></span>{completenessLabel(result.meta?.completeness)}</span>
        {#if result.meta?.asOf}<span class="text-3">Data as of {formatRelative(result.meta.asOf)}</span>{/if}
        <!-- The snapshot id is precise and occasionally decisive, but it is opaque to
             anyone who has not read the data model. The chip states what it MEANS; the id
             itself stays exact, one hover away. -->
        <span class="snap-id" class:match={result.snapshotMatch} title="Snapshot {result.snapshotId}">
          {result.snapshotMatch ? 'Current data ✓' : 'Older snapshot'}
        </span>
      </p>
    </div>

    <div class="ca-stack">
    <!-- WHAT CHANGED: the field-level diff of the two contracts, breaking first. A
         statement about the contracts, not about what runs. -->
    <details class="ca-fold disclosure" id="sec-what-changed" data-testid="changes-what-changed">
      <summary><span class="disclosure-caret" data-motion aria-hidden="true">&#9656;</span><h2>What changed</h2>
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

    <!-- WHAT IT AFFECTS: the same revision pair over the operational graph. Kept apart
         from the diff because "the contract changed" and "something running is affected"
         are different claims with different evidence. -->
    <div class="ca-folds" id="sec-what-it-affects" data-testid="changes-what-it-affects">
      <details class="ca-fold disclosure" id="sec-affected-consumers" data-testid="changes-consumers">
        <summary><span class="disclosure-caret" data-motion aria-hidden="true">&#9656;</span><h2>Affected consumers</h2>
          <span class="ca-fold-meta">{consumerMeta}</span>
        </summary>
        <div class="ca-fold-body">
        {#if (consumers.items?.length ?? 0) === 0}
          <EmptyState title="No affected consumers" message="Nothing in the operational graph uses this change." level={3} />
        {:else}
          <!-- Both rankings come from the backend tallies over EVERY consumer, so they do
               not change as the table pages. -->
          <div class="impact-viz" data-testid="impact-consumer-viz">
            <DistributionBar
              title="By version range"
              level={3}
              description="Does each consumer's declared range accept the new revision?"
              segments={verdictSegments(consumers.byVerdict)}
              total={consumers.total}
            />
            <DistributionBar
              title="By evidence"
              level={3}
              description="How each consumer is known."
              segments={confidenceSegments(consumers.byConfidence)}
              total={consumers.total}
            />
          </div>
          <div class="data-scroll" aria-busy={pagingConsumers}>
            <table class="data-table">
              <colgroup><col style="width:22%" /><col style="width:13%" /><col /><col style="width:12%" /><col style="width:12%" /><col style="width:14%" /></colgroup>
              <thead>
                <tr>
                  <th>Consumer</th>
                  <th>Reach <HelpTip label="Reach" text="Direct consumers depend on the changed service; transitive ones are reached through others." /></th>
                  <th>Path <HelpTip label="Path" text="The chain of dependencies from the consumer back to the changed service." /></th>
                  <th>Verdict</th>
                  <th>Confidence <HelpTip label="Confidence" text={CONFIDENCE_HELP} /></th>
                  <th>Owner</th>
                </tr>
              </thead>
              <tbody>
                {#each consumers.items as c, ci (`${c.service?.key}:${ci}`)}
                  <tr>
                    <td data-label="Consumer" class="consumer-cell"><EntityLink ref={c.service} showStatus={false} /></td>
                    <td data-label="Reach">{#if c.direct}<span class="badge badge-info">Direct</span>{:else}<span class="badge badge-neutral">Transitive · depth {c.depth}</span>{/if}</td>
                    <td data-label="Path" class="path-cell">
                      {#if (c.path?.length ?? 0) > 0}{c.path.map((p) => p.label).join(' → ')}{#if c.pathTruncated} …{/if}{:else}<span class="text-dim">—</span>{/if}
                    </td>
                    <td data-label="Verdict"><span class="badge {verdictClass(c.compatibilityVerdict)}">{c.compatibilityVerdict}</span>{#if c.note}<p class="text-3 consumer-note" data-testid="consumer-note">{c.note}</p>{/if}
                      {#if c.unmet?.length}
                        <!-- A refused guarantee breaks the consumer whatever its range accepts. -->
                        <div class="consumer-refused" data-testid="consumer-refused"><IdentityBadge label="Refused" tone="err" title="The new revision does not offer what this consumer requires." /><RequirementList unmet={c.unmet} /></div>
                      {/if}</td>
                    <td data-label="Confidence"><span class="pill" title={CONFIDENCE_EXPLAIN[c.confidence] || ''}>{c.confidence}</span></td>
                    <td data-label="Owner">{#if c.owner}<span class="cell-clip" title={c.owner}>{c.owner}</span>{:else}<span class="text-dim">—</span>{/if}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
          <nav class="consumer-pager" aria-label="Consumer pages">
            <span class="text-3">Showing {formatCount(cShownFrom)}–{formatCount(cShownTo)} of {formatCount(consumers.total)}{pagingConsumers ? ' · loading…' : ''}</span>
            <div class="pager-btns">
              <button type="button" class="pg" disabled={!cHasPrev || pagingConsumers} onclick={() => analyze(Math.max(0, (consumers.offset ?? 0) - CONSUMER_PAGE), true)}>Previous</button>
              <button type="button" class="pg" disabled={!cHasNext || pagingConsumers} onclick={() => analyze(consumers.nextOffset, true)}>Next</button>
            </div>
          </nav>
        {/if}
        </div>
      </details>

      <details class="ca-fold disclosure" data-testid="changes-owners-targets">
        <summary><span class="disclosure-caret" data-motion aria-hidden="true">&#9656;</span><h2>Owners and targets</h2>
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
          <summary><span class="disclosure-caret" data-motion aria-hidden="true">&#9656;</span><h2>Incomplete evidence</h2>
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
    {#if fromRevKey && toRevKey}
      <EmptyState title="Ready to compare" message="Press Compare revisions." />
    {:else}
      <EmptyState title="Pick two revisions" message="An earlier and a later one." />
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

  .ca-answer { display: flex; flex-direction: column; gap: var(--sp-2); margin: var(--sp-4) 0 var(--sp-3); }
  .ca-answer-line { display: flex; align-items: baseline; gap: var(--sp-2); flex-wrap: wrap; margin: 0; font-size: var(--text-md); color: var(--c-text); }
  .consumer-refused { display: flex; flex-direction: column; gap: var(--sp-1); align-items: flex-start; margin-top: var(--sp-1); }
  .consumer-note { margin: var(--sp-1) 0 0; font-size: var(--text-xs); }
  .impact-summary { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; margin: 0; }
  .ca-stack { border-top: 1px solid var(--c-border); }
  .ca-fold { border-bottom: 1px solid var(--c-border); padding: var(--sp-2) 0; }
  .ca-fold > summary { gap: var(--sp-2); }
  .ca-fold > summary h2 { margin: 0; }
  .ca-fold-meta { margin-left: auto; color: var(--c-text-3); font-size: var(--text-sm); }
  .ca-fold-body { display: flex; flex-direction: column; gap: var(--sp-3); padding: var(--sp-2) 0 var(--sp-3); }
  .ca-show-all { align-self: flex-start; }
  .check-row { display: inline-flex; align-items: center; gap: var(--sp-2); }
  .disco-more { margin: var(--sp-3) 0 0; font-size: var(--text-sm); color: var(--c-text-3); }
  .snap-id { font-family: var(--font-mono, monospace); font-size: var(--text-xs); color: var(--c-text-3); padding: 2px 6px; border: 1px solid var(--c-border); border-radius: 999px; }
  .snap-id.match { color: var(--c-ok); border-color: var(--c-ok-border, var(--c-ok)); }

  .partial-banner { display: flex; align-items: center; gap: var(--sp-3); flex-wrap: wrap; padding: var(--sp-3) var(--sp-4); margin-bottom: var(--sp-4); border: 1px solid var(--c-warn-border); border-radius: var(--radius-sm); background: var(--c-warn-bg); color: var(--c-text-2); font-size: var(--text-sm); }
  .partial-banner strong { color: var(--c-warn); }

  .path-cell { font-family: var(--font-mono, monospace); font-size: var(--text-xs); }
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
