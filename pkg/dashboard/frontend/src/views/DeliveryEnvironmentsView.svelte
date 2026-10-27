<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { deliveryHistoryUrl, deliveryEnvironmentsUrl, serviceUrl } from '../lib/router.ts';
  import { shortDigest, rungLabel, rungHelp, shadowLabel, heldButRan, timeLabel, sentence, sortEnvironments } from '../lib/delivery.ts';
  import { urlInt, limitParam } from '../lib/paging.ts';
  import { formatCount } from '../lib/format.ts';
  import Pager from '../components/Pager.svelte';
  import ListFilter from '../components/ListFilter.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import DeliveryNav from '../components/DeliveryNav.svelte';
  import DeliveryProblem from '../components/DeliveryProblem.svelte';
  import ListingNote from '../components/ListingNote.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import ProposePanel from '../components/ProposePanel.svelte';
  import Select from '../components/Select.svelte';
  import ShowAll from '../components/ShowAll.svelte';
  import { cap } from '../lib/cap.svelte.ts';

  // Every service in every environment: the version it runs and whether that
  // version is verified, failed, frozen or waiting. Promote and unfreeze start
  // from here, through a plan. The board pages by service on the server and
  // keeps every environment as a column on every page.
  let { offset = '', limit = '', q = '', refreshTick = 0, miraURL = '' } = $props();

  const loader = createProductLoader(() => api.deliveryEnvironments({
    offset: urlInt(offset) || undefined, limit: urlInt(limit) || undefined, q: q || undefined,
  }));
  const question = $derived(`${urlInt(offset)}@@${urlInt(limit)}@@${q}`);
  $effect(() => { loader.sync(`${question}@@${refreshTick}`, question); });
  onDestroy(() => loader.destroy());

  const data = $derived(loader.dataTag === question ? loader.data : null);
  const rows = $derived(data?.environments ?? []);
  const environments = $derived(sortEnvironments(data?.environmentNames ?? [...new Set(rows.map((r) => r.environment))]));
  // One environment at a time when thirteen columns are too many to read
  // across. ponytail: narrows the columns of this page only; the server still
  // pages by service, so a service with no Stage there shows an empty row.
  let only = $state('');
  const shown = $derived(only && environments.includes(only) ? [only] : environments);
  const services = $derived([...new Set(rows.map((r) => r.service))].sort());
  // Every cell's Stages, grouped once per answer rather than searched per cell.
  const cells = $derived(rows.reduce((m, r) => {
    const k = `${r.service}\u0000${r.environment}`;
    m.set(k, [...(m.get(k) ?? []), r]);
    return m;
  }, new Map()));
  const cell = (svc, env) => cells.get(`${svc}\u0000${env}`) ?? [];
  const href = (off, lim) => deliveryEnvironmentsUrl({ q, offset: off, limit: limitParam(lim, limit, data?.page?.limit ?? lim) });
  const serviceTotal = $derived(data?.page?.total ?? services.length);
  const observing = $derived(rows.filter((r) => r.rung === 'observe' && r.shadow?.length && shown.includes(r.environment)));
  const observingCap = cap(5);
  const VERIFICATION = { Verified: 'Verified', Unverified: 'Not verified', AcceptedUnknown: 'A person accepted the unknown' };

  // The plan being looked at: one request at a time, opened in a row right
  // under the service it is for, so the page never jumps away from it.
  // Each request is a new object, so a second click re-plans in a new panel;
  // closing it sends focus back to the button that opened it.
  let proposing = $state(null);
  let proposingVersion = $state('');
  let proposingService = $state('');
  let opener = null;
  function open(e, r, req, version = '') { opener = e.currentTarget; proposingVersion = version; proposingService = r.service; proposing = req; }
  function promote(r, e) { open(e, r, { namespace: r.namespace, stage: r.stage, type: 'delivery.pacto.trianalab.io/promote', to: r.candidate, from: r.pointer || undefined }, r.candidateVersion); }
  function unfreeze(r, e) { open(e, r, { namespace: r.namespace, stage: r.stage, type: 'delivery.pacto.trianalab.io/unfreeze' }); }
  function closePlan() { proposing = null; opener?.focus(); }
</script>

<div class="product-page">
  <PageHeader
    title="Environments"
    subtitle="Every service in every environment: the version it runs and whether that version is verified, failed, frozen or waiting. A Stage is one service in one environment."
    count={data ? `${formatCount(serviceTotal)} service${serviceTotal === 1 ? '' : 's'}, ${environments.length} environment${environments.length === 1 ? '' : 's'}` : ''}
    asOf={data?.asOf}
    revalidating={!!data && loader.loading}
  />
  <DeliveryNav current="delivery-environments" />
  <div class="ev-tools">
    <ListFilter value={q} href={(v) => deliveryEnvironmentsUrl({ q: v })} label="Filter services by name" placeholder="Service" testid="environments-filter" />
    {#if environments.length > 1}
      <Select label="Environment" value={only} options={[{ value: '', label: 'Every environment' }, ...environments]} onchange={(e) => { only = e.currentTarget.value; }} />
    {/if}
  </div>

  {#if !data && loader.loading}
    <EmptyState loading={true} />
  {:else if !data && loader.error}
    <DeliveryProblem error={loader.error} onRetry={() => loader.refresh()} />
  {:else if data}
    <ListingNote listing={data} error={loader.error} onRetry={() => loader.refresh()} />
    {#if rows.length === 0 && q}
      <div class="state-box" data-testid="environments-empty"><h2>Nothing matches the filter</h2><p>No service you can see matches “{q}”.</p></div>
    {:else if rows.length === 0}
      <div class="state-box" data-testid="environments-empty">
        <h2>No environments yet</h2>
        <p>No Stage exists {data.completeness === 'partial' ? 'that you can see' : 'in this cluster'}. A Stage is one service in one environment. Install pacto-operator with <code>delivery.enabled=true</code>, then create one.</p>
      </div>
    {:else}
      <p class="t-meta ev-legend" data-testid="environments-legend"><span class="ev-none" aria-hidden="true">–</span> not delivered in that environment. A version opens its history.{#if shown.length > 6}{' '}Scroll sideways for more environments, or pick one above.{/if}</p>
      <div class="ev-board">
        <table class="data-table ev-table" data-testid="environments-board" style:min-width={`calc(13rem + ${shown.length} * 12rem)`}>
          <caption class="visually-hidden">Services by environment</caption>
          <colgroup><col style="width: 13rem" />{#each shown as env (env)}<col style={shown.length > 1 ? 'width: 12rem' : undefined} />{/each}</colgroup>
          <thead>
            <tr><th scope="col">Service</th>{#each shown as env (env)}<th scope="col"><span class="cell-clip" title={env}>{env}</span></th>{/each}</tr>
          </thead>
          <tbody>
            {#each services as svc (svc)}
              <tr data-testid={`env-row-${svc}`}>
                <th scope="row"><a class="cell-clip" href={serviceUrl(svc)} title={svc}>{svc}</a></th>
                {#each shown as env (env)}
                  {@const here = cell(svc, env)}
                  <td data-label={env}>
                    {#each here as r (r.namespace + '/' + r.stage)}
                      <div class="ev-cell" data-testid="env-cell" data-stage={`${r.namespace}/${r.stage}`}>
                        {#if here.length > 1}<span class="t-meta ev-stage">{r.namespace}/{r.stage}</span>{/if}
                        <span class="ev-head">
                          <a class="ev-version" href={deliveryHistoryUrl({ namespace: r.namespace, stage: r.stage })}
                            title={r.pointer || 'nothing deployed yet'}>{r.version || shortDigest(r.pointer) || 'Nothing yet'}</a>
                          {#if r.rung}<span class="t-meta ev-rung" data-testid="rung" title={rungHelp(r.rung)}>{rungLabel(r.rung)}</span>{/if}
                        </span>
                        <span class="ev-badges">
                          {#if r.verified}<span class="badge badge-ok">Verified</span>{/if}
                          {#if r.failed}<span class="badge badge-err">Failed</span>{/if}
                          {#if r.frozen}<span class="badge badge-warn" title={r.frozenReason || 'Nothing moves here until a person unfreezes it'}>Frozen</span>{/if}
                          {#if r.autoHold}<span class="badge badge-info" title="A person or agent rolled this environment back or promoted it back to a version it already verified, so automation does not promote here until a person unfreezes it or promotes a version it has not verified yet">Auto-hold</span>{/if}
                          {#if r.held}<span class="badge badge-warn">{r.held} waiting</span>{/if}
                          {#if r.pointer && !r.verified && !r.failed}<span class="badge badge-neutral">Not verified</span>{/if}
                        </span>
                        <!-- svelte-ignore a11y_no_noninteractive_tabindex (focus only unclamps the reason for a keyboard reader; it is whole in the DOM for a screen reader) -->
                        {#if r.ready && !r.ready.startsWith('True')}{@const why = `Cannot promote: ${sentence(r.readyMessage || r.ready.replace(/^\w+: /, ''))}`}<span class="t-meta ev-ready" title={why} tabindex="0">{why}</span>{/if}
                        {#if r.actions?.length}
                          <span class="ev-actions">
                            {#if r.actions.includes('promote')}
                              <button type="button" class="btn btn-sm" onclick={(e) => promote(r, e)} aria-label={`Promote ${svc} in ${env} to ${r.candidateVersion || shortDigest(r.candidate)}`} title={`Promote to ${r.candidateVersion || r.candidate}`}>Promote</button>
                            {/if}
                            {#if r.actions.includes('unfreeze')}
                              <button type="button" class="btn btn-sm" onclick={(e) => unfreeze(r, e)} aria-label={`Unfreeze ${svc} in ${env}`}>Unfreeze</button>
                            {/if}
                          </span>
                        {/if}
                      </div>
                    {:else}
                      <span class="ev-none" title="Not delivered here"><span aria-hidden="true">–</span><span class="visually-hidden">Not delivered here</span></span>
                    {/each}
                  </td>
                {/each}
              </tr>
              {#if proposing && proposingService === svc}
                <tr class="ev-plan-row"><td colspan={shown.length + 1}>
                  <div class="ev-plan">
                    {#key proposing}
                      <ProposePanel request={proposing} toVersion={proposingVersion} {miraURL} onClose={closePlan} />
                    {/key}
                  </div>
                </td></tr>
              {/if}
            {/each}
          </tbody>
        </table>
      </div>
      <Pager page={data.page} {href} label="Pages of services" noun="services" testid="environments-pager" />
      {#if observing.length}
        <section class="ev-shadows" data-testid="shadow-summary" aria-labelledby="ev-shadows-title">
          <h2 id="ev-shadows-title" class="t-section-title">What Pacto would have done <span class="t-meta">{formatCount(observing.length)} observing Stage{observing.length === 1 ? '' : 's'} on this page</span></h2>
          <p class="t-body-2">These Stages observe: GitOps applies and Pacto moves nothing. Each row is what Pacto would have done with a candidate on Autonomous, next to what happened. It is advisory and unsigned: status, not a record. "Source reported at" is when GitOps' source showed the candidate, not when it rolled out.</p>
          {#each observingCap.slice(observing) as r (r.namespace + '/' + r.stage)}
            <details class="ev-shadow disclosure" data-testid="shadow" data-stage={`${r.namespace}/${r.stage}`}>
              <summary>
                <span class="disclosure-caret" data-motion aria-hidden="true">&#9656;</span>
                <h3 id={`shadow-${r.namespace}-${r.stage}`} class="ev-shadow-title">{r.service} in {r.environment}</h3>
                <span class="t-meta">{formatCount(r.shadow.length)} candidate{r.shadow.length === 1 ? '' : 's'}</span>
              </summary>
              <div class="ev-scroll">
                <table class="ev-table">
                  <caption class="visually-hidden">What Pacto would have done with each candidate on {r.service} in {r.environment}, newest first</caption>
                  <thead><tr><th scope="col">Candidate</th><th scope="col">On Autonomous, Pacto would have</th><th scope="col">Source reported at</th><th scope="col">Verification</th></tr></thead>
                  <tbody>
                    {#each r.shadow as sh (sh.candidate + (sh.intentDigest ?? ''))}
                      <tr class:ev-attention={heldButRan(sh)} data-testid="shadow-row">
                        <td><code title={sh.candidate}>{shortDigest(sh.candidate)}</code></td>
                        <td>
                          {shadowLabel(sh)}
                          {#if heldButRan(sh)}<span class="badge badge-warn" data-testid="held-but-ran">GitOps ran it anyway</span>{/if}
                        </td>
                        <td>{sh.observedAt ? timeLabel(sh.observedAt) : 'Not reported yet'}</td>
                        <td>{VERIFICATION[sh.verification] ?? 'Not checked yet'}</td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
            </details>
          {/each}
          <ShowAll cap={observingCap} total={observing.length} noun="observing Stages" testid="shadow-more" />
        </section>
      {/if}
    {/if}
  {/if}
</div>

<style>
  .ev-scroll { overflow-x: auto; }
  .ev-table { background: var(--c-surface); border-collapse: collapse; width: 100%; font-size: var(--text-sm); }
  .ev-table th, .ev-table td { border-bottom: 1px solid var(--c-border); padding: var(--sp-2) var(--sp-3); text-align: left; vertical-align: top; }
  .ev-table thead th { color: var(--c-text-3); font-weight: 600; }
  /* The service column stays in view while the environments scroll sideways. */
  .ev-board .ev-table thead th:first-child, .ev-board .ev-table tbody th { position: sticky; left: 0; background: var(--c-surface); z-index: 1; }
  .ev-board .ev-table thead th:first-child { z-index: 3; }
  .ev-plan-row td { background: var(--c-surface-inset); }
  .ev-plan { position: sticky; left: var(--sp-3); max-width: min(60rem, calc(100vw - 4 * var(--sp-4))); }
  .ev-stage { color: var(--c-text-2); font-family: var(--font-mono); }
  .ev-tools { display: flex; flex-wrap: wrap; gap: var(--sp-3); align-items: center; justify-content: space-between; }
  .ev-legend { margin: 0; }
  /* Page scroll down, sideways scroll only when the columns need it: no inner box that hides rows. */
  /* relative: the visually hidden labels in off-screen cells are absolute and would widen the page. */
  .ev-board { position: relative; overflow-x: auto; border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); }
  .ev-none { color: var(--c-text-3); }
  .ev-shadows { display: flex; flex-direction: column; gap: var(--sp-2); }
  .ev-shadows > h2, .ev-shadows > p { margin: 0; }
  .ev-shadow summary { display: flex; align-items: baseline; gap: var(--sp-2); cursor: pointer; }
  .ev-shadow-title { margin: 0; font-size: var(--text-sm); font-weight: 600; }
  .ev-shadow[open] > :not(summary) { margin-top: var(--sp-2); }
  @media (max-width: 720px) {
    .ev-board .ev-table { min-width: 0 !important; }
    .ev-board .ev-table tbody th { position: static; }
  }
  .ev-table th a, .ev-version { color: var(--c-accent); }
  .ev-cell { display: flex; flex-direction: column; gap: var(--sp-1); }
  .ev-cell + .ev-cell { margin-top: var(--sp-2); }
  .ev-version { font-weight: 600; }
  .ev-badges, .ev-actions { display: flex; gap: var(--sp-1); flex-wrap: wrap; }
  .ev-ready { color: var(--c-warn); display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
  /* Two lines until a reader points at it or tabs to it; then the whole reason. */
  .ev-ready:hover, .ev-ready:focus-visible { -webkit-line-clamp: unset; line-clamp: none; }
  .ev-head { display: flex; flex-wrap: wrap; align-items: baseline; column-gap: var(--sp-2); }
  .ev-head .ev-version { overflow-wrap: anywhere; }
  .ev-tools > :global(form) { flex: 1 1 24rem; }
  .ev-rung { white-space: nowrap; }
  .ev-attention td:first-child { box-shadow: inset 3px 0 0 var(--c-warn); }
  .state-box { display: flex; flex-direction: column; align-items: center; gap: var(--sp-2); padding: var(--sp-8) var(--sp-4); text-align: center; color: var(--c-text-3); }
  .state-box h2 { color: var(--c-text-2); }
</style>
