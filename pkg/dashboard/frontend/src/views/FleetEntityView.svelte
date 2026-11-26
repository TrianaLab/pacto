<script>
  import { onDestroy } from 'svelte';
  import { api, ApiError } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { decideViewState, snapshotKnowledge } from '../lib/knowledgeState.ts';
  import { kindLabel, serviceKeyName } from '../lib/entityLabels.ts';
  import { formatWhen } from '../lib/dateFormat.ts';
  import { fleetOverviewUrl, fleetGraphFocusUrl, fleetChangesUrl, fleetEntityUrl, hashForHref, replaceHash, serviceDeclareUrl } from '../lib/router.ts';
  import { fleetEntityBreadcrumbs } from '../lib/breadcrumbs.ts';
  import { postureAnswer, revisionAnswer } from '../lib/answers.ts';
  import { currentEntity } from '../lib/currentEntity.svelte.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import KnowledgeBanner from '../components/KnowledgeBanner.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import PageToc from '../components/PageToc.svelte';
  import CopyableIdentifier from '../components/CopyableIdentifier.svelte';
  import ProductEmptyState from '../components/ProductEmptyState.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import TargetKeyChooser from '../components/TargetKeyChooser.svelte';
  import StaleRefreshNotice from '../components/StaleRefreshNotice.svelte';
  import ProvenanceMark from '../components/ProvenanceMark.svelte';
  import ServiceEntity from './entity/ServiceEntity.svelte';
  import RevisionEntity from './entity/RevisionEntity.svelte';
  import TargetEntity from './entity/TargetEntity.svelte';
  import OwnerEntity from './entity/OwnerEntity.svelte';
  import SourceEntity from './entity/SourceEntity.svelte';
  import DomainEntity from './entity/DomainEntity.svelte';
  import OfferingEntity from './entity/OfferingEntity.svelte';
  import ExternalEntity from './entity/ExternalEntity.svelte';
  import InferredServiceEntity from './entity/InferredServiceEntity.svelte';

  // The unified entity route for every kind. It resolves the
  // product entity-detail endpoint (NarrowedEntityDetail) -- never the raw snapshot --
  // and owns the shared shell: entity-relationship breadcrumbs, identity header,
  // canonical key, knowledge caveat and contextual actions. The kind-specific rich
  // body is delegated to the per-kind component.
  // miraURL: Mira's address, only for a caller whose permissions hide nothing.
  // canDeclare and declareQuery reach the inferred service page's Declare panel; its
  // signed intent also reaches a declared service page's accept plan. createService,
  // portalCreateURL and createQuery reach a golden path's Create service panel.
  let { kind = '', entityKey = '', refreshTick = 0, deliveryState = 'unknown', miraURL = '', correlation = '', changesQuery = {}, canDeclare = false, declareQuery = {}, createService = false, portalCreateURL = '', createQuery = {} } = $props();

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
  // The query of the current address (a correlationID the investigation carries),
  // kept when the page moves to another key of the same question.
  function currentQuery() {
    const i = location.hash.indexOf('?');
    return i < 0 ? '' : location.hash.slice(i);
  }
  // A legacy 3-part target key, or a cluster-qualified key in a single-cluster
  // snapshot, resolves to one target whose canonical key differs: put the canonical
  // href in the address bar without a new history entry, so a shared link is the real one.
  $effect(() => {
    if (kind === 'target' && detail?.entity?.key && detail.entity.key !== entityKey) {
      replaceHash((detail.entity.href ? hashForHref(detail.entity.href) : fleetEntityUrl('target', detail.entity.key)) + currentQuery());
    }
  });
  // A target key that does not name one target for certain never resolves to a
  // guess: the server answers 409 with its reason and the candidates, and the reader chooses.
  const candidates = $derived(
    kind === 'target' && loader.error instanceof ApiError && loader.error.status === 409 ? loader.error.candidates : [],
  );
  const knowledge = $derived(snapshotKnowledge(detail?.meta));
  const state = $derived(decideViewState({
    loading: loader.loading, error: loader.error, itemCount: detail ? 1 : 0, knowledge,
  }));
  // A refresh that failed over a page we can still show. The page stays; the failure is
  // stated rather than swallowed, so nobody reads a frozen page as a current one.
  const refreshError = $derived(state.kind === 'ready' ? state.refreshError : null);
  // A poll in flight over data already on screen. decideViewState has decided this on
  // every page since stale-while-revalidate landed; the header is the first thing to
  // actually say it, in words rather than by spinning something.
  const revalidating = $derived(state.kind === 'ready' && !!state.revalidating);

  // The one-line answer under the title: is this service or owner estate broken, is
  // this revision valid and ready. Kinds without a single question get none.
  const entityAnswer = $derived.by(() => {
    if (!detail) return null;
    // An inferred service has no contract, so no posture: "nothing running" would be false.
    if (detail.entity?.declaration === 'inferred') return null;
    if (detail.service) return postureAnswer(detail.service.summary, knowledge.incomplete, detail.service.ownership?.conflicts?.total ?? 0);
    if (detail.owner) return postureAnswer(detail.owner.summary, knowledge.incomplete);
    if (detail.domain) return postureAnswer(detail.domain.summary, knowledge.incomplete);
    if (detail.revision) return revisionAnswer(detail.revision);
    return null;
  });

  // Asked for by the key of an inferred service a contract took over: the answer is the
  // declared service, and the page says so once instead of renaming the question.
  const replacedFrom = $derived(detail?.replacedFrom);

  // A service only a producer's contribution describes: inferred, yet rich enough for the
  // service page. One discreet mark in the header says who contributed it and when; the
  // details stay in the tip.
  const contribution = $derived(detail?.service?.contribution);
  const contributionTip = $derived.by(() => {
    const c = contribution;
    if (!c) return '';
    const now = Date.parse(detail?.meta?.asOf ?? '') || Date.now();
    const recorded = c.asOf ? `recorded ${formatWhen(c.asOf, now)}` : 'record time unknown';
    const read = c.readAt ? `, read ${formatWhen(c.readAt, now)}` : '';
    const key = c.keyId ? `, signed by key ${c.keyId}` : '';
    return `Contributed by ${c.producer} from ${c.ref}${key}; ${recorded}${read}${c.stale ? ', stale' : ''}. Inferred: a producer's record, never a contract, and it never clears a gate.`;
  });
  const replacedAge = $derived(replacedFrom?.at ? ` ${formatWhen(replacedFrom.at, Date.parse(detail?.meta?.asOf ?? '') || Date.now())}` : '');

  // Entity-relationship breadcrumbs from canonical DTO refs; a minimal
  // trail while loading/erroring.
  const trail = $derived(
    detail ? fleetEntityBreadcrumbs(detail) : [{ label: 'Overview', href: fleetOverviewUrl() }, { label: kindLabel(kind) }],
  );

  // The nav lights Golden paths on a golden path's page, like the trail above.
  $effect(() => {
    currentEntity.template = !!detail?.entity?.template;
    return () => { currentEntity.template = false; };
  });

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
        // The other question the same workspace answers (#471): what depends on it.
        out.push({ label: 'What if it becomes unavailable?', href: fleetChangesUrl(svcKey, { event: 'unavailable' }) });
      } else if (a === 'impact' && e.kind === 'offering') out.push({ label: 'What if it becomes unavailable?', href: fleetChangesUrl('', { offering: e.key, event: 'unavailable' }) });
      // An external system has no revision Pacto controls: the question is what its change reaches.
      else if (a === 'impact' && e.kind === 'external') out.push({ label: 'What if it changes?', href: fleetChangesUrl('', { external: e.key, event: 'changed' }) });
      else if (a === 'service' && detail.target?.service?.href) out.push({ label: 'Open service', href: hashForHref(detail.target.service.href) });
      // An inferred offering a person may accept here: the page's own Accept panel.
      else if (a === 'accept-offering' && canDeclare) out.push({ label: 'Accept', href: serviceDeclareUrl(e.key) });
    }
    return out;
  });
</script>

<div class="product-page">
  <Breadcrumbs {trail} />

  <!-- The page's one visible title, at page-title scale, plus the kind, the
       disambiguating context, the current status and the primary actions. It used to be a visually-hidden h1 with the inline list-row
       identity underneath, which rendered at exactly the size and weight of every
       section title on the page: the page was outranked by its own contents.

       It sits OUTSIDE the ready branch on purpose. A page that is still loading, or
       whose entity does not exist, is still a page: it needs a name on screen, in the
       accessibility tree and in the browser tab, and without one the empty-state
       heading became the first heading on the page. We know the kind and the requested
       key before the request resolves, so there is always something honest to say. -->
  <PageHeader
    ref={detail?.entity}
    title={detail?.entity ? '' : entityKey}
    titlePrefix={kindLabel(kind)}
    {kind}
    status={detail?.status || ''}
    {actions}
    asOf={detail?.meta?.asOf}
    {revalidating}
    subtitle={detail?.entity?.summary || ''}
  >
    {#if contribution}
      <p class="ev-contribution t-meta" data-testid="service-contribution">Contributed by <bdi>{contribution.producer}</bdi>{#if contribution.stale}<span data-testid="service-contribution-stale">, stale</span>{/if} <ProvenanceMark label={`Inferred: contributed by ${contribution.producer}`} text={contributionTip} /></p>
    {/if}
  </PageHeader>

  {#if state.kind !== 'ready' && candidates.length}
    <TargetKeyChooser message={loader.error?.message ?? ''} {candidates} hrefFor={(key) => fleetEntityUrl('target', key) + currentQuery()} />
  {:else if state.kind !== 'ready'}
    <ProductEmptyState {state} noun={kindLabel(kind).toLowerCase()} onRetry={load} />
  {:else}
    <!-- An entity page is a dozen titled sections deep. The shared navigator lists the
         ones actually rendered for THIS kind (a target has no revision list, an owner no
         findings), so the contents can never offer a section that is not there. -->
    <div class="page-toc-layout">
      <PageToc />
      <div class="page-toc-main ev-body">
      <!-- The answer heads the content column, so the contents rail starts right under
           the page title and stays above the fold. -->
      {#if entityAnswer}
        <p class="page-answer t-body ev-answer tone-{entityAnswer.tone}" data-testid="entity-answer"><strong>{entityAnswer.lead}</strong> {entityAnswer.rest}</p>
      {/if}
      {#if replacedFrom}
        <p class="ev-replaced t-body-2" data-testid="entity-replaced-from"><bdi title={serviceKeyName(replacedFrom.from)}>{serviceKeyName(replacedFrom.from)}</bdi> was replaced by {detail.entity?.label || replacedFrom.to}{replacedAge}{replacedFrom.link === 'observed' ? `, linked with no Pacto by its ${replacedFrom.signal === 'annotation' ? 'annotation' : 'image digest'}` : replacedFrom.target ? `, linked by Pacto ${replacedFrom.target}` : ''}</p>
      {/if}
      <!-- Uncertainty and failed refreshes come FIRST and are never collapsible: they
           qualify everything below them, so a reader who stops after the first screen
           must still have seen them. -->
      <KnowledgeBanner {knowledge} noun="page" sources={detail?.meta?.sources ?? []} />

      {#if refreshError}
        <StaleRefreshNotice noun="page" onRetry={load} />
      {/if}

      <!-- The canonical key is Pacto's precise identity for this entity and stays available
           in full -- but it is ontology a first-time user has no use for, so it is one
           disclosure away instead of the second thing on the page. Nothing
           is lost: the value is unchanged and still copyable. -->
      <!-- A <details> maps to role=group, which takes its accessible name from the
           author only — never from its summary — so the canonical test id is the one
           stable handle a test has on this disclosure, exactly as for the software
           inventory and the graph's text alternative. -->
      <details class="ev-ident disclosure" data-testid="entity-identifier">
        <summary><span class="disclosure-caret" data-motion aria-hidden="true"></span>Identifier</summary>
        <div class="ev-key">
          <span class="ev-key-label">Canonical key</span>
          <CopyableIdentifier value={detail.entity.key} />
        </div>
        <p class="ev-key-hint">The identity Pacto uses for this {kindLabel(kind).toLowerCase()} everywhere: in the API, the CLI and shared links.</p>
      </details>

      <!-- One malformed record (a duplicate key, a field of the wrong shape) must cost
           this body, not the page: without a boundary it left the skeleton up for ever. -->
      <svelte:boundary>
        {#if detail.service && detail.entity?.declaration === 'inferred' && !contribution}
          <InferredServiceEntity {detail} {canDeclare} {declareQuery} />
        {:else if detail.service}
          <!-- A contributed inferred offering is accepted from its own page (515-S4). -->
          <ServiceEntity {detail} {deliveryState} {refreshTick} {miraURL} {changesQuery} acceptSigned={declareQuery.signed ?? ''}
            canDeclare={canDeclare && !!contribution && !!detail.entity?.offering} declareQuery={contribution && detail.entity?.offering ? declareQuery : {}} {createService} {portalCreateURL} {createQuery} />
        {:else if detail.revision}
          <RevisionEntity {detail} {deliveryState} {miraURL} {correlation} />
        {:else if detail.target}
          <TargetEntity {detail} {deliveryState} {correlation} />
        {:else if detail.owner}
          <OwnerEntity {detail} />
        {:else if detail.source}
          <SourceEntity {detail} />
        {:else if detail.domain}
          <DomainEntity {detail} />
        {:else if detail.offering}
          <OfferingEntity {detail} />
        {:else if detail.external}
          <ExternalEntity {detail} {canDeclare} {declareQuery} />
        {/if}
        {#snippet failed(err, reset)}
          <EmptyState error title={`Couldn’t show this ${kindLabel(kind).toLowerCase()}`} message="Part of its record could not be drawn. Refresh to try again." onRetry={reset} />
        {/snippet}
      </svelte:boundary>
      </div>
    </div>
  {/if}
</div>

<style>
  /* The answer's lead word carries the tone; the words say it too (WCAG 1.4.1). */
  .ev-answer.tone-err strong { color: var(--c-err); }
  .ev-answer.tone-warn strong { color: var(--c-warn); }
  .ev-answer.tone-ok strong { color: var(--c-ok); }
  /* The resolved page. Its own element (rather than a bare fragment) is what lets a
     test say "the entity BODY is on screen" without that also being true while the
     page header is still waiting for the request. The column layout comes from the
     shared `.page-toc-main`; only the tighter section rhythm is this page's own. */
  .ev-body { gap: var(--sp-4); }
  /* Muted like every inferred item: a producer's record, never a contract. */
  .ev-contribution { margin: 0; color: var(--c-text-3); overflow-wrap: anywhere; }
  .ev-replaced { margin: 0; color: var(--c-text-2); overflow-wrap: anywhere; }
  /* Look and behaviour come from the shared .disclosure class in styles/components.css. */
  .ev-key { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .ev-key-label { color: var(--c-text-3); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); letter-spacing: var(--role-label-tracking); }
  .ev-key-hint { margin: var(--sp-2) 0 0; font-size: var(--text-sm); color: var(--c-text-3); }
</style>
