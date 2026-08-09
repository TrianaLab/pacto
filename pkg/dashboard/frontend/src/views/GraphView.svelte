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
  import { snapshotKnowledge } from '../lib/knowledgeState.ts';
  import { knowledgeLabel, knowledgeTone } from '../lib/entityLabels.ts';
  import { fleetGraphFocusUrl, fleetGraphDiscoveryUrl, fleetOverviewUrl, fleetAttentionUrl, hashForHref, fleetChangesUrl, replaceHash } from '../lib/router.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import EntityLink from '../components/EntityLink.svelte';
  import EntityIdentity from '../components/EntityIdentity.svelte';
  import IdentityBadge from '../components/IdentityBadge.svelte';
  import LimitationsList from '../components/LimitationsList.svelte';
  import NeighborhoodGraph from '../NeighborhoodGraph.svelte';

  // The product Operational Graph (Phase 4). With no focus it shows a DISCOVERY state
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
      loader.sync(`${gs.kind}@@${gs.key}@@${gs.perspective}@@${gs.direction}@@${gs.depth}@@${gs.views.join(',')}@@${refreshTick}`);
    }
  });
  onDestroy(() => loader.destroy());

  const nb = $derived(focused ? loader.data : null);

  // Canonicalize an old deep link whose focus the backend REPLACED (a bookmarked target
  // URL under the revision perspective resolves to the linked revision). RequestedFocus
  // stays truthful; the backend supplies an explicit projectionFocus, and we replace
  // (not push) the URL to it so a reload stays on the canonical Product URL and the
  // active perspective never contradicts the visible graph (requirement, Part 4).
  $effect(() => {
    if (!nb) return;
    const canon = projectionFocusMismatch(nb, gs.kind, gs.key);
    if (canon) {
      replaceHash(fleetGraphFocusUrl(canon.kind, canon.key, {
        perspective: gs.perspective, views: gs.views, direction: gs.direction, depth: gs.depth,
      }));
    }
  });
  const loading = $derived(focused && loader.loading);
  const error = $derived(focused ? loader.error : null);
  const knowledge = $derived(snapshotKnowledge(nb?.meta));
  const focusRef = $derived(nb?.requestedFocus ?? null);
  const focusNode = $derived((nb?.nodes || []).find((n) => n.focus) || null);

  // Perspective options valid for THIS focus (requirement E): a service can only be a
  // service projection; a target is a revision projection only when its link is
  // authoritative. Ordinary navigation therefore never produces a backend 422.
  const perspectives = $derived(availablePerspectives(gs.kind, {
    targetRevisionAuthoritative: revisionLinkAuthoritative(focusNode?.revisionState),
  }));
  const depthSupported = $derived(perspectiveSupportsDepth(gs.perspective));
  const oneHopNote = $derived(nb && nb.effectiveDepth < gs.depth);

  // ── quick-inspection drawer (node or edge) ───────────────────────────────────
  // The drawer is a NON-modal side panel (requirement 8.3): it is not focus-trapped;
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
  function selectNode(node) { if (node) { rememberOpener(); selected.kind = 'node'; selected.node = node; selected.edge = null; } }
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
  function selectNodeById(id) { selectNode((nb?.nodes || []).find((n) => n.ref.key === id)); }
  function selectEdgeByCyId(cyId) {
    selectEdge((nb?.edges || []).find((e) => cyEdgeId(e.from.key, e.to.key, e.relation) === cyId));
  }

  // ── graph controls (ephemeral fit/zoom, wired from the canvas) ───────────────
  let controls = $state(null);
  function onControls(c) { controls = c; }

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
    // URL and the visible graph never disagree (requirement, Part 4). Keys come from the
    // backend neighborhood data (focusService / the runs edge), never inferred. A push
    // (not replace) so Back returns to the previous canonical route.
    const canon = canonicalFocusForPerspective(nb, gs.kind, p);
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

<div class="graph-view">
  <Breadcrumbs trail={focused
    ? [{ label: 'Overview', href: fleetOverviewUrl() }, { label: 'Operational graph', href: fleetGraphDiscoveryUrl() }, { label: focusRef?.label || gs.key }]
    : [{ label: 'Overview', href: fleetOverviewUrl() }, { label: 'Operational graph' }]} />

  {#if !focused}
    <!-- Discovery state (requirement K): search-first, no fleet hairball, no request. -->
    <section class="discovery" data-testid="graph-discovery">
      <h1>Operational graph</h1>
      <p class="disco-lead">The operational graph is search-first: pick one entity and see <strong>its</strong> local neighborhood render below. It never opens the whole fleet at once.</p>

      <form class="disco-search" role="search" onsubmit={submitSearch}>
        <input type="search" bind:value={queryText} oninput={runSearch} placeholder="Search services, revisions, operational targets..." aria-label="Search for a service, revision or target to focus the graph" />
        <button type="submit" class="gv-btn gv-btn-primary">Search</button>
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
        <!-- Resting discovery state (requirement, reopen section 4): an unmistakable
             affordance that a graph renders HERE once a focus is chosen -- so the tab reads
             as a graph discovery experience, not an empty page -- without auto-rendering the
             whole fleet. -->
        <div class="disco-placeholder" data-testid="graph-discovery-placeholder" aria-hidden="true">
          <svg viewBox="0 0 120 64" class="disco-ph-glyph" role="presentation" focusable="false">
            <line x1="30" y1="32" x2="66" y2="18" /><line x1="30" y1="32" x2="66" y2="46" /><line x1="66" y1="18" x2="96" y2="32" />
            <circle cx="30" cy="32" r="9" /><circle cx="66" cy="18" r="7" /><circle cx="66" cy="46" r="7" /><circle cx="96" cy="32" r="7" />
          </svg>
          <p class="disco-ph-title">Select a service, revision or operational target to render its local graph</p>
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
    <!-- Focused neighborhood: an actual visual topology (requirements F/G/H). -->
    <div class="gv-head">
      <h1>Operational graph</h1>
      {#if focusRef}<EntityIdentity ref={focusRef} />{/if}
      <button type="button" class="gv-btn gv-reset" onclick={resetFocus} data-testid="graph-reset">Reset focus</button>
    </div>

    <div class="gv-toolbar" role="group" aria-label="Graph controls">
      <div class="gv-ctl">
        <span class="gv-ctl-k">Perspective</span>
        <div class="gv-seg">
          {#each perspectives as p}
            <button type="button" class:active={gs.perspective === p} onclick={() => setPerspective(p)} data-testid="perspective-{p}">{p}</button>
          {/each}
        </div>
      </div>
      <div class="gv-ctl">
        <span class="gv-ctl-k">Knowledge</span>
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
        <button type="button" class="gv-btn" onclick={expand} disabled={gs.depth >= MAX_DEPTH} data-testid="graph-expand">Expand</button>
      {/if}
      <div class="gv-ctl gv-viewctl">
        <span class="gv-ctl-k">View</span>
        <div class="gv-seg">
          <button type="button" onclick={() => controls?.fit()} data-testid="graph-fit" aria-label="Fit graph to view">Fit</button>
          <button type="button" onclick={() => controls?.zoomIn()} data-testid="graph-zoom-in" aria-label="Zoom in">+</button>
          <button type="button" onclick={() => controls?.zoomOut()} data-testid="graph-zoom-out" aria-label="Zoom out">-</button>
        </div>
      </div>
    </div>

    {#if loading}
      <p class="gv-status" role="status">Loading the neighborhood...</p>
    {:else if error}
      <div class="gv-error" role="alert">Couldn't load the neighborhood: {error instanceof Error ? error.message : String(error)}</div>
    {:else if nb}
      {#if knowledge.incomplete}
        <div class="gv-caveat tone-{knowledgeTone(knowledge.level)}" role="status" data-testid="graph-knowledge-caveat">
          {knowledgeLabel(knowledge.level)} - this neighborhood may be incomplete.
        </div>
      {/if}
      {#if nb.truncated}
        <div class="gv-caveat tone-warn" role="status" data-testid="graph-truncated">
          This neighborhood is bounded and was truncated. Narrow the direction or depth, or open an entity to continue.
        </div>
      {/if}
      {#if oneHopNote}
        <div class="gv-caveat tone-info" role="status" data-testid="graph-effective-depth">
          The {gs.perspective} projection is evaluated one hop deep; deeper exploration lives in the revision projection.
        </div>
      {/if}

      <div class="gv-body">
        <div class="gv-main">
          {#if neighborhoodIsEmpty(nb)}
            <p class="gv-status" data-testid="graph-empty">No {gs.direction === 'both' ? 'related entities' : gs.direction} are known for this focus under the selected views.</p>
          {:else}
            <!-- Primary: the visual Cytoscape topology (requirement F). -->
            <NeighborhoodGraph neighborhood={nb} focusKey={focusRef?.key || ''} onSelectNode={selectNodeById} onSelectEdge={selectEdgeByCyId} oncontrols={onControls} />

            <!-- Legend (requirement G/Part 6): every item is a REAL canvas distinction.
                 Node kinds are shapes/borders; edge relation and reconciliation state are
                 line-swatches that mirror exactly what the canvas draws (line style +
                 width + tone), never a decorative badge for a state the canvas can't show.
                 The drawer/text list keeps the precise, scoped wording. -->
            <div class="gv-legend" data-testid="graph-legend" aria-label="Graph legend">
              <span class="lg-group">Nodes</span>
              <span class="lg-item"><span class="lg-node lg-service"></span> Service</span>
              <span class="lg-item"><span class="lg-node lg-revision"></span> Revision</span>
              <span class="lg-item"><span class="lg-node lg-target"></span> Operational target</span>
              <span class="lg-group">Relationship</span>
              <span class="lg-item"><span class="lg-edge lg-dep"></span> Depends on</span>
              <span class="lg-item"><span class="lg-edge lg-runs"></span> Runs</span>
              <span class="lg-group">Reconciliation</span>
              <span class="lg-item"><span class="lg-edge lg-matched"></span> Matched / corroborated</span>
              <span class="lg-item"><span class="lg-edge lg-eno"></span> Expected, not observed</span>
              <span class="lg-item"><span class="lg-edge lg-drift"></span> Observed, not expected</span>
              <span class="lg-item"><span class="lg-edge lg-insufficient"></span> Insufficient evidence</span>
            </div>

            <!-- Accessible text alternative (requirement P): the same nodes and edges as
                 a semantic list, keyboard-focusable, driving the same drawer. -->
            <details class="gv-textalt" data-testid="graph-textalt">
              <summary>Relationships (text)</summary>
              <ul class="gv-nodes" aria-label="Graph nodes">
                {#each nb.nodes as n (n.ref.key)}
                  <li>
                    <button type="button" class="gv-textbtn" class:is-focus={n.focus} onclick={() => selectNode(n)} data-testid="graph-node-item">
                      <EntityIdentity ref={n.ref} />
                    </button>
                  </li>
                {/each}
              </ul>
              <ul class="gv-edges" aria-label="Graph relationships" data-testid="graph-edges">
                {#each nb.edges as e (e.id)}
                  <li>
                    <button type="button" class="gv-edge" onclick={() => selectEdge(e)} data-testid="graph-edge">
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

          {#if (nb.unresolvedDependencies?.count ?? 0) > 0}
            <section class="gv-unresolved" data-testid="graph-unresolved">
              <h3>Unresolved dependencies ({nb.unresolvedDependencies.total})</h3>
              <ul>
                {#each nb.unresolvedDependencies.items as u (u.from.key + '::' + u.ref)}
                  <li><EntityLink ref={u.from} showStatus={false} /> declares <code>{u.requestedRef || u.ref}</code> - no provider resolves it{#if u.reason}: {u.reason}{/if}</li>
                {/each}
              </ul>
            </section>
          {/if}

          {#if (nb.limitations?.count ?? 0) > 0}
            <section class="gv-limitations" data-testid="graph-limitations">
              <h3>Limitations</h3>
              <LimitationsList items={nb.limitations.items} />
            </section>
          {/if}
        </div>

        <!-- Quick-inspection drawer (requirement H). -->
        {#if selected.kind === 'node' && selected.node}
          <aside class="gv-drawer" data-testid="graph-drawer" aria-label="Node details" tabindex="-1" bind:this={drawerEl}>
            <div class="gv-drawer-head"><h2>Entity</h2><button type="button" class="gv-close" onclick={closeDrawer} aria-label="Close">x</button></div>
            <EntityIdentity ref={selected.node.ref} />
            {#if selected.node.status}<p class="gv-drow"><span class="gv-k">Status</span> {selected.node.status}</p>{/if}
            {#if selected.node.owner}<p class="gv-drow"><span class="gv-k">Owner</span> {selected.node.owner}</p>{/if}
            {#if selected.node.revisionState}<p class="gv-drow"><span class="gv-k">Revision link</span> {selected.node.revisionState}</p>{/if}
            {#if knowledge.incomplete}<p class="gv-caveat-inline">{knowledgeLabel(knowledge.level)}: this view may be incomplete.</p>{/if}
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
                      <li>{c.sourceRevision || 'a revision'}{#if c.compatibility} &middot; <code>{c.compatibility}</code>{/if}{#if c.reconciliation} &middot; {c.reconciliation}{/if}</li>
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
  .graph-view { display: flex; flex-direction: column; gap: var(--sp-4); }
  .discovery { display: flex; flex-direction: column; gap: var(--sp-4); max-width: 820px; }
  .disco-lead { color: var(--c-text-2); }
  .disco-search { display: flex; gap: var(--sp-2); max-width: 520px; }
  .disco-search input { flex: 1; padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); color: var(--c-text); font: inherit; font-size: var(--text-sm); min-height: var(--touch-min); }
  .disco-results { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .disco-results a { display: block; padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); text-decoration: none; }
  .disco-results a:hover { border-color: var(--c-accent); }
  .disco-hint { color: var(--c-text-3); font-size: var(--text-sm); }
  .gv-btn-primary { background: var(--c-accent); color: var(--c-on-accent); border-color: var(--c-accent); }
  .gv-btn-primary:hover { filter: brightness(1.08); border-color: var(--c-accent); }
  .disco-placeholder {
    display: flex; flex-direction: column; align-items: center; gap: var(--sp-2);
    text-align: center; padding: var(--sp-6) var(--sp-4);
    border: 2px dashed var(--c-border); border-radius: var(--radius-md);
    background: var(--c-surface-inset);
  }
  .disco-ph-glyph { width: 120px; height: 64px; }
  .disco-ph-glyph line { stroke: var(--c-text-3); stroke-width: 2; }
  .disco-ph-glyph circle { fill: var(--c-surface); stroke: var(--c-accent); stroke-width: 2; }
  .disco-ph-title { margin: var(--sp-2) 0 0; font-size: var(--text-md); font-weight: 600; color: var(--c-text); }
  .disco-ph-sub { margin: 0; font-size: var(--text-sm); color: var(--c-text-2); max-width: 44ch; }
  .disco-panels { display: grid; grid-template-columns: repeat(auto-fit, minmax(260px, 1fr)); gap: var(--sp-3); margin-top: var(--sp-2); }
  .disco-panel { border: 1px solid var(--c-border); border-radius: var(--radius-md); padding: var(--sp-4); background: var(--c-surface); }
  .disco-panel h2 { margin: 0 0 var(--sp-2); font-size: var(--text-md); }
  .disco-legend { display: grid; grid-template-columns: auto 1fr; gap: 6px var(--sp-3); margin: var(--sp-2) 0 0; }
  .disco-legend dd { margin: 0; color: var(--c-text-3); font-size: var(--text-sm); }

  .gv-head { display: flex; align-items: center; gap: var(--sp-3); flex-wrap: wrap; }
  .gv-head h1 { margin: 0; }
  .gv-reset { margin-left: auto; }
  .gv-toolbar { display: flex; gap: var(--sp-4); flex-wrap: wrap; align-items: flex-end; padding: var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-md); background: var(--c-surface); }
  .gv-ctl { display: flex; flex-direction: column; gap: 4px; }
  .gv-viewctl { margin-left: auto; }
  .gv-ctl-k { font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.04em; color: var(--c-text-3); }
  .gv-seg { display: inline-flex; border: 1px solid var(--c-border); border-radius: var(--radius-sm); overflow: hidden; }
  .gv-seg button { padding: 6px 10px; border: none; background: var(--c-bg); color: var(--c-text-2); font: inherit; font-size: var(--text-sm); cursor: pointer; text-transform: capitalize; }
  .gv-seg button.active { background: var(--c-accent); color: var(--c-on-accent); }
  .gv-seg button:disabled { opacity: 0.5; cursor: not-allowed; }
  .gv-depth { align-items: center; }
  .gv-depth span { padding: 0 var(--sp-3); }
  .gv-btn { padding: var(--sp-2) var(--sp-4); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); color: var(--c-text); font: inherit; font-size: var(--text-sm); cursor: pointer; min-height: var(--touch-min); }
  .gv-btn:hover { border-color: var(--c-accent); }
  .gv-link { color: var(--c-accent); text-decoration: none; font-size: var(--text-sm); }
  .gv-link:hover { text-decoration: underline; }
  .gv-caveat { padding: var(--sp-2) var(--sp-3); border-radius: var(--radius-sm); font-size: var(--text-sm); background: var(--c-warn-bg); border: 1px solid var(--c-warn-border); }
  .gv-status { color: var(--c-text-3); }
  .gv-error { padding: var(--sp-3); border-radius: var(--radius-sm); background: var(--c-err-bg); border: 1px solid var(--c-err); color: var(--c-text); }

  .gv-body { display: grid; grid-template-columns: 1fr; gap: var(--sp-4); }
  .gv-main { display: flex; flex-direction: column; gap: var(--sp-4); }

  .gv-legend { display: flex; flex-wrap: wrap; gap: var(--sp-2) var(--sp-4); padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); font-size: var(--text-sm); color: var(--c-text-2); }
  .lg-item { display: inline-flex; align-items: center; gap: 6px; }
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
  .gv-textalt summary { cursor: pointer; font-size: var(--text-sm); color: var(--c-text-2); }
  .gv-nodes, .gv-edges { list-style: none; margin: var(--sp-2) 0 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .gv-nodes { flex-flow: row wrap; }
  .gv-textbtn { display: inline-flex; padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-bg); cursor: pointer; text-align: left; }
  .gv-textbtn.is-focus { border-color: var(--c-accent); border-width: 2px; }
  .gv-edge { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; width: 100%; text-align: left; padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-bg); cursor: pointer; }
  .gv-edge:hover, .gv-textbtn:hover { border-color: var(--c-accent); }
  .gv-rel { font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.03em; color: var(--c-text-3); }
  .gv-diff { margin-left: auto; }
  .gv-unresolved, .gv-limitations { border: 1px solid var(--c-border); border-radius: var(--radius-md); padding: var(--sp-3); background: var(--c-surface); }
  .gv-unresolved h3, .gv-limitations h3 { margin: 0 0 var(--sp-2); font-size: var(--text-md); }
  .gv-unresolved ul { margin: 0; padding-left: var(--sp-4); font-size: var(--text-sm); color: var(--c-text-2); display: flex; flex-direction: column; gap: 4px; }

  .gv-drawer { border: 1px solid var(--c-border); border-radius: var(--radius-md); padding: var(--sp-4); background: var(--c-surface); display: flex; flex-direction: column; gap: var(--sp-2); }
  .gv-drawer-head { display: flex; align-items: center; justify-content: space-between; }
  .gv-drawer-head h2 { margin: 0; font-size: var(--text-md); }
  .gv-close { background: none; border: none; color: var(--c-text-3); cursor: pointer; font-size: var(--text-md); }
  .gv-drow { margin: 0; font-size: var(--text-sm); color: var(--c-text-2); display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .gv-k { font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.04em; color: var(--c-text-3); }
  .gv-ddesc { margin: 0; font-size: var(--text-sm); color: var(--c-text-3); }
  .gv-claims ul { margin: 4px 0 0; padding-left: var(--sp-4); font-size: var(--text-sm); color: var(--c-text-2); }
  .gv-drawer-actions { display: flex; gap: var(--sp-3); flex-wrap: wrap; margin-top: var(--sp-2); }
  .gv-caveat-inline { font-size: var(--text-sm); color: var(--c-warn); margin: 0; }

  @media (min-width: 900px) {
    .gv-body { grid-template-columns: 1fr minmax(280px, 360px); align-items: start; }
  }
</style>
