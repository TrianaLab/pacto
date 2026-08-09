<script>
  import { api } from '../lib/api.ts';
  import { decideViewState, snapshotKnowledge } from '../lib/knowledgeState.ts';
  import { kindLabel, knowledgeLabel, knowledgeTone } from '../lib/entityLabels.ts';
  import { fleetOverviewUrl, fleetGraphFocusUrl, fleetChangesUrl, hashForHref } from '../lib/router.ts';
  import { fleetEntityBreadcrumbs } from '../lib/breadcrumbs.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import EntityIdentity from '../components/EntityIdentity.svelte';
  import CopyableIdentifier from '../components/CopyableIdentifier.svelte';
  import StatusBadge from '../components/StatusBadge.svelte';
  import ProductEmptyState from '../components/ProductEmptyState.svelte';
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

  let detail = $state(null);
  let loading = $state(true);
  let error = $state(null);
  let lastKey = '';

  async function load() {
    loading = true;
    error = null;
    detail = null;
    try {
      detail = await api.fleetEntityDetail(kind, entityKey);
    } catch (e) {
      error = e;
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    const key = `${kind}@@${entityKey}@@${refreshTick}`;
    if (key !== lastKey) {
      lastKey = key;
      load();
    }
  });

  const knowledge = $derived(snapshotKnowledge(detail?.meta));
  const state = $derived(decideViewState({ loading, error, itemCount: detail ? 1 : 0, knowledge }));

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

  {#if state.kind !== 'ready'}
    <ProductEmptyState {state} noun={kindLabel(kind).toLowerCase()} onRetry={load} />
  {:else}
    <!-- One useful page-level heading for the entity page (requirement 8.1). The rich
         EntityIdentity remains the visual header; the h1 gives the accessibility tree a
         meaningful page title without visual duplication. -->
    <h1 class="visually-hidden">{kindLabel(kind)}: {detail.entity.label || detail.entity.key}</h1>
    <header class="ev-head">
      <EntityIdentity ref={detail.entity} showStatus={false} />
      {#if detail.status}<StatusBadge status={detail.status} />{/if}
    </header>

    <!-- The canonical key is Pacto's precise identity for this entity and stays available
         in full -- but it is ontology a first-time user has no use for, so it is one
         disclosure away instead of the second thing on the page (requirement 9). Nothing
         is lost: the value is unchanged and still copyable. -->
    <details class="ev-ident">
      <summary>Identifier</summary>
      <div class="ev-key">
        <span class="ev-key-label">Canonical key</span>
        <CopyableIdentifier value={detail.entity.key} />
      </div>
      <p class="ev-key-hint">The identity Pacto uses for this {kindLabel(kind).toLowerCase()} everywhere — in the API, the CLI and shared links.</p>
    </details>

    {#if knowledge.incomplete}
      <div class="ev-knowledge tone-{knowledgeTone(knowledge.level)}" role="status">
        {knowledgeLabel(knowledge.level)} — some sources are degraded, so this view may be incomplete.
      </div>
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
  .ev-ident > summary {
    font-size: var(--text-sm); color: var(--c-text-3); cursor: pointer;
    min-height: var(--touch-min); display: flex; align-items: center;
  }
  .ev-ident > summary:hover { color: var(--c-text-2); }
  .ev-key { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .ev-key-label { font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.04em; color: var(--c-text-3); }
  .ev-key-hint { margin: var(--sp-2) 0 0; font-size: var(--text-sm); color: var(--c-text-3); }
  .ev-knowledge {
    padding: var(--sp-2) var(--sp-3); border-radius: var(--radius-sm); font-size: var(--text-sm);
    background: var(--c-warn-bg); border: 1px solid var(--c-warn-border);
  }
  .ev-knowledge.tone-err { background: var(--c-err-bg); border-color: color-mix(in srgb, var(--c-err) 30%, transparent); }
  .ev-actions { display: flex; gap: var(--sp-2); flex-wrap: wrap; }
  .ev-action {
    text-decoration: none; font-size: var(--text-sm); color: var(--c-accent);
    border: 1px solid var(--c-accent-border); background: var(--c-accent-bg);
    padding: 4px 12px; border-radius: var(--radius-sm);
  }
  .ev-action:hover { border-color: var(--c-accent); }
</style>
