<script>
  import { onDestroy } from 'svelte';
  import { api, ApiError, SchemaCompatibilityError } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import {
    graphStateFromParams, hasFocus, toggleView, differenceLabel, differenceTone,
    differenceDescription, relationLabel, neighborhoodIsEmpty, MAX_DEPTH,
    defaultPerspectiveForKind, availablePerspectives, revisionLinkAuthoritative,
    perspectiveSupportsDepth, corroborationLabel, corroborationTone, serviceScopedCaveat,
    canonicalFocusForPerspective, projectionFocusMismatch,
  } from '../lib/graphState.ts';
  import { cyEdgeId } from '../lib/neighborhoodGraph.ts';
  import { abbreviateDigests } from '../lib/format.ts';
  import { graphQueryKey } from '../lib/graphSpatial.ts';
  import { snapshotKnowledge } from '../lib/knowledgeState.ts';
  import KnowledgeBanner from '../components/KnowledgeBanner.svelte';
  import EntityStatusBadge from '../components/EntityStatusBadge.svelte';
  import { fleetGraphFocusUrl, fleetGraphDiscoveryUrl, fleetOverviewUrl, fleetAttentionUrl, hashForHref, fleetChangesUrl, replaceHash } from '../lib/router.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import EntityLink from '../components/EntityLink.svelte';
  import EntityIdentity from '../components/EntityIdentity.svelte';
  import IdentityBadge from '../components/IdentityBadge.svelte';
  import HelpTip from '../components/HelpTip.svelte';
  import { kindLabelPlural } from '../lib/entityLabels.ts';
  import LimitationsList from '../components/LimitationsList.svelte';
  import NeighborhoodGraph from '../NeighborhoodGraph.svelte';

  // The product Operational Graph. With no focus it shows a DISCOVERY state
  // (search only) and loads NO neighborhood and NO FleetSnapshot -- never a whole-fleet
  // hairball. With a focus it consumes the bounded PRODUCT neighborhood API and renders
  // an ACTUAL visual topology on the shared Cytoscape engine: mixed service/revision/
  // target nodes, dependency and runs edges, backend-authoritative difference and
  // service-scoped corroboration (never re-inferred, never color alone). An accessible
  // text list of the same relationships accompanies the canvas; selecting a node or
  // edge opens a bounded quick-inspection drawer without navigating away.
  let { params = {}, refreshTick = 0 } = $props();

  const gs = $derived(graphStateFromParams(params));
  const focused = $derived(hasFocus(params));

  // ── focused neighborhood (product API only) ──────────────────────────────────
  const loader = createProductLoader(() => api.fleetNeighborhood({
    kind: gs.kind, key: gs.key, perspective: gs.perspective,
    direction: gs.direction, depth: gs.depth, views: gs.views,
  }));
  $effect(() => {
    if (focused) {
      loader.sync(`${gs.kind}@@${gs.key}@@${gs.perspective}@@${gs.direction}@@${gs.depth}@@${gs.views.join(',')}@@${refreshTick}`, queryKey);
    }
  });
  onDestroy(() => loader.destroy());

  const nb = $derived(focused ? loader.data : null);

  // ── graph query identity ────────────────────────────────────
  // The canonical identity of the QUESTION being asked, independent of the refresh tick.
  // A refresh keeps it, so the canvas is reconciled in place and the user's arrangement
  // survives; a different focus/perspective/view/direction/depth changes it, so the new
  // question gets its own instance and its own saved arrangement.
  const queryKey = $derived(graphQueryKey(gs));
  // `shown` is the neighborhood that actually answers the CURRENT question. The loader
  // keeps the previous data while the next request is in flight (so a background refresh
  // does not blank the screen), which is exactly right for a refresh -- but showing it
  // under a different query would render one graph while the page claims another, and
  // would file its node positions under the new query's key. The loader tags its data
  // with the query it answers, so this is a fact, not a guess.
  //
  // ...with ONE exception, and it is the reason a Depth or Direction toggle used to blank
  // the canvas: while a different question about the SAME SUBJECT is in flight, the
  // previous answer stays mounted. NeighborhoodGraph reconciles the new one into it, so
  // the arrangement and the viewport survive what is, to the reader, an adjustment rather
  // than a navigation. Unmounting here destroyed the instance before it could, whatever
  // NeighborhoodGraph did with its own keying. A change of SUBJECT is a navigation and
  // carries nothing.
  const graphSubject = (k) => (k || '').split('|', 3).join('|');
  const carrying = $derived(!!nb && loader.dataTag !== queryKey && graphSubject(loader.dataTag) === graphSubject(queryKey));
  const shown = $derived(nb && (loader.dataTag === queryKey || carrying) ? nb : null);

  // Canonicalize an old deep link whose focus the backend REPLACED (a bookmarked target
  // URL under the revision perspective resolves to the linked revision). RequestedFocus
  // stays truthful; the backend supplies an explicit projectionFocus, and we replace
  // (not push) the URL to it so a reload stays on the canonical Product URL and the
  // active perspective never contradicts the visible graph.
  $effect(() => {
    if (!shown) return;
    const canon = projectionFocusMismatch(shown, gs.kind, gs.key);
    if (canon) {
      replaceHash(fleetGraphFocusUrl(canon.kind, canon.key, {
        perspective: gs.perspective, views: gs.views, direction: gs.direction, depth: gs.depth,
      }));
    }
  });
  const loading = $derived(focused && loader.loading);
  const error = $derived(focused ? loader.error : null);
  const knowledge = $derived(snapshotKnowledge(shown?.meta));
  const focusRef = $derived(shown?.requestedFocus ?? null);
  const focusNode = $derived((shown?.nodes || []).find((n) => n.focus) || null);

  // Perspective options valid for THIS focus: a service can only be a
  // service projection; a target is a revision projection only when its link is
  // authoritative. Ordinary navigation therefore never produces a backend 422.
  const perspectives = $derived(availablePerspectives(gs.kind, {
    targetRevisionAuthoritative: revisionLinkAuthoritative(focusNode?.revisionState),
  }));
  const depthSupported = $derived(perspectiveSupportsDepth(gs.perspective));
  // Both of these are claims about the answer to the CURRENT question, so neither is made
  // while a carried answer is on screen -- an old effectiveDepth of 1 against a freshly
  // requested depth of 3 would otherwise assert a one-hop projection that is not one.
  const oneHopNote = $derived(!carrying && shown && shown.effectiveDepth < gs.depth);

  // ── quick-inspection drawer (node or edge) ───────────────────────────────────
  // The drawer is a NON-modal side panel: it is not focus-trapped;
  // Escape and the Close button close it and return focus to the control that opened it
  // (a Relationships-list button in the keyboard path), and opening it moves focus into
  // the drawer so a screen reader announces it.
  const selected = $state({ kind: '', node: null, edge: null });
  let drawerEl = $state(null);
  let drawerOpener = null;
  function rememberOpener() {
    const a = document.activeElement;
    drawerOpener = a && a !== document.body ? a : null;
  }
  // Picking a node from the text list points the CANVAS at it too: same pin, same gentle
  // centering a tap would give. Without it the two halves of this screen answered
  // separately -- the drawer described one entity while the topology beside it still
  // emphasised another, and a keyboard reader could never move the canvas at all.
  // `pin: false` is for the opposite direction (the canvas reporting its own tap), where
  // the camera has already run and re-running it re-centres a node already centred.
  function selectNode(node, { pin = true } = {}) {
    if (!node) return;
    rememberOpener();
    selected.kind = 'node'; selected.node = node; selected.edge = null;
    if (pin) controls?.focusNode(node.ref.key);
  }
  function selectEdge(edge) { if (edge) { rememberOpener(); selected.kind = 'edge'; selected.edge = edge; selected.node = null; } }
  function closeDrawer() {
    selected.kind = ''; selected.node = null; selected.edge = null;
    const o = drawerOpener; drawerOpener = null;
    if (o) queueMicrotask(() => o.focus?.());
  }
  // Escape closes the non-modal drawer from anywhere it is open. Handling it at the window
  // (rather than a keydown on the <aside>) keeps the complementary landmark free of a
  // keyboard listener a non-interactive element should not carry, and still works when
  // focus has moved off the drawer.
  function onDrawerKeydown(e) { if (e.key === 'Escape' && selected.kind) { e.preventDefault(); closeDrawer(); } }
  // Move focus into the drawer when it opens so it is announced and Escape-operable.
  $effect(() => {
    if (selected.kind && drawerEl) queueMicrotask(() => drawerEl?.focus());
  });
  // Canvas selection reports the Cytoscape element id; map it back to the backend node
  // (by canonical key) or edge (by the reproduced Cytoscape edge id).
  function selectNodeById(id) { selectNode((shown?.nodes || []).find((n) => n.ref.key === id), { pin: false }); }
  function selectEdgeByCyId(cyId) {
    selectEdge((shown?.edges || []).find((e) => cyEdgeId(e.from.key, e.to.key, e.relation) === cyId));
  }

  // ── graph controls (ephemeral fit/zoom, wired from the canvas) ───────────────
  let controls = $state(null);
  function onControls(c) { controls = c; }

  // ── legend-as-filter ─────────────────────────────────────────────────────────
  // Every legend entry names a distinction the canvas actually draws, so every one of
  // them can switch that distinction off. A dense neighborhood is read by taking things
  // out of it, and this legend was the one place on the screen that already listed
  // exactly the right things to take out -- as static text. Nothing leaves the data or
  // the text list: hidden categories fade on the canvas and the summary line names them.
  const LEGEND_GROUPS = [
    { title: 'Nodes', items: [
      { key: 'kind:service', label: 'Service', cls: 'lg-node lg-service' },
      { key: 'kind:revision', label: 'Revision', cls: 'lg-node lg-revision' },
      { key: 'kind:target', label: 'Operational target', cls: 'lg-node lg-target' },
    ] },
    { title: 'Relationship', items: [
      { key: 'rel:dependency', label: 'Depends on', cls: 'lg-edge lg-dep' },
      { key: 'rel:runs', label: 'Runs', cls: 'lg-edge lg-runs' },
    ] },
    { title: 'Reconciliation', items: [
      { key: 'state:matched', label: 'Matched / corroborated', cls: 'lg-edge lg-matched' },
      { key: 'state:expected-not-observed', label: 'Expected, not observed', cls: 'lg-edge lg-eno' },
      { key: 'state:drift', label: 'Observed, not expected', cls: 'lg-edge lg-drift' },
      { key: 'state:insufficient', label: 'Insufficient evidence', cls: 'lg-edge lg-insufficient' },
    ] },
  ];
  let hiddenKeys = $state([]);
  const legendShown = (k) => !hiddenKeys.includes(k);
  function toggleLegend(k) {
    hiddenKeys = hiddenKeys.includes(k) ? hiddenKeys.filter((x) => x !== k) : [...hiddenKeys, k];
  }
  $effect(() => { controls?.applyLegendFilter(new Set(hiddenKeys)); });

  // What the canvas is showing right now, in words. The text alternative listed the
  // nodes and the edges but never said how many, never said the legend was hiding some
  // of them and never said which one the drawer was about -- so a reader who cannot see
  // the canvas was the only reader those three facts were kept from. Polite, because
  // none of it interrupts anything.
  const hiddenLabels = $derived(
    LEGEND_GROUPS.flatMap((g) => g.items).filter((i) => hiddenKeys.includes(i.key)).map((i) => i.label)
  );
  const selectedSummary = $derived(
    selected.kind === 'node' ? `Selected ${selected.node?.ref?.label || selected.node?.ref?.key}.`
      : selected.kind === 'edge' ? `Selected the relationship from ${selected.edge?.from?.label || selected.edge?.from?.key} to ${selected.edge?.to?.label || selected.edge?.to?.key}.`
      : ''
  );
  const graphSummary = $derived(!shown ? '' : [
    `${shown.nodes.length} ${shown.nodes.length === 1 ? 'entity' : 'entities'} and ${shown.edges.length} ${shown.edges.length === 1 ? 'relationship' : 'relationships'}.`,
    hiddenLabels.length ? `Dimmed on the canvas: ${hiddenLabels.join(', ')}.` : '',
    selectedSummary,
  ].filter(Boolean).join(' '));

  // ── control -> URL (shareable graph state; never canvas coordinates) ─────────
  function go(patch) {
    const next = { perspective: gs.perspective, views: gs.views, direction: gs.direction, depth: gs.depth, ...patch };
    location.hash = fleetGraphFocusUrl(gs.kind, gs.key, next);
    closeDrawer();
  }
  function setPerspective(p) {
    const depth = perspectiveSupportsDepth(p) ? gs.depth : 1;
    // A perspective that reinterprets identity (target->service, target->revision,
    // revision->service) canonicalizes the URL to the entity actually projected, so the
    // URL and the visible graph never disagree. Keys come from the
    // backend neighborhood data (focusService / the runs edge), never inferred. A push
    // (not replace) so Back returns to the previous canonical route.
    const canon = canonicalFocusForPerspective(shown, gs.kind, p);
    if (canon && (canon.kind !== gs.kind || canon.key !== gs.key)) {
      location.hash = fleetGraphFocusUrl(canon.kind, canon.key, { perspective: p, views: gs.views, direction: gs.direction, depth });
      closeDrawer();
      return;
    }
    go({ perspective: p, depth });
  }
  function setDirection(d) { go({ direction: d }); }
  function setDepth(d) { if (depthSupported) go({ depth: Math.max(1, Math.min(MAX_DEPTH, d)) }); }
  function flipView(v) { go({ views: toggleView(gs.views, v) }); }
  function expand() { if (depthSupported && gs.depth < MAX_DEPTH) go({ depth: gs.depth + 1 }); } // backend re-merges a larger bounded neighborhood
  function resetFocus() { location.hash = fleetGraphDiscoveryUrl(); }

  const VIEW_DEFS = [
    { v: 'expected', label: 'Expected', help: 'Contract-declared relationships (intent).' },
    { v: 'observed', label: 'Observed', help: 'Relationships backed by runtime observation.' },
    { v: 'differences', label: 'Differences', help: 'Where declared intent and observed reality diverge.' },
  ];
  // The three layers are DEFINED in the discovery state's legend -- but a reader who
  // arrives on a focused graph from a deep link or an entity page never sees discovery,
  // and on that screen the only explanation was a `title=` tooltip: mouse-only, gone on
  // touch, unreachable by keyboard. One help affordance beside the
  // control carries all three, built from the same source so the two cannot drift.
  const VIEW_HELP = VIEW_DEFS.map((d) => `${d.label}: ${d.help}`).join(' ');

  // ── discovery search (search-first entry, product-honest) ────────────────────
  let queryText = $state('');
  let results = $state([]);
  let searchTotal = $state(0);
  let searching = $state(false);
  let searchError = $state(null);
  let searchSeq = 0;
  function runSearch() {
    const q = queryText.trim();
    const mySeq = ++searchSeq;
    if (!q) { results = []; searchTotal = 0; searching = false; searchError = null; return; }
    searching = true;
    searchError = null;
    // Only graph-focusable kinds: the graph projects services, revisions and targets.
    api.fleetEntities({ text: q, kinds: ['service', 'revision', 'target'], limit: 20 })
      .then((r) => {
        if (mySeq !== searchSeq) return; // a newer query supersedes this response
        results = r.entities || [];
        searchTotal = r.total ?? results.length;
        searchError = null;
      })
      .catch((e) => {
        if (mySeq !== searchSeq) return;
        // Product-honest: a transport/schema failure is NOT "no matches". Keep the
        // error distinct so the UI never renders an empty state after a failed request.
        results = [];
        searchTotal = 0;
        searchError = e;
      })
      .finally(() => { if (mySeq === searchSeq) searching = false; });
  }
  function submitSearch(e) { e.preventDefault(); runSearch(); }
  onDestroy(() => { searchSeq++; });

  function searchErrorMessage(e) {
    if (e instanceof SchemaCompatibilityError) return 'The dashboard and backend API versions differ; reload to update.';
    if (e instanceof ApiError) return `Search failed (HTTP ${e.status}). ${e.message}`;
    return 'Search is unavailable right now. Check your connection and try again.';
  }
</script>

<svelte:window onkeydown={onDrawerKeydown} />

<div class="product-page">
  <Breadcrumbs trail={focused
    ? [{ label: 'Overview', href: fleetOverviewUrl() }, { label: 'Operational graph', href: fleetGraphDiscoveryUrl() }, { label: focusRef?.label || gs.key }]
    : [{ label: 'Overview', href: fleetOverviewUrl() }, { label: 'Operational graph' }]} />

  <!-- One page title, outside both branches, in the shared page-header grammar. Each
       branch used to declare its own h1: the workspace was the only product route whose
       title moved, changed its neighbours and re-rendered when the user searched. What
       the focus IS belongs in the header too -- it is the second half of the page's
       name once one is chosen. -->
  <PageHeader
    title="Operational graph"
    subtitle={focused ? '' : 'The operational graph is search-first: pick one entity and see its local neighborhood render below. It never opens every service at once.'}
  >
    {#if focused}
      <div class="gv-focus">
        {#if focusRef}<EntityIdentity ref={focusRef} />{/if}
        <button type="button" class="btn gv-reset" onclick={resetFocus} data-testid="graph-reset">Reset focus</button>
      </div>
    {/if}
  </PageHeader>

  {#if !focused}
    <!-- Discovery state: search-first, no fleet hairball, no request. -->
    <section class="discovery" data-testid="graph-discovery">

      <form class="disco-search" role="search" onsubmit={submitSearch}>
        <input type="search" bind:value={queryText} oninput={runSearch} placeholder="Search services, revisions, operational targets..." aria-label="Search for a service, revision or target to focus the graph" />
        <button type="submit" class="btn btn-primary">Search</button>
      </form>

      {#if searching}
        <p class="disco-hint" role="status">Searching...</p>
      {:else if searchError}
        <div class="gv-error" role="alert" data-testid="graph-search-error">{searchErrorMessage(searchError)}</div>
      {:else if results.length > 0}
        <ul class="disco-results" data-testid="graph-search-results">
          {#each results as r (r.kind + '::' + r.key)}
            <li>
              <a href={fleetGraphFocusUrl(r.kind, r.key, { perspective: defaultPerspectiveForKind(r.kind) })} data-testid="graph-focus-link">
                <EntityIdentity ref={r} />
              </a>
            </li>
          {/each}
        </ul>
        {#if searchTotal > results.length}
          <p class="disco-hint" data-testid="graph-search-truncated">Showing {results.length} of {searchTotal}. Refine your search to narrow the results.</p>
        {/if}
      {:else if queryText.trim()}
        <p class="disco-hint" data-testid="graph-search-empty">No entities match "{queryText}".</p>
      {:else}
        <!-- Resting discovery state: an unmistakable
             affordance that a graph renders HERE once a focus is chosen -- so the tab reads
             as a graph discovery experience, not an empty page -- without auto-rendering the
             whole fleet. -->
        <div class="disco-placeholder" data-testid="graph-discovery-placeholder" aria-hidden="true">
          <svg viewBox="0 0 120 64" class="disco-ph-glyph" role="presentation" focusable="false">
            <line x1="30" y1="32" x2="66" y2="18" /><line x1="30" y1="32" x2="66" y2="46" /><line x1="66" y1="18" x2="96" y2="32" />
            <circle cx="30" cy="32" r="9" /><circle cx="66" cy="18" r="7" /><circle cx="66" cy="46" r="7" /><circle cx="96" cy="32" r="7" />
          </svg>
          <p class="disco-ph-title t-section-title">Select a service, revision or operational target to render its local graph</p>
          <p class="disco-ph-sub">Search above, or start from an entity that needs attention. The graph appears right here once you pick a focus.</p>
        </div>
      {/if}

      <div class="disco-panels">
        <div class="disco-panel">
          <h2>Start from what needs attention</h2>
          <p>Jump straight into the graph focused on an operational problem.</p>
          <a class="gv-link" href={fleetAttentionUrl()}>Open attention triage &rarr;</a>
        </div>
        <div class="disco-panel">
          <h2>What the graph shows</h2>
          <dl class="disco-legend">
            {#each VIEW_DEFS as vd}
              <dt><IdentityBadge label={vd.label} tone={vd.v === 'differences' ? 'warn' : (vd.v === 'observed' ? 'info' : 'ok')} /></dt>
              <dd>{vd.help}</dd>
            {/each}
          </dl>
        </div>
      </div>
    </section>
  {:else}
    <!-- Focused neighborhood: an actual visual topology. -->
    <div class="workspace-controls is-row" role="group" aria-label="Graph controls">
      <div class="gv-ctl">
        <span class="gv-ctl-k">Perspective</span>
        <div class="gv-seg">
          {#each perspectives as p}
            <!-- The wire enum is service/revision/target; the button says what the user
                 reads everywhere else in the product ("Operational targets", never a
                 lowercase "target"). data-testid keeps the wire value for tests. -->
            <button type="button" class:active={gs.perspective === p} onclick={() => setPerspective(p)} data-testid="perspective-{p}">{kindLabelPlural(p)}</button>
          {/each}
        </div>
      </div>
      <div class="gv-ctl">
        <span class="gv-ctl-k">Knowledge <HelpTip label="the knowledge layers" text={VIEW_HELP} /></span>
        <div class="gv-seg">
          {#each VIEW_DEFS as vd}
            <button type="button" class:active={gs.views.includes(vd.v)} title={vd.help} onclick={() => flipView(vd.v)} data-testid="view-{vd.v}" aria-pressed={gs.views.includes(vd.v)}>{vd.label}</button>
          {/each}
        </div>
      </div>
      <div class="gv-ctl">
        <span class="gv-ctl-k">Direction</span>
        <div class="gv-seg">
          <button type="button" class:active={gs.direction === 'dependencies'} onclick={() => setDirection('dependencies')} data-testid="dir-dependencies">Dependencies</button>
          <button type="button" class:active={gs.direction === 'dependents'} onclick={() => setDirection('dependents')} data-testid="dir-dependents">Dependents</button>
          <button type="button" class:active={gs.direction === 'both'} onclick={() => setDirection('both')} data-testid="dir-both">Both</button>
        </div>
      </div>
      {#if depthSupported}
        <div class="gv-ctl">
          <span class="gv-ctl-k">Depth</span>
          <div class="gv-seg gv-depth">
            <button type="button" onclick={() => setDepth(gs.depth - 1)} disabled={gs.depth <= 1} aria-label="Decrease depth">-</button>
            <span data-testid="graph-depth">{gs.depth}</span>
            <button type="button" onclick={() => setDepth(gs.depth + 1)} disabled={gs.depth >= MAX_DEPTH} aria-label="Increase depth">+</button>
          </div>
        </div>
        <button type="button" class="btn" onclick={expand} disabled={gs.depth >= MAX_DEPTH} data-testid="graph-expand">Expand</button>
      {/if}
      <div class="gv-ctl gv-viewctl">
        <span class="gv-ctl-k">View</span>
        <div class="gv-seg">
          <!-- Fit re-frames the arrangement you have. Reset layout throws it away and
               lays the graph out again -- two different operations, so two buttons. -->
          <button type="button" onclick={() => controls?.fit()} data-testid="graph-fit" aria-label="Fit graph to view">Fit</button>
          <button type="button" onclick={() => controls?.zoomIn()} data-testid="graph-zoom-in" aria-label="Zoom in">+</button>
          <button type="button" onclick={() => controls?.zoomOut()} data-testid="graph-zoom-out" aria-label="Zoom out">-</button>
          <button type="button" onclick={() => controls?.resetLayout()} data-testid="graph-reset-layout" aria-label="Reset the graph layout, discarding your arrangement">Reset layout</button>
        </div>
      </div>
    </div>

    <!-- A refresh keeps the rendered graph MOUNTED. Swapping it for a loading line every
         poll destroyed the canvas and rebuilt it from scratch, which is what made a
         background refresh throw away the arrangement -- and it made the whole
         reconcile-in-place path unreachable. The refresh says so in a status line
         instead, and a refresh that fails leaves the last good answer on screen with the
         failure stated rather than blanking it. -->
    {#if !shown && loading}
      <p class="gv-status" role="status">Loading the neighborhood...</p>
    {:else if !shown && error}
      <div class="gv-error" role="alert">Couldn't load the neighborhood: {error instanceof Error ? error.message : String(error)}</div>
    {:else if shown}
      {#if loading}
        <p class="gv-status" role="status" data-testid="graph-refreshing">Refreshing the neighborhood...</p>
      {:else if error}
        <div class="gv-error" role="alert" data-testid="graph-refresh-error">Couldn't refresh the neighborhood, so this is the last answer we got: {error instanceof Error ? error.message : String(error)}</div>
      {/if}
      <KnowledgeBanner {knowledge} noun="neighborhood" testid="graph-knowledge-caveat" />
      {#if !carrying && shown.truncated}
        <div class="gv-caveat tone-warn" role="status" data-testid="graph-truncated">
          This neighborhood is bounded and was truncated. Narrow the direction or depth, or open an entity to continue.
        </div>
      {/if}
      {#if oneHopNote}
        <div class="gv-caveat tone-info" role="status" data-testid="graph-effective-depth">
          The {gs.perspective} projection is evaluated one hop deep; deeper exploration lives in the revision projection.
        </div>
      {/if}

      <!-- The drawer column is reserved only while a drawer is open. Reserved always, the
           flagship screen spent a third of its width on an empty region with no border,
           no heading and nothing to explain it, and squeezed the topology -- the one
           thing the page exists to show -- into the remainder. -->
      <div class="gv-body" class:gv-body-drawer={selected.kind === 'node' || selected.kind === 'edge'}>
        <div class="gv-main">
          {#if neighborhoodIsEmpty(shown)}
            <p class="gv-status" data-testid="graph-empty">No {gs.direction === 'both' ? 'related entities' : gs.direction} are known for this focus under the selected views.</p>
          {:else}
            <!-- Primary: the visual Cytoscape topology. -->
            <NeighborhoodGraph neighborhood={shown} focusKey={focusRef?.key || ''} {queryKey} onSelectNode={selectNodeById} onSelectEdge={selectEdgeByCyId} oncontrols={onControls} />

            <!-- Legend, and the canvas's filter: every item is a REAL canvas distinction.
                 Node kinds are shapes/borders; edge relation and reconciliation state are
                 line-swatches that mirror exactly what the canvas draws (line style +
                 width + tone), never a decorative badge for a state the canvas can't show.
                 Because each entry names something the canvas draws, each entry can also
                 switch it off. The drawer/text list keeps the precise, scoped wording. -->
            <div class="gv-legend" role="group" data-testid="graph-legend" aria-label="Graph legend and filters">
              {#each LEGEND_GROUPS as grp (grp.title)}
                <span class="lg-group">{grp.title}</span>
                {#each grp.items as it (it.key)}
                  <button
                    type="button"
                    class="lg-item"
                    class:lg-off={!legendShown(it.key)}
                    aria-pressed={legendShown(it.key)}
                    onclick={() => toggleLegend(it.key)}
                    data-testid="graph-legend-toggle"
                    data-legend-key={it.key}
                  ><span class={it.cls} aria-hidden="true"></span> {it.label}</button>
                {/each}
              {/each}
            </div>

            <!-- Accessible text alternative: the same nodes and edges as
                 a semantic list, keyboard-focusable, driving the same drawer. The summary
                 states what the canvas states visually -- how much is here, what the
                 legend is dimming, what is selected -- so the two are not different
                 screens for different readers. -->
            <p class="gv-summary" aria-live="polite" data-testid="graph-summary">{graphSummary}</p>
            <details class="gv-textalt disclosure" data-testid="graph-textalt">
              <summary><span class="disclosure-caret" data-motion aria-hidden="true">&#9656;</span>Relationships (text)</summary>
              <ul class="gv-nodes" aria-label="Graph nodes">
                {#each shown.nodes as n (n.ref.key)}
                  <li>
                    <button
                      type="button" class="gv-textbtn"
                      class:is-focus={n.focus} class:is-selected={selected.node === n}
                      aria-current={selected.node === n ? 'true' : undefined}
                      onclick={() => selectNode(n)} data-testid="graph-node-item"
                    >
                      <EntityIdentity ref={n.ref} />
                    </button>
                  </li>
                {/each}
              </ul>
              <ul class="gv-edges" aria-label="Graph relationships" data-testid="graph-edges">
                {#each shown.edges as e (e.id)}
                  <li>
                    <button
                      type="button" class="gv-edge" class:is-selected={selected.edge === e}
                      aria-current={selected.edge === e ? 'true' : undefined}
                      onclick={() => selectEdge(e)} data-testid="graph-edge"
                    >
                      <span class="gv-endpoint">{e.from.label || e.from.key}</span>
                      <span class="gv-rel" data-testid="edge-relation">{relationLabel(e.relation)}</span>
                      <span class="gv-endpoint">{e.to.label || e.to.key}</span>
                      {#if e.relation !== 'runs' && e.difference}
                        <span class="gv-diff" data-testid="edge-difference"><IdentityBadge label={differenceLabel(e.difference)} tone={differenceTone(e.difference)} title={differenceDescription(e.difference)} /></span>
                      {:else if e.relation !== 'runs' && e.serviceCorroboration}
                        <span class="gv-diff" data-testid="edge-corroboration"><IdentityBadge label={corroborationLabel(e.serviceCorroboration)} tone={corroborationTone(e.serviceCorroboration)} /></span>
                      {/if}
                    </button>
                  </li>
                {/each}
              </ul>
            </details>
          {/if}

          {#if (shown.unresolvedDependencies?.count ?? 0) > 0}
            <section class="gv-unresolved" data-testid="graph-unresolved">
              <h2>Unresolved dependencies ({shown.unresolvedDependencies.total})</h2>
              <ul>
                {#each shown.unresolvedDependencies.items as u (u.from.key + '::' + u.ref)}
                  <li><EntityLink ref={u.from} showStatus={false} /> declares <code>{u.requestedRef || u.ref}</code> - no provider resolves it{#if u.reason}: {u.reason}{/if}</li>
                {/each}
              </ul>
            </section>
          {/if}

          {#if (shown.limitations?.count ?? 0) > 0}
            <section class="gv-limitations" data-testid="graph-limitations">
              <h2>Limitations</h2>
              <LimitationsList items={shown.limitations.items} />
            </section>
          {/if}
        </div>

        <!-- Quick-inspection drawer. -->
        {#if selected.kind === 'node' && selected.node}
          <aside class="gv-drawer" data-testid="graph-drawer" aria-label="Node details" tabindex="-1" bind:this={drawerEl}>
            <div class="gv-drawer-head"><h2>Entity</h2><button type="button" class="gv-close" onclick={closeDrawer} aria-label="Close">x</button></div>
            <EntityIdentity ref={selected.node.ref} />
            <!-- The wire word, not the badge, printed a bare "NonCompliant" beside a
                 canvas whose own legend and every list on the product say "Not
                 compliant" -- and in the plain text tone of a neutral fact, for the one
                 field on the panel that is a state. Same component as every other
                 surface, so the drawer cannot drift from them again. -->
            {#if selected.node.status}
              <p class="gv-drow"><span class="gv-k">Status</span> <EntityStatusBadge kind={selected.node.ref.kind} status={selected.node.status} /></p>
            {/if}
            {#if selected.node.owner}<p class="gv-drow"><span class="gv-k">Owner</span> {selected.node.owner}</p>{/if}
            {#if selected.node.revisionState}<p class="gv-drow"><span class="gv-k">Revision link</span> {selected.node.revisionState}</p>{/if}
            <div class="gv-drawer-actions">
              <a class="gv-link" href={hashForHref(selected.node.ref.href)}>Open full detail &rarr;</a>
              <a class="gv-link" href={fleetGraphFocusUrl(selected.node.ref.kind, selected.node.ref.key, { perspective: defaultPerspectiveForKind(selected.node.ref.kind), views: gs.views, direction: gs.direction })} data-testid="drawer-focus-here">Focus here</a>
              {#if selected.node.ref.kind === 'service'}<a class="gv-link" href={fleetChangesUrl(selected.node.ref.key)}>Compare revisions &rarr;</a>{/if}
            </div>
          </aside>
        {:else if selected.kind === 'edge' && selected.edge}
          <aside class="gv-drawer" data-testid="graph-drawer" aria-label="Relationship details" tabindex="-1" bind:this={drawerEl}>
            <div class="gv-drawer-head"><h2>Relationship</h2><button type="button" class="gv-close" onclick={closeDrawer} aria-label="Close">x</button></div>
            <p class="gv-drow"><EntityLink ref={selected.edge.from} showStatus={false} /> <strong>{relationLabel(selected.edge.relation)}</strong> <EntityLink ref={selected.edge.to} showStatus={false} /></p>
            {#if selected.edge.relation === 'runs'}
              <p class="gv-ddesc">This operational target runs the linked revision. It is an observed link at target scope, not a declared dependency.</p>
            {:else}
              {#if selected.edge.difference}
                <p class="gv-drow"><span class="gv-k">Difference</span> <IdentityBadge label={differenceLabel(selected.edge.difference)} tone={differenceTone(selected.edge.difference)} /></p>
                <p class="gv-ddesc">{differenceDescription(selected.edge.difference)}</p>
              {:else if selected.edge.serviceCorroboration}
                <p class="gv-drow"><span class="gv-k">Corroboration</span> <IdentityBadge label={corroborationLabel(selected.edge.serviceCorroboration)} tone={corroborationTone(selected.edge.serviceCorroboration)} /></p>
                <p class="gv-ddesc" data-testid="edge-scope-caveat">{serviceScopedCaveat(gs.perspective)}</p>
              {/if}
              {#if (selected.edge.declaredClaims?.count ?? 0) > 0}
                <div class="gv-claims">
                  <span class="gv-k">Declared by</span>
                  <ul>
                    {#each selected.edge.declaredClaims.items as c, i (i)}
                      <!-- Through the shared abbreviator, like every other surface that
                           prints a canonical revision key. A 64-hex digest has no break
                           opportunity, so printed whole it also sets this panel's
                           minimum width and pushes the panel off the screen. -->
                      <li title={c.sourceRevision || undefined}>{abbreviateDigests(c.sourceRevision) || 'a revision'}{#if c.compatibility} &middot; <code>{c.compatibility}</code>{/if}{#if c.reconciliation} &middot; {c.reconciliation}{/if}</li>
                    {/each}
                  </ul>
                </div>
              {/if}
              {#if selected.edge.observed}
                <p class="gv-drow"><span class="gv-k">Observed</span> {selected.edge.count || 0} calls{#if selected.edge.observationSources?.total} across {selected.edge.observationSources.total} source(s){/if}{#if selected.edge.stale} &middot; stale window{/if}</p>
              {/if}
            {/if}
          </aside>
        {/if}
      </div>
    {/if}
  {/if}
</div>

<style>
  /* Buttons and the .disco-* discovery language are shared with the rest of the product
     and live in styles/components.css -- two search-first tabs side by side in the nav
     must not look like two different products. This view had reimplemented .btn as
     .gv-btn, and the copy was broken: .gv-btn was declared AFTER .gv-btn-primary at the
     same specificity, so the primary Search button rendered as a plain surface one. */

  /* What the graph is focused ON, beside the page title. The panel around the controls
     below it is the shared .workspace-controls. */
  .gv-focus { display: flex; align-items: center; gap: var(--sp-3); flex-wrap: wrap; }
  .gv-reset { margin-left: auto; }
  .gv-ctl { display: flex; flex-direction: column; gap: 4px; }
  .gv-viewctl { margin-left: auto; }
  .gv-ctl-k { font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.04em; color: var(--c-text-3); }
  .gv-seg { display: inline-flex; border: 1px solid var(--c-border); border-radius: var(--radius-sm); overflow: hidden; }
  .gv-seg button { padding: 6px 10px; border: none; background: var(--c-bg); color: var(--c-text-2); font: inherit; font-size: var(--text-sm); cursor: pointer; text-transform: capitalize; }
  .gv-seg button.active { background: var(--c-accent); color: var(--c-on-accent); }
  .gv-seg button:disabled { opacity: 0.5; cursor: not-allowed; }
  .gv-depth { align-items: center; }
  .gv-depth span { padding: 0 var(--sp-3); }
  .gv-link { color: var(--c-accent); text-decoration: none; font-size: var(--text-sm); }
  .gv-link:hover { text-decoration: underline; }
  .gv-caveat { padding: var(--sp-2) var(--sp-3); border-radius: var(--radius-sm); font-size: var(--text-sm); background: var(--c-warn-bg); border: 1px solid var(--c-warn-border); }
  .gv-status { color: var(--c-text-3); }
  .gv-error { padding: var(--sp-3); border-radius: var(--radius-sm); background: var(--c-err-bg); border: 1px solid var(--c-err); color: var(--c-text); }

  .gv-body { display: grid; grid-template-columns: 1fr; gap: var(--sp-4); }
  .gv-main { display: flex; flex-direction: column; gap: var(--sp-4); }

  .gv-legend { display: flex; flex-wrap: wrap; gap: var(--sp-2) var(--sp-4); padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); font-size: var(--text-sm); color: var(--c-text-2); }
  /* A legend entry is a toggle, so it is a button -- reset to look like the text it
     replaced, and given a hit target and a focus ring like every other control. */
  .lg-item { display: inline-flex; align-items: center; gap: 6px; padding: 2px 4px; margin: -2px -4px; border: 0; border-radius: var(--radius-xs); background: none; font: inherit; color: inherit; cursor: pointer; transition: color var(--motion-feedback), opacity var(--motion-feedback); }
  .lg-item:hover { color: var(--c-text); }
  /* Struck through, not just faded: "off" has to be readable in a screenshot, in a
     high-contrast theme and to a reader who cannot separate these two greys. */
  .lg-off { color: var(--c-text-3); text-decoration: line-through; }
  .lg-off .lg-node, .lg-off .lg-edge { opacity: 0.3; }
  .lg-node { width: 16px; height: 12px; border: 1.5px solid var(--c-text-3); background: var(--c-surface); }
  .lg-service { border-radius: 3px; }
  .lg-revision { border-radius: 3px; border-style: dashed; }
  .lg-target { border-radius: 50%; }
  .lg-group { font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.04em; color: var(--c-text-3); font-weight: 600; margin-left: var(--sp-2); }
  .lg-group:first-child { margin-left: 0; }
  .lg-edge { width: 22px; height: 0; border-top: 2px solid var(--c-text-3); }
  /* Line-swatches mirror the exact canvas edge styles (lib/graph.ts cyStylesheet):
     relation by line-style, reconciliation state by tone + width, so each legend
     entry corresponds to something the canvas actually renders. */
  .lg-dep { border-top-style: solid; }
  .lg-runs { border-top-style: dashed; border-top-color: var(--c-info); }
  .lg-matched { border-top-color: var(--c-ok); }
  .lg-eno { border-top-color: var(--c-info); }
  .lg-drift { border-top-width: 3px; border-top-color: var(--c-warn); }
  .lg-insufficient { border-top-style: dotted; border-top-color: var(--c-neutral); }

  .gv-textalt { border: 1px solid var(--c-border); border-radius: var(--radius-md); padding: var(--sp-2) var(--sp-3); background: var(--c-surface); }
  /* Look and behaviour come from the shared .disclosure class. */
  .gv-nodes, .gv-edges { list-style: none; margin: var(--sp-2) 0 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .gv-nodes { flex-flow: row wrap; }
  .gv-summary { margin: var(--sp-2) 0 0; font-size: var(--text-sm); color: var(--c-text-2); }
  .gv-textbtn { display: inline-flex; padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-bg); cursor: pointer; text-align: left; }
  /* Both marks are inset shadows, not a thicker border: a border that grows on selection
     reflows the row it is in and nudges every row after it, so marking one entry moved
     the list under the pointer that was picking from it. */
  .gv-textbtn.is-focus { border-color: var(--c-accent); box-shadow: inset 0 0 0 1px var(--c-accent); }
  .gv-textbtn.is-selected, .gv-edge.is-selected { border-color: var(--c-accent); box-shadow: inset 0 0 0 2px var(--c-accent); }
  .gv-edge { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; width: 100%; text-align: left; padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-bg); cursor: pointer; }
  .gv-edge:hover, .gv-textbtn:hover { border-color: var(--c-accent); }
  .gv-rel { font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.03em; color: var(--c-text-3); }
  .gv-diff { margin-left: auto; }
  .gv-unresolved, .gv-limitations { border: 1px solid var(--c-border); border-radius: var(--radius-md); padding: var(--sp-3); background: var(--c-surface); }
  /* These were h3s forced back up to section size with a hard-coded font-size -- a
     section title dressed as a subsection, then un-dressed by hand. They sit directly
     under the page h1, so h2 is both the honest outline level and, via base.css, the
     section role. No override left to drift. */
  .gv-unresolved h2, .gv-limitations h2 { margin: 0 0 var(--sp-2); }
  .gv-unresolved ul { margin: 0; padding-left: var(--sp-4); font-size: var(--text-sm); color: var(--c-text-2); display: flex; flex-direction: column; gap: 4px; }

  .gv-drawer { border: 1px solid var(--c-border); border-radius: var(--radius-md); padding: var(--sp-4); background: var(--c-surface); display: flex; flex-direction: column; gap: var(--sp-2); }
  .gv-drawer-head { display: flex; align-items: center; justify-content: space-between; }
  .gv-drawer-head h2 { margin: 0; }
  .gv-close { background: none; border: none; color: var(--c-text-3); cursor: pointer; font-size: var(--text-md); }
  .gv-drow { margin: 0; font-size: var(--text-sm); color: var(--c-text-2); display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .gv-k { font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.04em; color: var(--c-text-3); }
  .gv-ddesc { margin: 0; font-size: var(--text-sm); color: var(--c-text-3); }
  .gv-claims ul { margin: 4px 0 0; padding-left: var(--sp-4); font-size: var(--text-sm); color: var(--c-text-2); }
  .gv-drawer-actions { display: flex; gap: var(--sp-3); flex-wrap: wrap; margin-top: var(--sp-2); }

  @media (min-width: 900px) {
    /* minmax(0, ...) rather than a bare 1fr: a grid item's automatic minimum is its
       content, so one wide child is enough to stop the column shrinking for the drawer. */
    .gv-body.gv-body-drawer { grid-template-columns: minmax(0, 1fr) minmax(280px, 360px); align-items: start; }
  }
</style>
