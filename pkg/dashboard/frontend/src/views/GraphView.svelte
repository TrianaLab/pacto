<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import {
    graphStateFromParams, hasFocus, toggleView, differenceLabel, differenceTone,
    differenceDescription, relationLabel, neighborhoodIsEmpty, GRAPH_PERSPECTIVES, MAX_DEPTH,
  } from '../lib/graphState.ts';
  import { snapshotKnowledge } from '../lib/knowledgeState.ts';
  import { knowledgeLabel, knowledgeTone } from '../lib/entityLabels.ts';
  import { fleetGraphFocusUrl, fleetGraphDiscoveryUrl, fleetOverviewUrl, fleetAttentionUrl, hashForHref, fleetImpactUrl } from '../lib/router.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import EntityLink from '../components/EntityLink.svelte';
  import EntityIdentity from '../components/EntityIdentity.svelte';
  import IdentityBadge from '../components/IdentityBadge.svelte';
  import LimitationsList from '../components/LimitationsList.svelte';

  // The search-first product Operational Graph (Phase 4). With no focus it shows a
  // DISCOVERY state (search + attention entry points + an explanation) -- never a
  // whole-fleet hairball. With a focus it consumes the PRODUCT neighborhood API (never
  // the FleetSnapshot) for a bounded local neighborhood: expected / observed /
  // differences views, a projection perspective (service / revision / target), a
  // direction and a depth, all shareable in the URL. Selecting a node or edge opens a
  // bounded quick-inspection drawer; the full stable entity page stays the durable
  // destination.
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
  const loading = $derived(focused && loader.loading);
  const error = $derived(focused ? loader.error : null);
  const knowledge = $derived(snapshotKnowledge(nb?.meta));
  const selected = $state({ kind: '', data: null }); // { kind: 'node'|'edge', data }

  function selectNode(node) { if (node) { selected.kind = 'node'; selected.data = node; } }
  function selectEdge(edge) { selected.kind = 'edge'; selected.data = edge; }
  function closeDrawer() { selected.kind = ''; selected.data = null; }
  // nodeFor finds the neighborhood node for a key (used to open the focus node drawer).
  function nodeFor(key) { return (nb?.nodes || []).find((n) => n.ref.key === key) || null; }

  // ── control -> URL (shareable graph state) ───────────────────────────────────
  function go(patch) {
    const next = { perspective: gs.perspective, views: gs.views, direction: gs.direction, depth: gs.depth, ...patch };
    location.hash = fleetGraphFocusUrl(gs.kind, gs.key, next);
    closeDrawer();
  }
  function setPerspective(p) { go({ perspective: p }); }
  function setDirection(d) { go({ direction: d }); }
  function setDepth(d) { go({ depth: Math.max(1, Math.min(MAX_DEPTH, d)) }); }
  function flipView(v) { go({ views: toggleView(gs.views, v) }); }
  function expand() { go({ depth: Math.min(MAX_DEPTH, gs.depth + 1) }); } // backend re-merges the larger bounded neighborhood
  function resetFocus() { location.hash = fleetGraphDiscoveryUrl(); }

  const VIEW_DEFS = [
    { v: 'expected', label: 'Expected', help: 'Contract-declared relationships (intent).' },
    { v: 'observed', label: 'Observed', help: 'Relationships backed by runtime observation.' },
    { v: 'differences', label: 'Differences', help: 'Where declared intent and observed reality diverge.' },
  ];

  // ── discovery search (search-first entry) ────────────────────────────────────
  let queryText = $state('');
  let results = $state([]);
  let searching = $state(false);
  let searchSeq = 0;
  function runSearch() {
    const q = queryText.trim();
    const mySeq = ++searchSeq;
    if (!q) { results = []; searching = false; return; }
    searching = true;
    api.fleetEntities({ text: q, limit: 20 })
      .then((r) => { if (mySeq === searchSeq) results = r.entities || []; })
      .catch(() => { if (mySeq === searchSeq) results = []; })
      .finally(() => { if (mySeq === searchSeq) searching = false; });
  }
  function submitSearch(e) { e.preventDefault(); runSearch(); }
  onDestroy(() => { searchSeq++; });

  const focusRef = $derived(nb?.requestedFocus ?? null);
</script>

<div class="graph-view">
  <Breadcrumbs trail={focused
    ? [{ label: 'Fleet', href: fleetOverviewUrl() }, { label: 'Operational graph', href: fleetGraphDiscoveryUrl() }, { label: focusRef?.label || gs.key }]
    : [{ label: 'Fleet', href: fleetOverviewUrl() }, { label: 'Operational graph' }]} />

  {#if !focused}
    <!-- Discovery state (requirement K): search-first, no fleet hairball. -->
    <section class="discovery" data-testid="graph-discovery">
      <h1>Operational graph</h1>
      <p class="disco-lead">Search for a service, revision or deployment to see its local operational neighborhood. The graph never opens the whole fleet at once.</p>

      <form class="disco-search" role="search" onsubmit={submitSearch}>
        <input type="search" bind:value={queryText} oninput={runSearch} placeholder="Search services, revisions, deployments..." aria-label="Search the fleet to focus the graph" />
        <button type="submit" class="gv-btn">Search</button>
      </form>

      {#if searching}
        <p class="disco-hint">Searching...</p>
      {:else if results.length > 0}
        <ul class="disco-results" data-testid="graph-search-results">
          {#each results as r (r.kind + '::' + r.key)}
            <li>
              <a href={fleetGraphFocusUrl(r.kind, r.key)} data-testid="graph-focus-link">
                <EntityIdentity ref={r} />
              </a>
            </li>
          {/each}
        </ul>
      {:else if queryText.trim()}
        <p class="disco-hint">No entities match "{queryText}".</p>
      {/if}

      <div class="disco-panels">
        <div class="disco-panel">
          <h2>Start from what needs attention</h2>
          <p>Jump into the graph from an operational problem.</p>
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
    <!-- Focused neighborhood (requirements L/M/O). -->
    <div class="gv-head">
      <h1>Operational graph</h1>
      {#if focusRef}<EntityIdentity ref={focusRef} />{/if}
      <button type="button" class="gv-btn gv-reset" onclick={resetFocus} data-testid="graph-reset">Reset focus</button>
    </div>

    <div class="gv-toolbar" role="group" aria-label="Graph controls">
      <div class="gv-ctl">
        <span class="gv-ctl-k">Perspective</span>
        <div class="gv-seg">
          {#each GRAPH_PERSPECTIVES as p}
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
      <div class="gv-ctl">
        <span class="gv-ctl-k">Depth</span>
        <div class="gv-seg gv-depth">
          <button type="button" onclick={() => setDepth(gs.depth - 1)} disabled={gs.depth <= 1} aria-label="Decrease depth">-</button>
          <span data-testid="graph-depth">{gs.depth}</span>
          <button type="button" onclick={() => setDepth(gs.depth + 1)} disabled={gs.depth >= MAX_DEPTH} aria-label="Increase depth">+</button>
        </div>
      </div>
      <button type="button" class="gv-btn" onclick={expand} disabled={gs.depth >= MAX_DEPTH} data-testid="graph-expand">Expand</button>
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

      <div class="gv-body">
        <div class="gv-canvas" data-testid="graph-canvas">
          <!-- Focus node -->
          <div class="gv-focus">
            <button type="button" class="gv-node is-focus" onclick={() => selectNode(nodeFor(focusRef?.key))} data-testid="graph-focus-node">
              <EntityIdentity ref={focusRef} />
            </button>
          </div>

          {#if neighborhoodIsEmpty(nb)}
            <p class="gv-status">No {gs.direction === 'both' ? 'related entities' : gs.direction} are known for this focus under the selected views.</p>
          {:else}
            <!-- Edges (backend-authoritative difference rendered verbatim) -->
            <ul class="gv-edges" data-testid="graph-edges">
              {#each nb.edges as e (e.id)}
                <li>
                  <button type="button" class="gv-edge" onclick={() => selectEdge(e)} data-testid="graph-edge">
                    <span class="gv-endpoint"><EntityLink ref={e.from} showStatus={false} /></span>
                    <span class="gv-rel" data-testid="edge-relation">{relationLabel(e.relation)}</span>
                    <span class="gv-endpoint"><EntityLink ref={e.to} showStatus={false} /></span>
                    {#if e.relation !== 'runs' && e.difference}
                      <span class="gv-diff" data-testid="edge-difference"><IdentityBadge label={differenceLabel(e.difference)} tone={differenceTone(e.difference)} title={differenceDescription(e.difference)} /></span>
                    {/if}
                  </button>
                </li>
              {/each}
            </ul>
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

        <!-- Quick-inspection drawer (requirement P) -->
        {#if selected.kind === 'node' && selected.data}
          <aside class="gv-drawer" data-testid="graph-drawer" aria-label="Node details">
            <div class="gv-drawer-head"><h2>Entity</h2><button type="button" class="gv-close" onclick={closeDrawer} aria-label="Close">x</button></div>
            <EntityIdentity ref={selected.data.ref} />
            {#if selected.data.status}<p class="gv-drow"><span class="gv-k">Status</span> {selected.data.status}</p>{/if}
            {#if selected.data.owner}<p class="gv-drow"><span class="gv-k">Owner</span> {selected.data.owner}</p>{/if}
            {#if knowledge.incomplete}<p class="gv-caveat-inline">{knowledgeLabel(knowledge.level)}: this view may be incomplete.</p>{/if}
            <div class="gv-drawer-actions">
              <a class="gv-link" href={hashForHref(selected.data.ref.href)}>Open full detail &rarr;</a>
              <a class="gv-link" href={fleetGraphFocusUrl(selected.data.ref.kind, selected.data.ref.key, { perspective: gs.perspective, views: gs.views, direction: gs.direction, depth: gs.depth })} data-testid="drawer-focus-here">Focus here</a>
              {#if selected.data.ref.kind === 'service'}<a class="gv-link" href={fleetImpactUrl(selected.data.ref.key)}>Analyze impact &rarr;</a>{/if}
            </div>
          </aside>
        {:else if selected.kind === 'edge' && selected.data}
          <aside class="gv-drawer" data-testid="graph-drawer" aria-label="Relationship details">
            <div class="gv-drawer-head"><h2>Relationship</h2><button type="button" class="gv-close" onclick={closeDrawer} aria-label="Close">x</button></div>
            <p class="gv-drow"><EntityLink ref={selected.data.from} showStatus={false} /> <strong>{relationLabel(selected.data.relation)}</strong> <EntityLink ref={selected.data.to} showStatus={false} /></p>
            {#if selected.data.relation !== 'runs'}
              <p class="gv-drow"><span class="gv-k">Difference</span> <IdentityBadge label={differenceLabel(selected.data.difference)} tone={differenceTone(selected.data.difference)} /></p>
              <p class="gv-ddesc">{differenceDescription(selected.data.difference)}</p>
              {#if (selected.data.declaredClaims?.count ?? 0) > 0}
                <div class="gv-claims">
                  <span class="gv-k">Declared by</span>
                  <ul>
                    {#each selected.data.declaredClaims.items as c, i (i)}
                      <li>{c.sourceRevision || 'a revision'}{#if c.compatibility} &middot; <code>{c.compatibility}</code>{/if}{#if c.reconciliation} &middot; {c.reconciliation}{/if}</li>
                    {/each}
                  </ul>
                </div>
              {/if}
              {#if selected.data.observed}
                <p class="gv-drow"><span class="gv-k">Observed</span> {selected.data.count || 0} calls{#if selected.data.observationSources?.total} across {selected.data.observationSources.total} source(s){/if}{#if selected.data.stale} &middot; stale window{/if}</p>
              {/if}
            {:else}
              <p class="gv-ddesc">This deployment runs the linked revision. It is an observed link, not a declared dependency.</p>
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
  .gv-ctl-k { font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.04em; color: var(--c-text-3); }
  .gv-seg { display: inline-flex; border: 1px solid var(--c-border); border-radius: var(--radius-sm); overflow: hidden; }
  .gv-seg button { padding: 6px 10px; border: none; background: var(--c-bg); color: var(--c-text-2); font: inherit; font-size: var(--text-sm); cursor: pointer; text-transform: capitalize; }
  .gv-seg button.active { background: var(--c-accent); color: #fff; }
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
  .gv-canvas { display: flex; flex-direction: column; gap: var(--sp-4); }
  .gv-focus { display: flex; }
  .gv-node { border: 2px solid var(--c-accent); border-radius: var(--radius-md); padding: var(--sp-3); background: var(--c-surface); cursor: pointer; text-align: left; }
  .gv-edges { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .gv-edge { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; width: 100%; text-align: left; padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); cursor: pointer; }
  .gv-edge:hover { border-color: var(--c-accent); }
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
