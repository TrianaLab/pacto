<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { deliveryHistoryUrl, deliveryEnvironmentsUrl, deliveryInboxUrl, deliveryAutonomyUrl, deliveryPublishUrl, fleetEntityUrl } from '../lib/router.ts';
  import { shortDigest, sentence, exceptParam, levelLabel, levelHelp, offerLabel, offerRequest, checkText, watchText, timeLabel, andList, typeLabel, progressStamp, isCommand } from '../lib/delivery.ts';
  import { completenessClass, completenessLabel } from '../lib/format.ts';
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
  import AnswerCard from './figure/AnswerCard.svelte';
  import Glyph from './figure/Glyph.svelte';
  import CopyableIdentifier from './CopyableIdentifier.svelte';

  // A service's Stages on its page: its own promotion path as a figure (SPEC 21),
  // then each Stage with the same badges as the Delivery board.
  // `state` (served here) is whether this dashboard serves delivery: only 'enabled' asks.
  // `domain` is the service's registry domain: same-named services in two
  // domains are two services, and a Stage naming one is not the other's.
  // `placement` and `meta` are the fleet's observed answer (detail.service.placement, detail.meta).
  let { service = '', domain = '', state: served = 'unknown', refreshTick = 0, placement = null, meta = null } = $props();

  // Paging counts services, so one service is one page. The exact `service`
  // and `domain` filter already returns only its Stages; the row guard keeps it
  // that way. A Stage that names no domain matches any; when this service has
  // a domain, its row says the match is by name only.
  const NO_DOMAIN = 'matched by service name only, so it may be a same-named service in another domain';
  const tag = $derived(`${domain}/${service}`);
  const loader = createProductLoader(() => api.deliveryEnvironments({ service, domain, limit: 1 }));
  $effect(() => { if (served === 'enabled') loader.sync(`${tag}@@${refreshTick}`, tag); });
  // The newest change per Stage: the every-Stage history keeps each Stage's recent lines, newest first.
  const history = createProductLoader(() => api.deliveryHistory('', '', { service, domain, limit: 25 }));
  onDestroy(() => { loader.destroy(); history.destroy(); });

  // Rows on hand stay while a refresh of the same service is in flight.
  const data = $derived(loader.dataTag === tag ? loader.data : null);
  const rows = $derived((data?.environments ?? []).filter((r) => r.service === service && (!r.domain || r.domain === domain)));
  const partial = $derived(data?.completeness === 'partial');
  const unreadable = $derived(andList(data?.unreadable ?? [], 5));
  $effect(() => { if (served === 'enabled' && rows.length) history.sync(`${tag}@@${refreshTick}`, tag); });
  const lastOf = $derived(new Map((history.dataTag === tag ? history.data?.histories ?? [] : []).map((h) => [`${h.namespace}/${h.stage?.name}`, h.entries?.[0]])));

  // Where it runs, observed from targets (D5): without delivery, or with a complete
  // listing that has no Stage, every environment; beside Stages only the environments
  // no Stage covers. A partial listing may hide the Stage that covers one, so nothing.
  const noDelivery = $derived((served !== 'enabled' && served !== 'unknown') || loader.error?.code === 'DeliveryNotInstalled');
  const observed = $derived.by(() => {
    const items = placement?.items ?? [];
    if (noDelivery || (data && !partial && !rows.length)) return items;
    if (!data || partial) return [];
    const covered = new Set(rows.map((r) => r.environment));
    return items.filter((p) => p.environment && !covered.has(p.environment));
  });
  const metaPartial = $derived(meta?.completeness === 'partial');

  // Why: asked once per Stage, when its disclosure first opens (one dry-run plan each).
  let whys = $state({});
  function askWhy(e, r) {
    const k = keyOf(r);
    if (!e.currentTarget.open || whys[k]) return;
    whys[k] = {};
    api.deliveryProgress(r.namespace, r.stage).then((d) => { whys[k] = { data: d }; }, (err) => { whys[k] = { error: err }; });
  }

  // One panel at a time under the Stage it acts on. Offers only describe: the
  // panel plans, then proposes with the caller's own identity, and admission decides.
  let acting = $state(null);
  let checksOpen = $state({});
  let opener = null;
  const keyOf = (r) => `${r.namespace}/${r.stage}`;
  function act(e, r, o) {
    opener = e.currentTarget;
    acting = { key: keyOf(r), offer: o, ...offerRequest(r, o) };
  }
  function close() { acting = null; opener?.focus(); }
  const ready = (r) => !!r.ready && !r.ready.startsWith('True');
</script>

{#snippet links()}
  {#if served === 'enabled'}
    <p class="t-body-2 sd-links go">
      <a href={deliveryHistoryUrl({ service, domain })}>Delivery history</a>
      <a href={deliveryEnvironmentsUrl({ service, domain })}>Open in Delivery</a>
      <a href={deliveryPublishUrl({ service, domain })}>Published versions</a>
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
  {@render observedStrip()}
{/snippet}

{#snippet observedStrip()}
  {#if observed.length}
    <section class="sd-placement" data-testid="service-placement" aria-labelledby="sd-placement-title">
      <h3 id="sd-placement-title" class="t-subsection-title">Where it runs (observed{metaPartial ? ', targets Pacto can see' : ''})</h3>
      <p class="t-meta sd-line">
        Each row is observed from targets, not a promotion step.{#if metaPartial} An environment missing here may still run it.{/if}
        <span class="badge {completenessClass(meta?.completeness)}">{completenessLabel(meta?.completeness)}</span>
        {#if meta?.asOf}<span>As of {timeLabel(meta.asOf)}</span>{/if}
      </p>
      <ul class="sd-pl-list">
        {#each observed as p (p.environment)}
          <li class="sd-pl-row" data-testid="service-placement-row">
            <span class="sd-pl-env"><Glyph kind="environment" /><bdi title={p.environment || undefined}>{p.environment || '(no environment label)'}</bdi></span>
            <span class="t-meta">{p.targets} {p.targets === 1 ? 'target' : 'targets'}</span>
            <span class="sd-line">
              {#if p.declared}<span class="badge badge-ok">{p.declared} declared</span>{/if}
              {#if p.notDeclared}<span class="badge badge-warn">{p.notDeclared} not declared</span>{/if}
              {#if p.unknown}<span class="badge badge-neutral"><span class="sd-hatch hatch" aria-hidden="true"></span>{p.unknown} unknown</span>{/if}
            </span>
            <span class="sd-line">
              {#each p.revisions ?? [] as v (v.key)}<a href={v.href} title={v.version || v.label}><bdi>{v.version || v.label}</bdi></a>{/each}
              {#if p.revisionsTruncated}<span class="t-meta">and {p.revisionsTotal - (p.revisions?.length ?? 0)} more</span>{/if}
            </span>
          </li>
        {/each}
      </ul>
      {#if placement?.truncated}<p class="t-meta">And {placement.total - placement.count} more environments.</p>{/if}
    </section>
  {/if}
{/snippet}

<PreviewSection title="Delivery" count={rows.length} total={rows.length} {emptyBody}
  help="The Stages of this service: the version each environment runs and whether it is verified, failed, frozen or waiting.">
  {#if data?.promotion?.nodes?.length}<PromotionFigure {service} graph={data.promotion} {rows} id="svc-promo" moreHref={(ex) => deliveryEnvironmentsUrl({ service, domain, except: exceptParam(ex) })} />{/if}
  <div class="sd-list">
  {#each rows as r (keyOf(r))}
    {@const problem = ready(r) ? '' : sourceProblem(r)}
    {@const source = upstreamText(r)}
    {@const last = lastOf.get(keyOf(r))}
    {@const why = whys[keyOf(r)]}
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
        {#if last}
          <!-- A line read from an Operation's status has no record: its applied time, cause and correlation are not signed. -->
          <p class="t-meta sd-line" data-testid="service-delivery-last">
            <span>Last change: {typeLabel(last.type)}{#if last.outcome}, {last.outcome}{/if}</span>
            {#if last.applied || last.finished}<span>applied {timeLabel(last.applied || last.finished)}</span>{/if}
            {#if last.cause?.reason || last.cause?.operation}<span>cause <bdi>{last.cause.reason || last.cause.operation}</bdi></span>{/if}
            {#if last.correlationID}<span>correlation <bdi class="sd-mono">{last.correlationID}</bdi></span>{/if}
            {#if !last.record}<span class="sd-nodomain" title="Read from the Operation's status: its applied time, cause and correlation are not in a signed record">from status, not signed</span>{/if}
          </p>
        {/if}
        <details class="disclosure sd-why" ontoggle={(e) => askWhy(e, r)}>
          <summary><span class="disclosure-caret" data-motion aria-hidden="true"></span>Why<span class="visually-hidden"> {r.environment} can or cannot move</span></summary>
          {#if why?.error}
            <p class="t-meta sd-warn">Pacto could not say why: {why.error.message || String(why.error)}</p>
          {:else if why?.data}
            {@const p = why.data}
            <div class="sd-why-body" data-testid="service-delivery-why">
              <AnswerCard stamps={[progressStamp(p.state)]} completeness={p.state === 'unknown' ? 'partial' : 'complete'} asOf={timeLabel(p.asOf)}
                provenance="The Stage and its operations, read as you, and a promote planned as you, not proposed">
                {#snippet lead()}<p class="ca-answer-line">{sentence(p.summary)}</p>{/snippet}
                {#snippet facts()}
                  {#if p.reasons?.length}
                    <dt>Held back by</dt>
                    <dd><ul class="sd-reasons">
                      {#each p.reasons as x, i (i)}
                        <li><span>{sentence(x.message)}</span>
                          {#if isCommand(x.fix)}<span data-testid="why-command"><CopyableIdentifier value={x.fix} label="command" /></span>
                          {:else if x.fix}<span class="t-meta">{sentence(x.fix)}</span>{/if}</li>
                      {/each}
                    </ul></dd>
                  {/if}
                  <dt>Automatic promote</dt>
                  <dd>{p.autoPromote ? sentence(p.autoPromote) : 'The promoter promotes the candidate on its own.'}</dd>
                {/snippet}
              </AnswerCard>
              {#if p.plan}
                <ProposePanel preview={p.plan} request={{ namespace: r.namespace, stage: r.stage, type: p.plan.type || 'delivery.pacto.trianalab.io/promote', to: p.plan.to || p.candidate }}
                  toVersion={p.plan.toVersion || p.versions?.[p.plan.to ?? ''] || ''} />
              {/if}
            </div>
          {:else}
            <p class="t-meta" role="status">Asking the cluster why</p>
          {/if}
        </details>
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
  {#if history.error && rows.length}<p class="t-meta">Recent changes could not be read: {history.error.message || String(history.error)}</p>{/if}
  {#if loader.error}<DeliveryProblem error={loader.error} onRetry={() => loader.refresh()} />{/if}
  {@render links()}
  {#if rows.length}{@render observedStrip()}{/if}
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
  .sd-links { display: flex; flex-wrap: wrap; gap: var(--sp-1) var(--sp-3); margin: var(--sp-2) 0 0; }
  .sd-why summary { cursor: pointer; }
  .sd-why-body { display: flex; flex-direction: column; gap: var(--sp-2); margin-block-start: var(--sp-2); min-inline-size: 0; }
  .sd-reasons { margin: 0; padding-inline-start: var(--sp-4); display: flex; flex-direction: column; gap: var(--sp-1); }
  .sd-reasons li > span { display: block; min-inline-size: 0; }
  /* Observed placement: one row per environment, its parts wrapping instead of scrolling (SPEC 24). */
  .sd-placement { container-type: inline-size; display: flex; flex-direction: column; gap: var(--sp-1); margin-block-start: var(--sp-3); }
  .sd-placement h3 { margin: 0; }
  .sd-pl-list { list-style: none; margin: 0; padding: 0; }
  .sd-pl-row { display: grid; grid-template-columns: minmax(0, 1fr); gap: var(--sp-1) var(--sp-3); padding: var(--sp-2) 0; border-block-end: 1px solid var(--c-border); min-inline-size: 0; overflow-wrap: anywhere; }
  @container (min-width: 40rem) { .sd-pl-row { grid-template-columns: minmax(12ch, 14rem) minmax(8ch, max-content) minmax(0, 1fr) minmax(0, 1fr); align-items: baseline; } }
  .sd-pl-env { display: inline-flex; align-items: center; gap: var(--sp-1); min-inline-size: 0; font-weight: 600; }
  .sd-pl-env bdi { min-inline-size: 0; overflow-wrap: anywhere; }
  .sd-hatch { display: inline-block; inline-size: 0.75em; block-size: 0.75em; margin-inline-end: var(--sp-1); border-radius: 2px; vertical-align: middle; }
</style>
