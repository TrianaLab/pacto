<script>
  import { api } from '../lib/api.ts';
  import { decideViewState, snapshotKnowledge } from '../lib/knowledgeState.ts';
  import {
    kindLabel, knowledgeLabel, knowledgeTone,
    linkStateLabel, linkStateTone, retrievabilityLabel, retrievabilityTone,
  } from '../lib/entityLabels.ts';
  import { fleetOverviewUrl, fleetGraphFocusUrl, fleetImpactUrl, hashForHref } from '../lib/router.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import EntityIdentity from '../components/EntityIdentity.svelte';
  import EntityLink from '../components/EntityLink.svelte';
  import CopyableIdentifier from '../components/CopyableIdentifier.svelte';
  import StatusBadge from '../components/StatusBadge.svelte';
  import ProductEmptyState from '../components/ProductEmptyState.svelte';

  // A real, useful entity route for every kind (requirement I). It resolves through
  // the product entity-detail endpoint (NarrowedEntityDetail) -- never the raw
  // snapshot -- and shows identity, canonical key, status/knowledge state, the
  // canonical actions from the DTO, and breadcrumbs. Rich per-kind pages are Phase 3.
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

  // Load on mount and whenever the target entity or refresh tick changes. Keeping the
  // initial load in the effect (rather than onMount) means load() is fire-and-forget
  // and always catches internally, so no rejected promise escapes to a lifecycle hook.
  $effect(() => {
    const key = `${kind}@@${entityKey}@@${refreshTick}`;
    if (key !== lastKey) {
      lastKey = key;
      load();
    }
  });

  const knowledge = $derived(snapshotKnowledge(detail?.meta));
  const state = $derived(decideViewState({ loading, error, itemCount: detail ? 1 : 0, knowledge }));

  // Map the DTO's route-neutral actions to canonical destinations via the centralized
  // navigation. Only actions we can honestly route are shown.
  const actions = $derived.by(() => {
    if (!detail) return [];
    const out = [];
    const e = detail.entity;
    const svcKey = detail.target?.service?.key || detail.revision?.service?.key || (e.kind === 'service' ? e.key : '');
    for (const a of detail.actions || []) {
      if (a === 'open-graph') out.push({ label: 'Open in graph', href: fleetGraphFocusUrl(e.kind, e.key) });
      else if (a === 'impact' && svcKey) out.push({ label: 'Analyze impact', href: fleetImpactUrl(svcKey) });
    }
    return out;
  });
</script>

<div class="entity-view">
  <Breadcrumbs trail={[{ label: 'Fleet', href: fleetOverviewUrl() }, { label: kindLabel(kind) }]} />

  {#if state.kind !== 'ready'}
    <ProductEmptyState {state} noun={kindLabel(kind).toLowerCase()} onRetry={load} />
  {:else}
    <header class="ev-head">
      <EntityIdentity ref={detail.entity} showStatus={false} />
      {#if detail.status}<StatusBadge status={detail.status} />{/if}
    </header>

    <div class="ev-key">
      <span class="ev-key-label">Canonical key</span>
      <CopyableIdentifier value={detail.entity.key} />
    </div>

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

    <!-- Kind-specific essentials (Phase 2 subset; rich pages are Phase 3). -->
    {#if detail.target}
      <section class="ev-block">
        <h2>Deployment</h2>
        <dl class="ev-dl">
          <dt>Revision match</dt>
          <dd><span class="tag tone-{linkStateTone(detail.target.linkState)}">{linkStateLabel(detail.target.linkState)}</span></dd>
          <dt>Content</dt>
          <dd><span class="tag tone-{retrievabilityTone(detail.target.identity?.identityClass, detail.target.identity?.retrievable)}">{retrievabilityLabel(detail.target.identity?.identityClass, detail.target.identity?.retrievable)}</span></dd>
          <dt>Compliance</dt>
          <dd><StatusBadge status={detail.target.compliance} /></dd>
          <dt>Service</dt>
          <dd><EntityLink ref={detail.target.service} /></dd>
          {#if detail.target.revision}<dt>Revision</dt><dd><EntityLink ref={detail.target.revision} /></dd>{/if}
        </dl>
        {#if detail.target.stale}<p class="ev-warn">Evidence is stale.</p>{/if}
      </section>
    {:else if detail.revision}
      <section class="ev-block">
        <h2>Revision</h2>
        <dl class="ev-dl">
          <dt>Service</dt><dd><EntityLink ref={detail.revision.service} /></dd>
          {#if detail.revision.version}<dt>Version</dt><dd>{detail.revision.version}</dd>{/if}
          <dt>Valid</dt><dd>{detail.revision.valid ? 'Yes' : 'No'}</dd>
          <dt>Content</dt>
          <dd><span class="tag tone-{retrievabilityTone(detail.revision.identity?.identityClass, detail.revision.identity?.retrievable)}">{retrievabilityLabel(detail.revision.identity?.identityClass, detail.revision.identity?.retrievable)}</span></dd>
        </dl>
      </section>
    {:else if detail.service}
      <section class="ev-block">
        <h2>Service</h2>
        {#if detail.service.ownership?.ref}<p>Owner: <EntityLink ref={detail.service.ownership.ref} showStatus={false} /></p>{/if}
        <p class="ev-counts">
          {detail.service.revisions?.total ?? 0} revisions ·
          {detail.service.deployments?.total ?? 0} deployments ·
          {detail.service.dependencies?.total ?? 0} dependencies ·
          {detail.service.dependents?.total ?? 0} dependents
        </p>
      </section>
    {:else if detail.owner}
      <section class="ev-block">
        <h2>Owner</h2>
        <p class="ev-counts">
          {detail.owner.services?.total ?? 0} services ·
          {detail.owner.revisions?.total ?? 0} revisions ·
          {detail.owner.deployments?.total ?? 0} deployments
        </p>
      </section>
    {:else if detail.source}
      <section class="ev-block">
        <h2>Source</h2>
        <dl class="ev-dl">
          <dt>Health</dt><dd><span class="tag tone-{knowledgeTone(detail.source.health === 'available' ? 'complete' : detail.source.health)}">{detail.source.health}</span></dd>
          {#if detail.source.kind}<dt>Kind</dt><dd>{detail.source.kind}</dd>{/if}
          <dt>Records</dt><dd>{detail.source.revisionCount ?? 0} revisions · {detail.source.targetCount ?? 0} deployments</dd>
        </dl>
      </section>
    {/if}
  {/if}
</div>

<style>
  .entity-view { display: flex; flex-direction: column; gap: var(--sp-4); }
  .ev-head { display: flex; align-items: center; gap: var(--sp-3); flex-wrap: wrap; }
  .ev-key { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .ev-key-label { font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.04em; color: var(--c-text-3); }
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
  .ev-block { border: 1px solid var(--c-border); border-radius: var(--radius-md); padding: var(--sp-4); background: var(--c-surface); }
  .ev-block h2 { margin: 0 0 var(--sp-3); }
  .ev-dl { display: grid; grid-template-columns: max-content 1fr; gap: var(--sp-2) var(--sp-4); margin: 0; align-items: center; }
  .ev-dl dt { color: var(--c-text-3); font-size: var(--text-sm); }
  .ev-dl dd { margin: 0; }
  .ev-counts { color: var(--c-text-2); font-size: var(--text-sm); }
  .ev-warn { color: var(--c-warn); font-size: var(--text-sm); }
  .tag {
    display: inline-block; font-size: var(--text-xs); font-weight: 600; padding: 2px 8px;
    border-radius: var(--radius-xs); color: var(--tone-c, var(--c-text));
    background: var(--c-surface-inset); border: 1px solid var(--tone-c, var(--c-border));
  }
  .tone-ok { --tone-c: var(--c-ok); }
  .tone-warn { --tone-c: var(--c-warn); }
  .tone-err { --tone-c: var(--c-err); }
  .tone-info { --tone-c: var(--c-info); }
  .tone-neutral { --tone-c: var(--c-neutral); }
</style>
