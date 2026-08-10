<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { decideViewState, snapshotKnowledge } from '../lib/knowledgeState.ts';
  import { kindLabel } from '../lib/entityLabels.ts';
  import { fleetOverviewUrl, fleetGraphFocusUrl, fleetChangesUrl, hashForHref } from '../lib/router.ts';
  import { fleetEntityBreadcrumbs } from '../lib/breadcrumbs.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import KnowledgeBanner from '../components/KnowledgeBanner.svelte';
  import EntityIdentity from '../components/EntityIdentity.svelte';
  import CopyableIdentifier from '../components/CopyableIdentifier.svelte';
  import EntityStatusBadge from '../components/EntityStatusBadge.svelte';
  import ProductEmptyState from '../components/ProductEmptyState.svelte';
  import StaleRefreshNotice from '../components/StaleRefreshNotice.svelte';
  import ServiceEntity from './entity/ServiceEntity.svelte';
  import RevisionEntity from './entity/RevisionEntity.svelte';
  import TargetEntity from './entity/TargetEntity.svelte';
  import OwnerEntity from './entity/OwnerEntity.svelte';
  import SourceEntity from './entity/SourceEntity.svelte';

  // The unified entity route for every kind (requirements D/E/F/G). It resolves the
  // product entity-detail endpoint (NarrowedEntityDetail) -- never the raw snapshot --
  // and owns the shared shell: entity-relationship breadcrumbs, identity header,
  // canonical key, knowledge caveat and contextual actions. The kind-specific rich
  // body is delegated to the per-kind component.
  let { kind = '', entityKey = '', refreshTick = 0 } = $props();

  // Four lifecycle events, three behaviours -- the distinction this page used to lack.
  //
  //  * FIRST LOAD and a change of CANONICAL ENTITY IDENTITY are new questions. Nothing
  //    on hand answers them, so they show a loading state.
  //  * A BACKGROUND REFRESH of the SAME entity re-asks the SAME question. The last good
  //    answer stays rendered until a newer one lands (or fails), because tearing the
  //    body out every poll tick collapses the page and clamps the reader's scroll.
  //  * A RETRY after no usable data re-runs the request under a fresh generation.
  //
  // `identity` is the question; `refreshTick` only re-asks it. The loader keeps the
  // previous answer across a re-ask by design, and `dataTag` says which question the
  // answer on hand belongs to -- so data for an entity the user has navigated AWAY from
  // is never shown under the new entity's heading, even without a remount.
  const identity = $derived(`${kind}@@${entityKey}`);
  const loader = createProductLoader(() => api.fleetEntityDetail(kind, entityKey));
  $effect(() => { loader.sync(`${identity}@@${refreshTick}`, identity); });
  onDestroy(() => loader.destroy());
  function load() { loader.refresh(); }

  const detail = $derived(loader.dataTag === identity ? loader.data : null);
  const knowledge = $derived(snapshotKnowledge(detail?.meta));
  const state = $derived(decideViewState({
    loading: loader.loading, error: loader.error, itemCount: detail ? 1 : 0, knowledge,
  }));
  // A refresh that failed over a page we can still show. The page stays; the failure is
  // stated rather than swallowed, so nobody reads a frozen page as a current one.
  const refreshError = $derived(state.kind === 'ready' ? state.refreshError : null);

  // Entity-relationship breadcrumbs from canonical DTO refs (requirement H); a minimal
  // trail while loading/erroring.
  const trail = $derived(
    detail ? fleetEntityBreadcrumbs(detail) : [{ label: 'Overview', href: fleetOverviewUrl() }, { label: kindLabel(kind) }],
  );

  // The service this entity belongs to, for impact/compare actions.
  const svcKey = $derived(
    detail?.target?.service?.key || detail?.revision?.service?.key || (detail?.entity?.kind === 'service' ? detail?.entity?.key : ''),
  );

  // Map the DTO's route-neutral action ids to canonical destinations. 'compare' and
  // 'impact' are two stages of ONE question, so they resolve to the same Change analysis
  // workspace and are emitted ONCE -- an entity page that offered both used to show two
  // buttons that opened the identical screen. From a revision the workspace opens with
  // that revision preselected as the later side, so the action continues the workflow the
  // user is already in instead of restarting it.
  const actions = $derived.by(() => {
    if (!detail) return [];
    const e = detail.entity;
    const out = [];
    let changeAdded = false;
    for (const a of detail.actions || []) {
      if (a === 'open-graph') out.push({ label: 'Open in graph', href: fleetGraphFocusUrl(e.kind, e.key) });
      else if ((a === 'compare' || a === 'impact') && svcKey && !changeAdded) {
        changeAdded = true;
        const opts = e.kind === 'revision' ? { new: e.key } : {};
        out.push({ label: 'Compare revisions', href: fleetChangesUrl(svcKey, opts) });
      } else if (a === 'service' && detail.target?.service?.href) out.push({ label: 'Open service', href: hashForHref(detail.target.service.href) });
    }
    return out;
  });
</script>

<div class="entity-view">
  <Breadcrumbs {trail} />

  <!-- One useful page-level heading for the entity page (requirement 8.1). The rich
       EntityIdentity remains the visual header; the h1 gives the accessibility tree a
       meaningful page title without visual duplication.

       It sits OUTSIDE the ready branch on purpose. A page that is still loading, or
       whose entity does not exist, is still a page: it needs a name in the
       accessibility tree and in the browser tab, and without one the empty-state
       heading became the first heading on the page. We know the kind and the requested
       key before the request resolves, so there is always something honest to say. -->
  <h1 class="visually-hidden">{kindLabel(kind)}: {detail?.entity?.label || detail?.entity?.key || entityKey}</h1>

  {#if state.kind !== 'ready'}
    <ProductEmptyState {state} noun={kindLabel(kind).toLowerCase()} onRetry={load} />
  {:else}
    <header class="ev-head">
      <EntityIdentity ref={detail.entity} showStatus={false} />
      <EntityStatusBadge kind={detail.entity.kind} status={detail.status} />
    </header>

    <!-- The canonical key is Pacto's precise identity for this entity and stays available
         in full -- but it is ontology a first-time user has no use for, so it is one
         disclosure away instead of the second thing on the page (requirement 9). Nothing
         is lost: the value is unchanged and still copyable. -->
    <details class="ev-ident disclosure">
      <summary><span class="disclosure-caret" aria-hidden="true">&#9656;</span>Identifier</summary>
      <div class="ev-key">
        <span class="ev-key-label">Canonical key</span>
        <CopyableIdentifier value={detail.entity.key} />
      </div>
      <p class="ev-key-hint">The identity Pacto uses for this {kindLabel(kind).toLowerCase()} everywhere — in the API, the CLI and shared links.</p>
    </details>

    <KnowledgeBanner {knowledge} noun="page" />

    {#if refreshError}
      <StaleRefreshNotice noun="page" onRetry={load} />
    {/if}

    {#if actions.length}
      <div class="ev-actions">
        {#each actions as act}<a class="ev-action" href={act.href}>{act.label}</a>{/each}
      </div>
    {/if}

    {#if detail.service}
      <ServiceEntity {detail} />
    {:else if detail.revision}
      <RevisionEntity {detail} />
    {:else if detail.target}
      <TargetEntity {detail} />
    {:else if detail.owner}
      <OwnerEntity {detail} />
    {:else if detail.source}
      <SourceEntity {detail} />
    {/if}
  {/if}
</div>

<style>
  .entity-view { display: flex; flex-direction: column; gap: var(--sp-4); }
  .ev-head { display: flex; align-items: center; gap: var(--sp-3); flex-wrap: wrap; }
  /* Look and behaviour come from the shared .disclosure class in styles/components.css. */
  .ev-key { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .ev-key-label { font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.04em; color: var(--c-text-3); }
  .ev-key-hint { margin: var(--sp-2) 0 0; font-size: var(--text-sm); color: var(--c-text-3); }
  .ev-actions { display: flex; gap: var(--sp-2); flex-wrap: wrap; }
  .ev-action {
    text-decoration: none; font-size: var(--text-sm); color: var(--c-accent);
    border: 1px solid var(--c-accent-border); background: var(--c-accent-bg);
    padding: 4px 12px; border-radius: var(--radius-sm);
  }
  .ev-action:hover { border-color: var(--c-accent); }
</style>
