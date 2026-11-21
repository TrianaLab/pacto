<script>
  import { onDestroy } from 'svelte';
  import { api, ApiError } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import {
    graphStateFromParams, hasFocus, toggleView, differenceTone, edgeDifference,
    edgeDifferenceLabel, edgeDifferenceDescription, relationLabel, neighborhoodIsEmpty, MAX_DEPTH,
    defaultPerspectiveForKind, availablePerspectives, revisionLinkAuthoritative,
    perspectiveSupportsDepth, corroborationLabel, corroborationTone, serviceScopedCaveat,
    canonicalFocusForPerspective, projectionFocusMismatch, nextMaxNodes, maxEdgesFor,
  } from '../lib/graphState.ts';
  import { cyEdgeId, graphNodeId, neighborhoodToGraph, laneGraph, GROUP_PREFIX, LANE_CAP, GROUP_THRESHOLD as GROUP_LIMIT, OTHER_TEAMS } from '../lib/neighborhoodGraph.ts';
  import { abbreviateDigests, formatCount, countOf, statusTone } from '../lib/format.ts';
  import { graphQueryKey } from '../lib/graphSpatial.ts';
  import { snapshotKnowledge } from '../lib/knowledgeState.ts';
  import KnowledgeBanner from '../components/KnowledgeBanner.svelte';
  import EntityStatusBadge from '../components/EntityStatusBadge.svelte';
  import { fleetGraphFocusUrl, fleetGraphDiscoveryUrl, fleetOverviewUrl, hashForHref, fleetChangesUrl, replaceHash } from '../lib/router.ts';
  import { hashWith } from '../lib/paging.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import CappedList from '../components/CappedList.svelte';
  import EntityLink from '../components/EntityLink.svelte';
  import EntityIdentity from '../components/EntityIdentity.svelte';
  import IdentityBadge from '../components/IdentityBadge.svelte';
  import BehaviorBadges from '../components/BehaviorBadges.svelte';
  import RequirementList from '../components/RequirementList.svelte';
  import HelpTip from '../components/HelpTip.svelte';
  import { kindLabelPlural, RANGE_NOT_DECLARED } from '../lib/entityLabels.ts';
  import LimitationsList from '../components/LimitationsList.svelte';
  import NeighborhoodGraph from '../NeighborhoodGraph.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import TargetKeyChooser from '../components/TargetKeyChooser.svelte';
  import FleetMap from '../components/FleetMap.svelte';

  // The product Operational Graph. With no focus, or a service focus, it is the fleet
  // map full screen (FleetMap: the whole fleet from the server's aggregates, and the
  // left-to-right focus diagram of one service). A revision, target, owner or external
  // focus keeps the neighbourhood: it consumes the bounded PRODUCT neighborhood API and renders
  // an ACTUAL visual topology on the shared Cytoscape engine: mixed service/revision/
  // target nodes, dependency and runs edges, backend-authoritative difference and
  // service-scoped corroboration (never re-inferred, never color alone). An accessible
  // text list of the same relationships accompanies the canvas; selecting a node or
  // edge opens a bounded quick-inspection drawer without navigating away.
  let { params = {}, refreshTick = 0 } = $props();

  const gs = $derived(graphStateFromParams(params));
  // The map hosts the whole fleet and the service focus; every other kind keeps the
  // neighbourhood below.
  const onMap = $derived(!hasFocus(params) || params.kind === 'service');
  const focused = $derived(!onMap);
  // The map's state: the route's query, the service focus from the path, and a legacy
  // service perspective's depth past one as the second hop.
  const mapParams = $derived.by(() => {
    const { kind, sel, depth, perspective, views, direction, maxNodes, ...rest } = params;
    void perspective; void views; void direction; void maxNodes;
    const out = { ...rest };
    if (kind === 'service' && sel) {
      out.focus = sel;
      if (!out.hops && Number(depth) > 1) out.hops = '2';
    }
    return out;
  });
  // The service focus lives in the path, so a focus change navigates instead of
  // writing ?focus= onto a path that still names the old service.
  function setMapParams(patch) {
    const next = { ...mapParams };
    for (const [k, v] of Object.entries(patch)) { if (v === null || v === '') delete next[k]; else next[k] = v; }
    const { focus, ...q } = next;
    location.hash = hashWith(focus ? fleetGraphFocusUrl('service', focus) : fleetGraphDiscoveryUrl(), q);
  }

  // ── focused neighborhood (product API only) ──────────────────────────────────
  const loader = createProductLoader(() => api.fleetNeighborhood({
    kind: gs.kind, key: gs.key, perspective: gs.perspective,
    direction: gs.direction, depth: gs.depth, views: gs.views, maxNodes: gs.maxNodes, maxEdges: maxEdgesFor(gs.maxNodes),
  }));
  $effect(() => {
    if (focused) {
      loader.sync(`${gs.kind}@@${gs.key}@@${gs.perspective}@@${gs.direction}@@${gs.depth}@@${gs.maxNodes}@@${gs.views.join(',')}@@${refreshTick}`, queryKey);
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
        maxNodes: gs.maxNodes,
      }));
    }
  });
  const loading = $derived(focused && loader.loading);
  const notFound = $derived(focused && !shown && loader.error instanceof ApiError && loader.error.status === 404);
  const error = $derived(focused ? loader.error : null);
  // A target key that does not name one target for certain: the server lists the
  // candidates (409) and the reader chooses one to focus, keeping the graph's state.
  const candidates = $derived(error instanceof ApiError && error.status === 409 ? error.candidates : []);
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
  // A relationship declared by a hundred revisions shows its first five claims.
  let claimsShown = $state(5);
  $effect(() => { if (selected.edge) claimsShown = 5; });
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
    if (pin) controls?.focusNode(graphNodeId(node.ref));
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
    // preventScroll: the drawer is sticky (wide) or a bottom sheet (narrow), so it is
    // already in view; scrolling to it used to throw the reader thousands of pixels.
    if (selected.kind && drawerEl) queueMicrotask(() => drawerEl?.focus({ preventScroll: true }));
  });
  // Canvas selection reports the Cytoscape element id; map it back to the backend node
  // (by canonical key) or edge (by the reproduced Cytoscape edge id).
  function selectNodeById(id) {
    // A team's folded node opens the team instead of a drawer.
    if (id?.startsWith(GROUP_PREFIX)) { openGroup(id); return; }
    selectNode((shown?.nodes || []).find((n) => graphNodeId(n.ref) === id), { pin: false });
  }

  // ── folding a large neighborhood by team ─────────────────────────────────────
  // The canvas is laid out in lanes: what the focus depends on to its left, what uses it
  // to its right. Past GROUP_THRESHOLD entities the ones beyond the direct neighbors are
  // drawn as one node per team and side. Opening a team or showing every entity is per
  // question: a new focus, depth or budget starts folded again.
  let openGroups = $state(new Set());
  let showEvery = $state(false);
  let groupedFor = '';
  $effect(() => { if (queryKey !== groupedFor) { groupedFor = queryKey; openGroups = new Set(); showEvery = false; } });
  const grouped = $derived(shown ? laneGraph(neighborhoodToGraph(shown), { open: openGroups, all: showEvery }) : null);
  function openGroup(id) { openGroups = new Set([...openGroups, id]); }
  // The two "Other teams" groups already name their direction.
  const sideWords = (g) => (g.owner.startsWith(OTHER_TEAMS) ? '' : g.side === 'depends-on' ? ' that it depends on' : ' that use it');
  function selectEdgeByCyId(cyId) {
    // The one edge a group draws stands for many relationships: selecting it opens the group.
    const g = (grouped?.groups ?? []).find((x) => cyId?.startsWith(`${x.id}→`) || cyId?.includes(`→${x.id}:`));
    if (g) { openGroup(g.id); return; }
    selectEdge((shown?.edges || []).find((e) => cyEdgeId(graphNodeId(e.from), graphNodeId(e.to), e.relation) === cyId));
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
      { key: 'kind:group', label: 'A team (grouped)', cls: 'lg-node lg-team' },
      { key: 'kind:external', label: 'External system', cls: 'lg-node lg-external' },
    ] },
    { title: 'Relationship', items: [
      { key: 'rel:dependency', label: 'Depends on', cls: 'lg-edge lg-dep' },
      { key: 'rel:runs', label: 'Runs', cls: 'lg-edge lg-runs' },
      { key: 'rel:reference', label: 'Configuration reference', cls: 'lg-edge lg-ref' },
    ] },
    { title: 'Reconciliation', items: [
      { key: 'state:matched', label: 'Matched / corroborated (observed)', cls: 'lg-edge lg-matched' },
      { key: 'state:expected-not-observed', label: 'Expected, not observed', cls: 'lg-edge lg-eno' },
      { key: 'state:drift', label: 'Observed, not expected', cls: 'lg-edge lg-drift' },
      { key: 'state:insufficient', label: 'Insufficient evidence', cls: 'lg-edge lg-insufficient' },
      { key: 'state:inferred', label: 'Inferred: one end has no contract, or read from configuration', cls: 'lg-edge lg-inferred' },
    ] },
    { title: 'Behavior', items: [
      { key: 'behavior:refused', label: 'Refuses a required guarantee', cls: 'lg-edge lg-refused' },
    ] },
  ];
  // The drawer lists every guarantee the edge's claims found refused, once each.
  const edgeUnmet = (e) => {
    const seen = new Set();
    return (e?.declaredClaims?.items ?? []).flatMap((c) => c.unmet ?? []).filter((m) => {
      const k = JSON.stringify(m);
      return !seen.has(k) && seen.add(k);
    });
  };
  const edgeReason = (e) => (e?.declaredClaims?.items ?? []).find((c) => c.guaranteeReason)?.guaranteeReason ?? '';
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
  // How much of the neighborhood is on screen. totalNodes is the count the traversal
  // reached before the node budget was applied, so it is the honest denominator -- and
  // it is reported by the service projection only, which is why anything at or below
  // the shown count reads as "no denominator" rather than as "you have it all".
  const totalNodes = $derived(shown?.totalNodes ?? 0);
  const withheldNodes = $derived(shown ? Math.max(0, totalNodes - shown.nodes.length) : 0);
  const moreNodes = $derived(nextMaxNodes(gs.maxNodes));
  // Which budget cut the answer, so the note never blames entities when it was the
  // relationships that ran out (and the button asks for both).
  const truncationNote = $derived(
    withheldNodes
      ? `Showing ${formatCount(shown.nodes.length)} of ${formatCount(totalNodes)} entities within this depth.`
      : shown?.truncatedEdges
        ? `Showing ${formatCount(shown.edges.length)} relationships: more exist than this view draws.`
        : 'This neighborhood is bounded and was truncated.'
  );

  // The one question this page answers first: is anything wrong around this focus? Only
  // confirmed problems count -- an entity whose status is an error tone, or a call seen at
  // runtime that no contract declares, or a dependency whose provider refuses a guarantee
  // it requires. "Expected, not observed" is not a problem (it is not proof of non-use),
  // and neither is a guarantee Pacto could not verify, so both stay out of this line.
  const problems = $derived.by(() => {
    if (!shown || carrying || neighborhoodIsEmpty(shown)) return null;
    const failing = shown.nodes.filter((n) => !n.focus && n.ref?.kind !== 'source' && n.status && statusTone(n.status) === 'err').length;
    const undeclared = shown.edges.filter((e) => e.relation !== 'runs' && e.difference === 'observed-not-expected').length;
    const refused = shown.edges.filter((e) => e.guaranteeStatus === 'unmet').length;
    return { failing, undeclared, refused };
  });
  // The focus comes first: it is what the reader asked about. Then what is around it.
  const focusTone = $derived(focusNode?.status ? statusTone(focusNode.status) : '');
  const focusSentence = $derived.by(() => {
    if (!problems) return '';
    const name = focusRef?.label || focusRef?.key || gs.key;
    if (focusRef?.kind === 'external') return `${name} is an external system: no contract describes it, so Pacto checks none.`;
    if (focusTone === 'err') return `${name} breaks its contract.`;
    if (focusTone === 'ok') return `${name} meets its contract.`;
    return focusNode?.status ? `${name} has no confirmed contract result yet.` : '';
  });
  const answer = $derived.by(() => {
    if (!problems) return '';
    const parts = [
      problems.failing ? `${countOf(problems.failing, 'entity', 'entities')} ${problems.failing === 1 ? 'breaks its' : 'break their'} contract` : '',
      problems.undeclared ? `${countOf(problems.undeclared, 'call')} ${problems.undeclared === 1 ? 'is' : 'are'} undeclared` : '',
      problems.refused ? `${countOf(problems.refused, 'dependency', 'dependencies')} ${problems.refused === 1 ? 'has' : 'have'} a refused guarantee` : '',
    ].filter(Boolean);
    const it = focusSentence ? 'it' : focusRef?.label || focusRef?.key || gs.key;
    if (parts.length) return `Around ${it}, ${parts.join(' and ')}.`;
    return knowledge?.incomplete ? `Nothing wrong found around ${it}, but some data is missing.` : `Nothing wrong found around ${it}.`;
  });

  // ── one notice line above the canvas ─────────────────────────────────────────
  // Missing data, a cut answer, team groups and capped lanes each used to be a box of
  // their own, and together they pushed the canvas below the fold. One line names them;
  // the full sentences and their buttons open under it.
  const showTruncated = $derived(!carrying && !!shown?.truncated);
  const showEveryNote = $derived(!grouped?.groups.length && showEvery && (shown?.nodes.length ?? 0) > GROUP_LIMIT);
  const noticeParts = $derived(!shown ? [] : [
    knowledge?.incomplete ? 'Some data is missing' : '',
    showTruncated ? (withheldNodes ? `${formatCount(shown.nodes.length)} of ${formatCount(totalNodes)} entities shown` : 'Some relationships are not drawn') : '',
    oneHopNote ? 'One hop deep' : '',
    grouped?.groups.length ? `${formatCount(grouped.folded)} grouped by team` : '',
    showEveryNote ? 'Every entity is drawn' : '',
    grouped?.more ? `${LANE_CAP} worst direct neighbors per side` : '',
  ].filter(Boolean));

  const graphSummary = $derived(!shown ? '' : [
    `${formatCount(shown.nodes.length)} ${shown.nodes.length === 1 ? 'entity' : 'entities'} and ${formatCount(shown.edges.length)} ${shown.edges.length === 1 ? 'relationship' : 'relationships'}.`,
    withheldNodes ? `${withheldNodes} more within this depth are not shown.` : '',
    grouped?.folded ? `${grouped.folded} of them are grouped by team on the canvas; the list below names each one.` : '',
    grouped?.more ? `${grouped.more.reduce((t, m) => t + m.count, 0)} direct neighbors are not drawn until you show them${grouped.folded ? '.' : '; the list below names each one.'}` : '',
    hiddenLabels.length ? `Dimmed on the canvas: ${hiddenLabels.join(', ')}.` : '',
    selectedSummary,
  ].filter(Boolean).join(' '));

  // ── control -> URL (shareable graph state; never canvas coordinates) ─────────
  function go(patch) {
    const next = { perspective: gs.perspective, views: gs.views, direction: gs.direction, depth: gs.depth, maxNodes: gs.maxNodes, ...patch };
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
    subtitle={focused ? '' : 'Every service on one map. Zoom in, or find a service to focus it.'}
  >
    {#if focused}
      <div class="gv-focus">
        {#if focusRef}<EntityIdentity ref={focusRef} />{/if}
        <button type="button" class="btn gv-reset" onclick={resetFocus} data-testid="graph-reset">Reset focus</button>
      </div>
    {/if}
  </PageHeader>

  {#if onMap}
    <!-- The whole fleet, full screen; the attention rail stays folded until asked for. -->
    <FleetMap variant="full" params={mapParams} onparams={setMapParams} />
  {:else}
    <!-- Focused neighborhood: an actual visual topology. -->
    <!-- No controls over a focus that does not exist: there is nothing for them to drive. -->
    {#if !notFound}
    <div class="workspace-controls is-row" role="group" aria-label="Graph controls">
      <div class="gv-ctl">
        <span class="gv-ctl-k">Perspective</span>
        {#if perspectives.length > 1}
          <div class="gv-seg">
            {#each perspectives as p}
              <!-- The wire enum is service/revision/target; the button says what the user
                   reads everywhere else in the product ("Operational targets", never a
                   lowercase "target"). data-testid keeps the wire value for tests. -->
              <button type="button" class:active={gs.perspective === p} onclick={() => setPerspective(p)} data-testid="perspective-{p}">{kindLabelPlural(p)}</button>
            {/each}
          </div>
        {:else}
          <!-- One perspective is not a choice: plain text, not a one-button segment. -->
          <span class="gv-one" data-testid="perspective-only">{kindLabelPlural(perspectives[0] ?? gs.perspective)}</span>
        {/if}
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
          <button type="button" onclick={() => controls?.resetLayout()} data-testid="graph-reset-layout" aria-label="Reset layout, discarding your arrangement">Reset layout</button>
        </div>
      </div>
    </div>
    {/if}
    <!-- The answer sits under the controls, as on Change analysis, so both workspaces
         start their body at the same distance from the title. -->
    {#if answer}
      <p class="gv-answer" class:has-problems={problems && (problems.failing || problems.undeclared)} data-testid="graph-answer">{#if focusSentence}<span class:gv-focus-err={focusTone === 'err'} data-testid="graph-answer-focus">{focusSentence}</span>{' '}{/if}<span class="gv-around">{answer}</span></p>
    {/if}

    <!-- A refresh keeps the rendered graph MOUNTED. Swapping it for a loading line every
         poll destroyed the canvas and rebuilt it from scratch, which is what made a
         background refresh throw away the arrangement -- and it made the whole
         reconcile-in-place path unreachable. The refresh says so in a status line
         instead, and a refresh that fails leaves the last good answer on screen with the
         failure stated rather than blanking it. -->
    {#if !shown && loading}
      <p class="gv-status" role="status">Loading the neighborhood...</p>
    {:else if !shown && error}
      <!-- A focus that does not exist is not a failure to retry: say so and offer the way
           back. Anything else is a load error with a Retry. -->
      {#if notFound}
        <div class="gv-error-state" role="alert" data-testid="graph-not-found">
          <h2>Nothing found for “{gs.key}”</h2>
          <a class="btn" href={fleetGraphDiscoveryUrl()}>Search again</a>
        </div>
      {:else if candidates.length}
        <TargetKeyChooser message={error.message} {candidates} hrefFor={(key) => fleetGraphFocusUrl('target', key, gs)} />
      {:else}
        <EmptyState error title="Couldn’t load the graph" message={error instanceof Error ? error.message : String(error)} onRetry={() => loader.refresh()} />
      {/if}
    {:else if shown}
      {#if loading}
        <p class="gv-status" role="status" data-testid="graph-refreshing">Refreshing the neighborhood...</p>
      {:else if error}
        <div class="gv-error" role="alert" data-testid="graph-refresh-error">Couldn't refresh the neighborhood, so this is the last answer we got: {error instanceof Error ? error.message : String(error)}</div>
      {/if}
      {#if noticeParts.length}
      <details class="gv-notice disclosure" data-testid="graph-notice">
        <summary><span class="disclosure-caret" data-motion aria-hidden="true"></span>{noticeParts.join(' · ')}</summary>
        <div class="gv-notice-body">
      <KnowledgeBanner {knowledge} noun="neighborhood" testid="graph-knowledge-caveat" />
      {#if showTruncated}
        <div class="gv-caveat" data-testid="graph-truncated">
          {truncationNote}
          {#if moreNodes}
            <button type="button" class="gv-more" data-testid="graph-show-more" onclick={() => go({ maxNodes: moreNodes })}>
              Show up to {moreNodes} entities
            </button>
          {:else}
            Narrow the direction or depth, or open an entity to continue.
          {/if}
        </div>
      {/if}
      {#if oneHopNote}
        <div class="gv-caveat" data-testid="graph-effective-depth">
          The {gs.perspective} view is one hop deep. Use Revisions to go deeper.
        </div>
      {/if}
      {#if !neighborhoodIsEmpty(shown)}
            {#if grouped?.groups.length}
              <div class="gv-caveat" data-testid="graph-grouped">
                {formatCount(grouped.folded)} grouped by team ({countOf(grouped.groups.length, 'group')}). Open one:
                <CappedList items={grouped.groups} max={6} row={true} label="Teams grouped on the graph">{#snippet item(g)}<button type="button" class="gv-more" data-testid="graph-open-group" title="{g.owner}: {formatCount(g.count)} {g.count === 1 ? 'entity' : 'entities'}{sideWords(g)}" aria-label="Open {g.owner}: {formatCount(g.count)} {g.count === 1 ? 'entity' : 'entities'}{sideWords(g)}" onclick={() => openGroup(g.id)}><span aria-hidden="true">{g.side === 'depends-on' ? '← ' : ''}</span>{g.owner} ({formatCount(g.count)})<span aria-hidden="true">{g.side === 'used-by' ? ' →' : ''}</span></button>{/snippet}</CappedList>
                <button type="button" class="gv-more" data-testid="graph-show-every" onclick={() => { showEvery = true; }}>Show every entity</button>
              </div>
            {:else if showEveryNote}
              <div class="gv-caveat" data-testid="graph-grouped">
                Every entity is drawn.
                <button type="button" class="gv-more" data-testid="graph-group-again" onclick={() => { showEvery = false; openGroups = new Set(); }}>Group by team again</button>
              </div>
            {/if}
            {#if grouped?.more}
              <div class="gv-caveat" data-testid="graph-lane-cap">
                Showing the {LANE_CAP} worst direct neighbors per side.
                {#each grouped.more as m (m.id)}<button type="button" class="gv-more" data-testid="graph-open-more" onclick={() => openGroup(m.id)}>Show {formatCount(m.count)} more {m.side === 'depends-on' ? 'dependencies' : 'dependents'}</button>{/each}
              </div>
            {/if}
      {/if}
        </div>
      </details>
      {/if}

      <!-- The drawer column is reserved only while a drawer is open. Reserved always, the
           flagship screen spent a third of its width on an empty region with no border,
           no heading and nothing to explain it, and squeezed the topology -- the one
           thing the page exists to show -- into the remainder. -->
      <div class="gv-body" class:gv-body-drawer={selected.kind === 'node' || selected.kind === 'edge'}>
        <div class="gv-main">
          {#if neighborhoodIsEmpty(shown)}
            <p class="gv-status" data-testid="graph-empty">No {gs.direction === 'both' ? 'related entities' : gs.direction} known under these views.</p>
          {:else}
            <!-- Primary: the visual Cytoscape topology. -->
            <NeighborhoodGraph neighborhood={shown} graph={grouped?.graph} focusKey={focusRef ? graphNodeId(focusRef) : ''} {queryKey} onSelectNode={selectNodeById} onSelectEdge={selectEdgeByCyId} oncontrols={onControls} />

            <!-- Legend, and the canvas's filter: every item is a REAL canvas distinction.
                 Node kinds are shapes/borders; edge relation and reconciliation state are
                 line-swatches that mirror exactly what the canvas draws (line style +
                 width + tone), never a decorative badge for a state the canvas can't show.
                 Because each entry names something the canvas draws, each entry can also
                 switch it off. The drawer/text list keeps the precise, scoped wording. -->
            <!-- Always shown: a reader new to the canvas needs the key right beside it. -->
            <div class="gv-legend" role="group" aria-label="Graph legend and filters" data-testid="graph-legend">
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
              <span class="t-meta lg-hint">Solid: observed. Dashed: expected but not observed, a Runs link or a configuration reference. Dotted: insufficient evidence. Select an entry to hide it.</span>
            </div>

            <!-- Accessible text alternative: the same nodes and edges as
                 a semantic list, keyboard-focusable, driving the same drawer. The summary
                 states what the canvas states visually -- how much is here, what the
                 legend is dimming, what is selected -- so the two are not different
                 screens for different readers. -->
            <p class="gv-summary" aria-live="polite" data-testid="graph-summary">{graphSummary}</p>
            <details class="gv-textalt disclosure" data-testid="graph-textalt">
              <summary><span class="disclosure-caret" data-motion aria-hidden="true"></span>Relationships (text)</summary>
              <ul class="gv-nodes" aria-label="Graph nodes">
                {#each shown.nodes as n (graphNodeId(n.ref))}
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
                      {#if e.relation !== 'runs' && edgeDifference(e)}
                        <span class="gv-diff" data-testid="edge-difference"><IdentityBadge label={edgeDifferenceLabel(e)} tone={differenceTone(edgeDifference(e))} title={edgeDifferenceDescription(e)} /></span>
                      {:else if e.relation !== 'runs' && e.serviceCorroboration}
                        <span class="gv-diff" data-testid="edge-corroboration"><IdentityBadge label={corroborationLabel(e.serviceCorroboration)} tone={corroborationTone(e.serviceCorroboration)} /></span>
                      {/if}
                    </button>
                    <!-- Beside the button, not in it: the badges carry their own "Show all" control. -->
                    {#if e.guaranteeStatus || e.deprecatedInterfaces?.length}<div class="gv-edge-behavior" data-testid="edge-behavior"><BehaviorBadges status={e.guaranteeStatus} reason={edgeReason(e)} deprecated={e.deprecatedInterfaces ?? []} /></div>{/if}
                  </li>
                {/each}
              </ul>
            </details>
          {/if}

          {#if (shown.unresolvedDependencies?.count ?? 0) > 0}
            <!-- A secondary list: a count that opens, not a block that pushes the page down. -->
            <details class="gv-unresolved disclosure" data-testid="graph-unresolved">
              <summary><span class="disclosure-caret" data-motion aria-hidden="true"></span><h2 class="gv-fold-title">Dependencies outside the graph</h2> <span class="gv-chip">{formatCount(shown.unresolvedDependencies.total)}</span></summary>
              <p class="t-meta">Declared, with no known service to draw. External ones are delivered outside Pacto; unresolved ones match nothing Pacto knows.</p>
              <CappedList items={shown.unresolvedDependencies.items} max={8} label="Dependencies outside the graph" testid="graph-unresolved-list">
                {#snippet item(u)}{#if u.external}<IdentityBadge label="external" />{' '}{/if}<EntityLink ref={u.from} showStatus={false} />{' '}declares {u.external ? 'external ' : ''}<code>{u.requestedRef || u.ref}</code>{#if u.reason}{': '}{u.reason}{/if}{/snippet}
              </CappedList>
              {#if shown.unresolvedDependencies.truncated}<p class="t-meta">Showing {formatCount(shown.unresolvedDependencies.count)} of {formatCount(shown.unresolvedDependencies.total)}.</p>{/if}
            </details>
          {/if}

          {#if (shown.limitations?.count ?? 0) > 0}
            <details class="gv-limitations disclosure" data-testid="graph-limitations">
              <summary><span class="disclosure-caret" data-motion aria-hidden="true"></span><h2 class="gv-fold-title">Limitations</h2> <span class="gv-chip">{formatCount(shown.limitations.total ?? shown.limitations.count)}</span></summary>
              <LimitationsList items={shown.limitations.items} />
            </details>
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
              {#if selected.node.ref.kind === 'service'}
                <a class="gv-link" href={fleetChangesUrl(selected.node.ref.key)}>Compare revisions &rarr;</a>
                <a class="gv-link" href={fleetChangesUrl(selected.node.ref.key, { event: 'unavailable' })} data-testid="drawer-what-if">What if it becomes unavailable? &rarr;</a>
              {/if}
            </div>
          </aside>
        {:else if selected.kind === 'edge' && selected.edge}
          <aside class="gv-drawer" data-testid="graph-drawer" aria-label="Relationship details" tabindex="-1" bind:this={drawerEl}>
            <div class="gv-drawer-head"><h2>Relationship</h2><button type="button" class="gv-close" onclick={closeDrawer} aria-label="Close">x</button></div>
            <p class="gv-drow"><EntityLink ref={selected.edge.from} showStatus={false} /> <strong>{relationLabel(selected.edge.relation)}</strong> <EntityLink ref={selected.edge.to} showStatus={false} /></p>
            {#if selected.edge.relation === 'runs'}
              <p class="gv-ddesc">Seen running here. Not a declared dependency.</p>
            {:else}
              {#if selected.edge.relation === 'configuration_reference'}
                <p class="gv-ddesc" data-testid="edge-reference-note">Declared configuration reference. It makes no runtime calls, so observation does not apply to it.</p>
              {/if}
              {#if edgeDifference(selected.edge)}
                <p class="gv-drow"><span class="gv-k">Difference</span> <IdentityBadge label={edgeDifferenceLabel(selected.edge)} tone={differenceTone(edgeDifference(selected.edge))} /></p>
                <p class="gv-ddesc" data-testid="edge-difference-description">{edgeDifferenceDescription(selected.edge)}</p>
              {:else if selected.edge.serviceCorroboration}
                <p class="gv-drow"><span class="gv-k">Corroboration</span> <IdentityBadge label={corroborationLabel(selected.edge.serviceCorroboration)} tone={corroborationTone(selected.edge.serviceCorroboration)} /></p>
                <p class="gv-ddesc" data-testid="edge-scope-caveat">{serviceScopedCaveat(gs.perspective)}</p>
              {/if}
              {#if selected.edge.guaranteeStatus || selected.edge.deprecatedInterfaces?.length}
                <div class="gv-drow gv-behavior" data-testid="drawer-behavior">
                  <span class="gv-k">Behavior</span>
                  <BehaviorBadges status={selected.edge.guaranteeStatus} reason={edgeReason(selected.edge)} deprecated={selected.edge.deprecatedInterfaces ?? []} />
                </div>
                <RequirementList unmet={edgeUnmet(selected.edge)} status={selected.edge.guaranteeStatus} reason={edgeReason(selected.edge)} />
              {/if}
              {#if (selected.edge.declaredClaims?.count ?? 0) > 0}
                <details class="gv-claims disclosure">
                  <summary><span class="disclosure-caret" data-motion aria-hidden="true"></span><span class="gv-k">Declared by</span> <span class="t-meta">{#if selected.edge.declaredClaims.truncated}{formatCount(selected.edge.declaredClaims.count)} of {formatCount(selected.edge.declaredClaims.total)}{:else}{formatCount(selected.edge.declaredClaims.count)}{/if}</span></summary>
                  <ul>
                    {#each selected.edge.declaredClaims.items.slice(0, claimsShown) as c, i (i)}
                      <!-- Through the shared abbreviator, like every other surface that
                           prints a canonical revision key. A 64-hex digest has no break
                           opportunity, so printed whole it also sets this panel's
                           minimum width and pushes the panel off the screen. -->
                      <li title={c.sourceRevision || undefined}>{abbreviateDigests(c.sourceRevision) || 'a revision'}{#if c.compatibility === 'unknown'}{' · '}{RANGE_NOT_DECLARED}{:else if c.compatibility}{' · '}<code>{c.compatibility}</code>{/if}{#if c.reconciliation}{' · '}{c.reconciliation}{/if}</li>
                    {/each}
                  </ul>
                  {#if selected.edge.declaredClaims.items.length > claimsShown}
                    <button type="button" class="btn btn-sm" onclick={() => { claimsShown = Infinity; }}>Show all {formatCount(selected.edge.declaredClaims.items.length)}</button>
                  {/if}
                </details>
              {/if}
              {#if selected.edge.observed}
                <p class="gv-drow"><span class="gv-k">Observed</span> {selected.edge.count || 0} calls{#if selected.edge.observationSources?.total} across {formatCount(selected.edge.observationSources.total)} source(s){/if}{#if selected.edge.stale}{' · '}stale window{/if}</p>
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
  .gv-ctl-k { color: var(--c-text-3); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); letter-spacing: var(--role-label-tracking); }
  .gv-seg { display: inline-flex; border: 1px solid var(--c-border); border-radius: var(--radius-sm); overflow: hidden; }
  .gv-seg button { padding: 6px 10px; border: none; background: var(--c-bg); color: var(--c-text-2); font: inherit; font-size: var(--text-sm); cursor: pointer; }
  .gv-one { font-size: var(--text-sm); color: var(--c-text-2); padding: 6px 0; }
  .gv-seg button.active { background: var(--c-accent); color: var(--c-on-accent); }
  .gv-seg button:disabled { opacity: 0.5; cursor: not-allowed; }
  .gv-depth { align-items: center; }
  .gv-depth span { padding: 0 var(--sp-3); }
  .gv-link { color: var(--c-accent); text-decoration: none; font-size: var(--text-sm); }
  .gv-link:hover { text-decoration: underline; }
  .gv-caveat { padding: var(--sp-2) var(--sp-3); border-radius: var(--radius-sm); font-size: var(--text-sm); background: var(--c-warn-bg); border: 1px solid var(--c-warn-border); }
  /* The way out of a truncated answer sits inside the sentence that reports it, so
     the reader never has to go looking for the control that fixes what they just read. */
  .gv-more { border: 1px solid var(--c-warn-border); border-radius: var(--radius-xs); background: none; padding: 1px 8px; margin-left: 4px; font: inherit; color: inherit; cursor: pointer; transition: background var(--motion-feedback); }
  .gv-more:hover { background: var(--c-surface); }
  .gv-status { color: var(--c-text-3); }
  .gv-error-state { display: flex; flex-direction: column; align-items: center; gap: var(--sp-3); padding: var(--sp-8) var(--sp-4); text-align: center; }
  .gv-error-state h2 { margin: 0; color: var(--c-text-2); }
  .gv-error { padding: var(--sp-3); border-radius: var(--radius-sm); background: var(--c-err-bg); border: 1px solid var(--c-err); color: var(--c-text); }

  .gv-body { display: grid; grid-template-columns: 1fr; gap: var(--sp-4); }
  .gv-main { display: flex; flex-direction: column; gap: var(--sp-4); }

  .gv-answer { margin: var(--sp-3) 0; font-size: var(--text-md); color: var(--c-text-2); }
  .gv-answer.has-problems .gv-around { color: var(--c-err); font-weight: 600; }
  .gv-focus-err { color: var(--c-err); }
  .lg-hint { margin-left: auto; }
  /* One line until opened; the pieces inside lose their own boxes. */
  .gv-notice { padding: 0 var(--sp-3); border-radius: var(--radius-sm); font-size: var(--text-sm); background: var(--c-warn-bg); border: 1px solid var(--c-warn-border); }
  .gv-notice > summary { color: var(--c-text-2); }
  .gv-notice-body { display: flex; flex-direction: column; gap: var(--sp-2); padding-bottom: var(--sp-3); }
  .gv-notice-body .gv-caveat, .gv-notice-body :global(.knowledge) { padding: 0; background: none; border: 0; }
  .gv-fold-title { display: inline; }
  .gv-chip { display: inline-flex; align-items: center; padding: 0 8px; border: 1px solid var(--c-border); border-radius: var(--radius-pill); font-size: var(--text-xs); color: var(--c-text-2); background: var(--c-bg); }
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
  .lg-team { border-radius: 3px; border-style: double; border-width: 3px; }
  .lg-target { border-radius: 50%; }
  /* The canvas's dashed hexagon, drawn as a mask so the dashes follow its six sides. */
  .lg-external {
    border: none; background: var(--c-text-3);
    mask: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 16 12'%3E%3Cpath d='M4 1 H12 L15 6 L12 11 H4 L1 6 Z' fill='none' stroke='%23000' stroke-width='1.5' stroke-dasharray='2 1.5'/%3E%3C/svg%3E") center / contain no-repeat;
  }
  .lg-group { color: var(--c-text-3); margin-left: var(--sp-2); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); letter-spacing: var(--role-label-tracking); }
  .lg-group:first-child { margin-left: 0; }
  .lg-edge { width: 22px; height: 0; border-top: 2px solid var(--c-text-3); }
  /* Line-swatches mirror the exact canvas edge styles (lib/graph.ts cyStylesheet):
     relation by line-style, reconciliation state by tone + width, so each legend
     entry corresponds to something the canvas actually renders. */
  .lg-dep { border-top-style: solid; }
  .lg-runs { border-top-style: dashed; border-top-color: var(--c-info); }
  .lg-ref { border-top-style: dashed; }
  .lg-matched { border-top-color: var(--c-ok); }
  .lg-eno { border-top-style: dashed; border-top-color: var(--c-info); }
  .lg-drift { border-top-width: 3px; border-top-color: var(--c-warn); }
  .lg-insufficient { border-top-style: dotted; border-top-color: var(--c-neutral); }
  .lg-inferred { border-top-style: dashed; }
  .lg-refused { border-top-width: 3px; border-top-color: var(--c-err); }
  .gv-behavior { display: flex; flex-wrap: wrap; align-items: center; gap: var(--sp-1) var(--sp-2); }

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
  .gv-edge-behavior { display: flex; flex-wrap: wrap; gap: var(--sp-1); margin: var(--sp-1) 0 0 var(--sp-3); }
  .gv-rel { color: var(--c-text-3); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); letter-spacing: var(--role-label-tracking); }
  .gv-diff { margin-left: auto; }
  /* An end's name (an inferred workload key) holds no break point: let it break anywhere. */
  .gv-endpoint, .gv-ddesc { min-width: 0; overflow-wrap: anywhere; }
  .gv-unresolved, .gv-limitations { border: 1px solid var(--c-border); border-radius: var(--radius-md); padding: var(--sp-3); background: var(--c-surface); }
  /* These were h3s forced back up to section size with a hard-coded font-size -- a
     section title dressed as a subsection, then un-dressed by hand. They sit directly
     under the page h1, so h2 is both the honest outline level and, via base.css, the
     section role. No override left to drift. */
  .gv-unresolved[open] > summary, .gv-limitations[open] > summary { margin-bottom: var(--sp-2); }
  .gv-unresolved :global(ul) { margin: 0; padding-left: var(--sp-4); font-size: var(--text-sm); color: var(--c-text-2); display: flex; flex-direction: column; gap: 4px; }

  /* A bottom sheet on a narrow screen, so opening it never scrolls the page to where a
     one-column layout put it; beside the canvas and sticky on a wide one. */
  .gv-drawer {
    border: 1px solid var(--c-border); border-radius: var(--radius-md) var(--radius-md) 0 0; padding: var(--sp-4); background: var(--c-surface);
    display: flex; flex-direction: column; gap: var(--sp-2);
    position: fixed; left: 0; right: 0; bottom: 0; z-index: 50; max-height: 60vh; overflow: auto; box-shadow: var(--shadow-md);
  }
  .gv-drawer-head { display: flex; align-items: center; justify-content: space-between; }
  .gv-drawer-head h2 { margin: 0; }
  .gv-close { background: none; border: none; color: var(--c-text-3); cursor: pointer; font-size: var(--text-md); }
  .gv-drow { margin: 0; font-size: var(--text-sm); color: var(--c-text-2); display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .gv-k { color: var(--c-text-3); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); letter-spacing: var(--role-label-tracking); }
  .gv-ddesc { margin: 0; font-size: var(--text-sm); color: var(--c-text-3); }
  .gv-claims ul { margin: 4px 0 0; padding-left: var(--sp-4); font-size: var(--text-sm); color: var(--c-text-2); }
  .gv-drawer-actions { display: flex; gap: var(--sp-3); flex-wrap: wrap; margin-top: var(--sp-2); }

  @media (min-width: 900px) {
    /* minmax(0, ...) rather than a bare 1fr: a grid item's automatic minimum is its
       content, so one wide child is enough to stop the column shrinking for the drawer. */
    .gv-body.gv-body-drawer { grid-template-columns: minmax(0, 1fr) minmax(280px, 360px); align-items: start; }
    .gv-drawer {
      position: sticky; top: calc(var(--navbar-h) + var(--sp-3)); left: auto; right: auto; bottom: auto; z-index: auto;
      max-height: calc(100vh - var(--navbar-h) - 2 * var(--sp-3)); border-radius: var(--radius-md); box-shadow: none;
    }
  }
</style>
