<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { deliveryHistoryUrl, deliveryEnvironmentsUrl, deliveryInboxUrl, deliveryAutonomyUrl, fleetEntityUrl } from '../lib/router.ts';
  import { shortDigest, sentence, exceptParam, levelLabel, levelHelp, offerLabel, checkText, watchText, timeLabel, andList } from '../lib/delivery.ts';
  import { sourceProblem, upstreamText } from '../lib/envFigure.ts';
  import EnvMark from './figure/EnvMark.svelte';
  import ProposePanel from './ProposePanel.svelte';
  import UndoPanel from './UndoPanel.svelte';
  import VerdictList from './VerdictList.svelte';
  import PreviewSection from './PreviewSection.svelte';
  import DeliveryBadges from './DeliveryBadges.svelte';
  import DeliveryProblem from './DeliveryProblem.svelte';
  import DeliveryUnavailable from './DeliveryUnavailable.svelte';
  import PromotionFigure from './figure/PromotionFigure.svelte';

  // A service's Stages on its page: its own promotion path as a figure (SPEC 21),
  // then each Stage with the same badges as the Delivery board.
  // `state` (served here) is whether this dashboard serves delivery: only 'enabled' asks.
  // `domain` is the service's registry domain: same-named services in two
  // domains are two services, and a Stage naming one is not the other's.
  let { service = '', domain = '', state: served = 'unknown', refreshTick = 0 } = $props();

  // Paging counts services, so one service is one page. The exact `service`
  // and `domain` filter already returns only its Stages; the row guard keeps it
  // that way. A Stage that names no domain matches any; when this service has
  // a domain, its row says the match is by name only.
  const NO_DOMAIN = 'matched by service name only, so it may be a same-named service in another domain';
  const tag = $derived(`${domain}/${service}`);
  const loader = createProductLoader(() => api.deliveryEnvironments({ service, domain, limit: 1 }));
  $effect(() => { if (served === 'enabled') loader.sync(`${tag}@@${refreshTick}`, tag); });
  onDestroy(() => loader.destroy());

  // Rows on hand stay while a refresh of the same service is in flight.
  const data = $derived(loader.dataTag === tag ? loader.data : null);
  const rows = $derived((data?.environments ?? []).filter((r) => r.service === service && (!r.domain || r.domain === domain)));
  const partial = $derived(data?.completeness === 'partial');
  const unreadable = $derived(andList(data?.unreadable ?? [], 5));

  // One panel at a time under the Stage it acts on. Offers only describe: the
  // panel plans, then proposes with the caller's own identity, and admission decides.
  let acting = $state(null);
  let checksOpen = $state({});
  let opener = null;
  const keyOf = (r) => `${r.namespace}/${r.stage}`;
  function act(e, r, o) {
    opener = e.currentTarget;
    const req = { namespace: r.namespace, stage: r.stage, type: `delivery.pacto.trianalab.io/${o.action}` };
    const to = o.to || r.candidate;
    if (o.action === 'promote') Object.assign(req, { to, from: r.pointer || undefined });
    acting = { key: keyOf(r), offer: o, request: req, version: o.action === 'promote' && to === r.candidate ? (r.candidateVersion ?? '') : '' };
  }
  function close() { acting = null; opener?.focus(); }
  const ready = (r) => !!r.ready && !r.ready.startsWith('True');
</script>

{#snippet links()}
  {#if served === 'enabled'}
    <p class="t-body-2 sd-links go">
      <a href={deliveryHistoryUrl({ service, domain })}>Delivery history</a>
      <a href={deliveryEnvironmentsUrl({ service, domain })}>Open in Delivery</a>
    </p>
  {/if}
{/snippet}

{#snippet emptyBody()}
  {#if served === 'unknown'}
    <p class="t-body-2">Checking whether this dashboard serves delivery.</p>
  {:else if served !== 'enabled'}
    <DeliveryUnavailable reason={served} />
  {:else if loader.error}
    <DeliveryProblem error={loader.error} onRetry={() => loader.refresh()} />
  {:else if !data}
    <p class="t-body-2">Loading delivery</p>
  {:else if partial}
    <p class="t-body-2" data-testid="service-delivery-partial">No Stages of this service that you can see. Pacto could not list {unreadable || 'every namespace'}, so a Stage may be there. As of {timeLabel(data.asOf)}.</p>
  {:else}
    <p class="t-body-2">No Stages for this service. Delivery is opt-in per service.</p>
  {/if}
  {@render links()}
{/snippet}

<PreviewSection title="Delivery" count={rows.length} total={rows.length} {emptyBody}
  help="The Stages of this service: the version each environment runs and whether it is verified, failed, frozen or waiting.">
  {#if data?.promotion?.nodes?.length}<PromotionFigure {service} graph={data.promotion} {rows} id="svc-promo" moreHref={(ex) => deliveryEnvironmentsUrl({ service, domain, except: exceptParam(ex) })} />{/if}
  <div class="sd-list">
  {#each rows as r (keyOf(r))}
    {@const problem = ready(r) ? '' : sourceProblem(r)}
    {@const source = upstreamText(r)}
    <div class="sd-row" data-testid="service-delivery-row">
      <div class="sd-head">
        <bdi class="sd-env">{r.environment}</bdi>
        <EnvMark state={r.state} word />
      </div>
      <div class="sd-body">
        <p class="sd-line">
          {#if r.revision}
            <a href={fleetEntityUrl('revision', r.revision)} title={r.pointer}>{r.version || shortDigest(r.pointer)}</a>
            <a class="t-meta" href={deliveryHistoryUrl({ namespace: r.namespace, stage: r.stage })}>History</a>
          {:else}
            <a href={deliveryHistoryUrl({ namespace: r.namespace, stage: r.stage })} title={r.pointer || 'nothing deployed yet'}>{r.version || shortDigest(r.pointer) || 'Nothing yet'}</a>
          {/if}
          {#if r.level}<span class="t-meta" data-testid="level" title={levelHelp(r.level)}>{levelLabel(r.level)}</span>{/if}
          <DeliveryBadges row={r} heldHref={deliveryInboxUrl({ service, domain })} />
          {#if r.level === 'observe'}<a class="t-meta" href={deliveryAutonomyUrl({ service, domain })}>What Pacto would have done</a>{/if}
        </p>
        {#if r.contractRef}<p class="t-meta sd-line" title={r.contractRef}>Contract {#if r.revision}<a href={fleetEntityUrl('revision', r.revision)}><bdi>{shortDigest(r.contractRef.split('@').pop())}</bdi></a>{:else}<bdi>{shortDigest(r.contractRef.split('@').pop())}</bdi>{/if}</p>{/if}
        {#if r.sourceRevision}<p class="t-meta sd-line" title={r.sourceRevision}>Source revision <bdi class="sd-mono">{r.sourceRevision}</bdi>, an annotation, not signed</p>{/if}
        {#if source}<p class="t-meta sd-line">{source}</p>{/if}
        {#if r.frozen && r.frozenReason}<p class="t-meta sd-line">Frozen: {sentence(r.frozenReason)}</p>{/if}
        {#if ready(r)}
          <p class="t-meta sd-line sd-warn">Cannot promote: {sentence(r.readyMessage || r.ready.replace(/^\w+: /, ''))}</p>
          {#if r.readyFix}<p class="t-meta sd-line">Fix: {sentence(r.readyFix)}</p>{/if}
        {:else if problem}<p class="t-meta sd-line sd-warn" data-testid="env-no-source">{problem}</p>{/if}
        {#if domain && !r.domain}<p class="sd-line"><span class="t-meta sd-nodomain" data-testid="service-delivery-nodomain" title={NO_DOMAIN}>Stage names no domain<span class="visually-hidden">: {NO_DOMAIN}</span></span></p>{/if}
        {#if r.candidate || r.candidateVersion}<p class="t-meta sd-line">Candidate {r.candidateVersion || shortDigest(r.candidate)}</p>{/if}
        {#if r.lastCheck}
          <!-- The verdicts mount only once opened: a closed disclosure's content still lays out boxes. -->
          <details class="disclosure sd-check" ontoggle={(e) => { checksOpen[keyOf(r)] = e.currentTarget.open; }}>
            <summary><span class="disclosure-caret" data-motion aria-hidden="true"></span>{checkText(r.lastCheck)}</summary>
            {#if checksOpen[keyOf(r)]}<VerdictList verdicts={r.lastCheck.verdicts ?? []} testid="service-delivery-check" />{/if}
          </details>
        {/if}
        {#if r.watched}<p class="t-meta sd-line" title={r.watched.message || undefined}>{watchText(r.watched)}</p>{/if}
        {#if r.offers?.length}
          <div class="sd-offers">
            {#each r.offers as o (o.action + (o.operation ?? ''))}
              <div class="sd-offer">
                <button type="button" class="btn btn-sm" data-testid="service-delivery-offer" disabled={!o.available}
                  aria-label={`${offerLabel(o.action)} ${service} in ${r.environment}`} title={o.reason}
                  onclick={(e) => act(e, r, o)}>{offerLabel(o.action)}</button>
                {#if !o.available}<span class="t-meta">{offerLabel(o.action)}: {o.reason}</span>{/if}
              </div>
            {/each}
          </div>
        {/if}
        {#if acting?.key === keyOf(r)}
          {#key acting}
            {#if acting.offer.action === 'undo'}
              <UndoPanel namespace={r.namespace} stage={r.stage} name={acting.offer.operation} onClose={close} />
            {:else}
              <ProposePanel request={acting.request} toVersion={acting.version} onClose={close} />
            {/if}
          {/key}
        {/if}
      </div>
    </div>
  {/each}
  </div>
  {#if partial && rows.length}<p class="t-meta" data-testid="service-delivery-partial">Pacto could not list {unreadable || 'every namespace'}, so this service may have more Stages. As of {timeLabel(data.asOf)}.</p>{/if}
  {#if loader.error}<DeliveryProblem error={loader.error} onRetry={() => loader.refresh()} />{/if}
  {@render links()}
</PreviewSection>

<style>
  /* Stage rows: the environment and its state beside the details, stacked below the container breakpoint (SPEC 24). */
  .sd-list { container-type: inline-size; display: flex; flex-direction: column; }
  .sd-row { display: grid; grid-template-columns: minmax(0, 1fr); gap: var(--sp-1) var(--sp-3); padding: var(--sp-2) 0; border-block-end: 1px solid var(--c-border); }
  @container (min-width: 40rem) { .sd-row { grid-template-columns: minmax(12ch, 14rem) minmax(0, 1fr); } }
  .sd-head { display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--sp-1) var(--sp-2); min-inline-size: 0; }
  .sd-env { font-weight: 600; overflow-wrap: anywhere; }
  .sd-body { display: flex; flex-direction: column; gap: var(--sp-1); min-inline-size: 0; overflow-wrap: anywhere; }
  .sd-line { display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--sp-1) var(--sp-2); margin: 0; }
  .sd-mono { font-family: var(--font-mono); }
  .sd-warn { color: var(--c-warn); }
  .sd-check summary { cursor: pointer; }
  .sd-offers { display: flex; flex-wrap: wrap; gap: var(--sp-1) var(--sp-3); }
  .sd-offer { display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--sp-1); min-inline-size: 0; }
  @media (pointer: coarse) { .sd-offer .btn { min-block-size: 44px; min-inline-size: 44px; } }
  .sd-nodomain { border: 1px solid var(--c-border); border-radius: var(--radius-sm); padding: 0 var(--sp-1); }
  .sd-links { display: flex; gap: var(--sp-3); margin: var(--sp-2) 0 0; }
</style>
