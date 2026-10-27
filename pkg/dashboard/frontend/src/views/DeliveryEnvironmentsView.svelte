<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { deliveryHistoryUrl, deliveryEnvironmentsUrl, serviceUrl } from '../lib/router.ts';
  import { shortDigest, levelLabel, levelHelp, shadowLabel, heldButRan, timeLabel, sentence, andList } from '../lib/delivery.ts';
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
  // keeps every environment as a column on every page. Past a few columns
  // each service is one row of status dots, one per environment, so 13 or 50
  // environments never scroll sideways; a dot opens the full cell.
  let { offset = '', limit = '', q = '', environment = '', tier = '', refreshTick = 0, miraURL = '' } = $props();

  const loader = createProductLoader(() => api.deliveryEnvironments({
    offset: urlInt(offset) || undefined, limit: urlInt(limit) || undefined, q: q || undefined,
    environment: environment || undefined, tier: tier || undefined,
  }));
  const question = $derived(`${urlInt(offset)}@@${urlInt(limit)}@@${q}@@${environment}@@${tier}`);
  // The same question without its page: turning a page keeps the rows on screen.
  const listQuestion = $derived(`${q}@@${environment}@@${tier}`);
  $effect(() => { loader.sync(`${question}@@${refreshTick}`, question, listQuestion); });
  onDestroy(() => loader.destroy());

  const answer = $derived(loader.answer(question, listQuestion));
  const data = $derived(answer.data);
  const rows = $derived(data?.environments ?? []);
  // The server orders environments the way changes travel and filters the
  // rows; the columns follow the same environment and tier.
  const environments = $derived(data?.environmentNames ?? [...new Set(rows.map((r) => r.environment))]);
  const tiers = $derived(data?.environmentTiers ?? {});
  const shown = $derived(environment ? [environment] : tier ? environments.filter((e) => tiers[e] === tier) : environments);
  const tierOptions = $derived(['dev', 'test', 'prod'].filter((t) => Object.values(tiers).includes(t)));
  const filtered = $derived(!!(q || environment || tier));
  const go = (over) => { location.hash = deliveryEnvironmentsUrl({ q, environment, tier, ...over }); };
  // A column a person can read is 12rem: past four the board becomes the strip.
  const BOARD_MAX = 4;
  const strip = $derived(shown.length > BOARD_MAX);
  // The strip's groups: consecutive environments of one tier (the server
  // orders them dev, test, prod, then the names it cannot place).
  const groups = $derived(shown.reduce((gs, e) => {
    const t = tiers[e] ?? '';
    if (gs.at(-1)?.tier === t) gs.at(-1).envs.push(e); else gs.push({ tier: t, envs: [e] });
    return gs;
  }, []));
  // One state per dot, the first that holds for any Stage in the cell: the
  // worst wins. The legend and the summary column read the same list.
  const STATES = [
    { key: 'failed', label: 'Failed', tone: 'err', is: (r) => r.failed },
    { key: 'frozen', label: 'Frozen', tone: 'warn', is: (r) => r.frozen },
    { key: 'autohold', label: 'Auto-hold', tone: 'info', is: (r) => r.autoHold },
    { key: 'waiting', label: 'Waiting', tone: 'warn', is: (r) => r.held > 0 },
    { key: 'unverified', label: 'Not verified', tone: 'neutral', is: (r) => !!r.pointer && !r.verified },
    { key: 'empty', label: 'Nothing yet', tone: 'neutral', is: (r) => !r.pointer },
    { key: 'verified', label: 'Verified', tone: 'ok', is: () => true },
  ];
  const stateOf = (here) => STATES.find((s) => here.some(s.is));
  const summaryOf = (svc) => {
    const n = new Map();
    for (const e of shown) { const here = cell(svc, e); if (here.length) { const k = stateOf(here).key; n.set(k, (n.get(k) ?? 0) + 1); } }
    return STATES.filter((s) => n.has(s.key)).map((s) => ({ ...s, n: n.get(s.key) }));
  };
  const dotTitle = (env, here) => {
    const vs = here.filter((r) => r.pointer).map((r) => r.version || shortDigest(r.pointer)).join(', ');
    return `${env}: ${vs ? `${vs}. ` : ''}${stateOf(here).label}${here.length > 1 ? ` (${here.length} Stages)` : ''}`;
  };
  // The cell a dot opened, under its service's row.
  let opened = $state(null);
  const toggle = (svc, env) => { opened = opened?.svc === svc && opened.env === env ? null : { svc, env }; };
  const cellId = (svc) => `ev-detail-${svc}`;
  // One tab stop per row: the arrow keys move along the dots.
  function roam(e) {
    const step = { ArrowLeft: -1, ArrowRight: 1, Home: -Infinity, End: Infinity }[e.key];
    if (!step) return;
    const dots = [...e.currentTarget.querySelectorAll('button')];
    const i = Math.max(0, Math.min(dots.length - 1, dots.indexOf(document.activeElement) + step));
    dots[i]?.focus();
    e.preventDefault();
  }
  const services = $derived([...new Set(rows.map((r) => r.service))].sort());
  // Every cell's Stages, grouped once per answer rather than searched per cell.
  const cells = $derived(rows.reduce((m, r) => {
    const k = `${r.service}\u0000${r.environment}`;
    m.set(k, [...(m.get(k) ?? []), r]);
    return m;
  }, new Map()));
  const cell = (svc, env) => cells.get(`${svc}\u0000${env}`) ?? [];
  const href = (off, lim) => deliveryEnvironmentsUrl({ q, environment, tier, offset: off, limit: limitParam(lim, limit, data?.page?.limit ?? lim) });
  const serviceTotal = $derived(data?.page?.total ?? services.length);
  const observing = $derived(rows.filter((r) => r.level === 'observe' && r.shadow?.length && shown.includes(r.environment)));
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

{#snippet stack(svc, env)}
    {#each cell(svc, env) as r (r.namespace + '/' + r.stage)}
      <div class="ev-cell" data-testid="env-cell" data-stage={`${r.namespace}/${r.stage}`}>
        {#if cell(svc, env).length > 1}<span class="t-meta ev-stage">{r.namespace}/{r.stage}</span>{/if}
        <span class="ev-head">
          <a class="ev-version" href={deliveryHistoryUrl({ namespace: r.namespace, stage: r.stage })}
            title={r.pointer || 'nothing deployed yet'}>{r.version || shortDigest(r.pointer) || 'Nothing yet'}</a>
          {#if r.level}<span class="t-meta ev-level" data-testid="level" title={levelHelp(r.level)}>{levelLabel(r.level)}</span>{/if}
        </span>
        <span class="ev-badges">
          {#if r.verified}<span class="badge badge-ok">Verified</span>{/if}
          {#if r.failed}<span class="badge badge-err">Failed</span>{/if}
          {#if r.frozen}<span class="badge badge-warn" title={r.frozenReason || 'Nothing moves here until a person unfreezes it'}>Frozen</span>{/if}
          {#if r.autoHold}<span class="badge badge-info" title="A person or agent rolled this environment back or promoted it back to a version it already verified, so automation does not promote here until a person unfreezes it or promotes a version it has not verified yet">Auto-hold</span>{/if}
          {#if r.held}<span class="badge badge-warn">{r.held} waiting</span>{/if}
          {#if r.pointer && !r.verified && !r.failed}<span class="badge badge-neutral">Not verified</span>{/if}
        </span>
        {#if r.frozen && r.frozenReason}<span class="t-meta ev-frozen">Frozen: {sentence(r.frozenReason)}</span>{/if}
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
{/snippet}

<div class="product-page">
  <PageHeader
    title="Environments"
    subtitle="Every service in every environment: the version it runs and whether that version is verified, failed, frozen or waiting. A Stage is one service in one environment."
    count={data ? `${formatCount(serviceTotal)} service${serviceTotal === 1 ? '' : 's'}, ${environments.length} environment${environments.length === 1 ? '' : 's'}` : ''}
    asOf={data?.asOf}
    revalidating={!!data && loader.loading}
    paging={answer.paging}
  />
  <DeliveryNav current="delivery-environments" />
  <div class="ev-tools">
    <ListFilter value={q} href={(v) => deliveryEnvironmentsUrl({ q: v, environment, tier })} label="Filter services by name" placeholder="Service" testid="environments-filter" />
    <span class="ev-picks">
      {#if tierOptions.length > 1 || tier}
        <Select label="Tier" value={tier} options={[{ value: '', label: 'Every tier' }, ...tierOptions]} onchange={(e) => go({ tier: e.currentTarget.value, environment: '' })} />
      {/if}
      {#if environments.length > 1 || environment}
        <Select label="Environment" value={environment} options={[{ value: '', label: 'Every environment' }, ...environments]} onchange={(e) => go({ environment: e.currentTarget.value, tier: '' })} />
      {/if}
    </span>
  </div>

  {#if !data && loader.loading}
    <EmptyState loading={true} />
  {:else if !data && loader.error}
    <DeliveryProblem error={loader.error} onRetry={() => loader.refresh()} />
  {:else if data}
    <ListingNote listing={data} error={loader.error} onRetry={() => loader.refresh()} />
    {#if rows.length === 0 && filtered}
      <div class="state-box" data-testid="environments-empty"><h2>Nothing matches the filter</h2><p>No service you can see{q ? ` matches “${q}”` : ''}{environment ? ` has a Stage in ${environment}` : ''}{tier ? ` has a Stage in a ${tier} environment` : ''}.</p></div>
    {:else if rows.length === 0}
      <div class="state-box" data-testid="environments-empty">
        <h2>No environments yet</h2>
        <p>No Stage exists {data.completeness === 'partial' ? 'that you can see' : 'in this cluster'}. A Stage is one service in one environment. Install pacto-operator with <code>delivery.enabled=true</code>, then create one.</p>
      </div>
    {:else}
      {#if strip}
        <div class="t-meta ev-legend ev-key" data-testid="environments-legend">
          {#each STATES as st (st.key)}<span><span class={`ev-dot s-${st.key}`} aria-hidden="true"></span>{st.label}</span>{/each}
          <span><span class="ev-dot s-none" aria-hidden="true"></span>Not delivered</span>
          <span>Dots run {andList(groups.map((g) => g.tier || 'other'))}, left to right. Hover a dot for its version, click it for the rest.</span>
        </div>
        <div class="ev-board" style:--n={shown.length} style:--groups={groups.length - 1}>
          <table class="data-table ev-table ev-strip" data-testid="environments-board">
            <caption class="visually-hidden">Services by environment, one dot per environment</caption>
            <colgroup><col style="width: 12rem" /><col /><col style="width: 15rem" /></colgroup>
            <thead>
              <tr>
                <th scope="col">Service</th>
                <th scope="col">
                  <span class="visually-hidden">Environments</span>
                  <div class="ev-dots" aria-hidden="true">
                    {#each groups as g, i (i)}
                      <span class="ev-group ev-group-head" style:--k={g.envs.length} title={`${g.tier || 'other'}: ${g.envs.join(', ')}`}>{g.tier || 'other'}</span>
                    {/each}
                  </div>
                </th>
                <th scope="col">State <span class="t-meta">in {shown.length} environments</span></th>
              </tr>
            </thead>
            <tbody>
              {#each services as svc (svc)}
                {@const sum = summaryOf(svc)}
                {@const first = shown.find((e) => cell(svc, e).length)}
                <tr data-testid={`env-row-${svc}`}>
                  <th scope="row"><a class="cell-clip" href={serviceUrl(svc)} title={svc}>{svc}</a></th>
                  <td>
                    <div class="ev-dots" role="toolbar" aria-label={`Environments of ${svc}`} tabindex="-1" onkeydown={roam}>
                      {#each groups as g, i (i)}
                        <span class="ev-group">
                          {#each g.envs as env (env)}
                            {@const here = cell(svc, env)}
                            {#if here.length}
                              {@const st = stateOf(here)}
                              {@const open = opened?.svc === svc && opened.env === env}
                              <button type="button" class={`ev-dot s-${st.key}`} class:open data-testid="env-dot" data-env={env}
                                tabindex={env === first ? 0 : -1} title={dotTitle(env, here)} aria-label={dotTitle(env, here)}
                                aria-expanded={open} aria-controls={open ? cellId(svc) : undefined} onclick={() => toggle(svc, env)}></button>
                            {:else}
                              <span class="ev-dot s-none" title={`${env}: not delivered here`} aria-hidden="true"></span>
                            {/if}
                          {/each}
                        </span>
                      {/each}
                    </div>
                  </td>
                  <td class="ev-sum" data-testid="env-summary">
                    {#each sum as st (st.key)}<span class={`badge badge-${st.tone}`}>{st.n} {st.label.toLowerCase()}</span>{/each}
                    {#if !sum.length}<span class="t-meta">Not delivered in these environments</span>{/if}
                  </td>
                </tr>
                {#if opened?.svc === svc}
                  <tr class="ev-plan-row"><td colspan="3" id={cellId(svc)}>
                    <div class="ev-detail" data-testid="env-detail">
                      <div class="ev-detail-head">
                        <h3 class="ev-shadow-title">{svc} in {opened.env}</h3>
                        <button type="button" class="btn btn-sm" onclick={() => { opened = null; }} aria-label={`Close ${svc} in ${opened.env}`}>Close</button>
                      </div>
                      {@render stack(svc, opened.env)}
                    </div>
                  </td></tr>
                {/if}
                {#if proposing && proposingService === svc}
                  <tr class="ev-plan-row"><td colspan="3">
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
      {:else}
        <p class="t-meta ev-legend" data-testid="environments-legend"><span class="ev-none" aria-hidden="true">–</span> not delivered in that environment. A version opens its history.</p>
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
                      {@render stack(svc, env)}
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
      {/if}
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
  .ev-picks { display: flex; flex-wrap: wrap; gap: var(--sp-3); }
  .ev-key { display: flex; flex-wrap: wrap; align-items: center; gap: var(--sp-1) var(--sp-3); }
  .ev-key > span { display: inline-flex; align-items: center; gap: var(--sp-1); }
  /* The strip: every row and the header share one dot size, worked out from
     the column's width, so dots line up down the page and never scroll. */
  .ev-dots { container-type: inline-size; display: flex; gap: var(--ev-gg); --ev-g: 3px; --ev-gg: 10px;
    --ev-d: clamp(6px, calc((100cqw - (var(--n) - 1) * var(--ev-g) - var(--groups) * (var(--ev-gg) - var(--ev-g))) / var(--n)), 16px); }
  /* A group is never narrower than its label; the header and the rows share the floor, so they still line up. */
  .ev-group { display: flex; gap: var(--ev-g); flex: none; min-width: 2.25rem; }
  .ev-group-head { display: block; width: max(2.25rem, calc(var(--k) * var(--ev-d) + (var(--k) - 1) * var(--ev-g))); overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
    border-bottom: 2px solid var(--c-border); font-size: var(--text-xs); text-transform: none; letter-spacing: normal; font-variant: normal; }
  .ev-dot { display: inline-block; flex: none; width: var(--ev-d, 12px); height: var(--ev-d, 12px); max-width: 16px; max-height: 16px; min-width: 6px; min-height: 6px;
    padding: 0; border: 2px solid transparent; border-radius: 50%; background: none; cursor: pointer; box-sizing: border-box; }
  .ev-key .ev-dot { width: 12px; height: 12px; cursor: default; }
  .s-verified { background: var(--c-ok); }
  .s-waiting { background: var(--c-warn); }
  .s-failed { background: var(--c-err); border-radius: 2px; transform: rotate(45deg) scale(0.85); }
  .s-frozen { background: var(--c-warn); border-radius: 2px; }
  .s-autohold { background: var(--c-info); border-radius: 2px; }
  .s-unverified { border-color: var(--c-text-3); }
  .s-empty { border: 2px dashed var(--c-text-3); }
  .s-none { cursor: default; border: none; border-radius: 0; background: linear-gradient(var(--c-border), var(--c-border)) center / 70% 2px no-repeat; }
  button.ev-dot:hover, button.ev-dot.open { outline: 2px solid var(--c-accent); outline-offset: 1px; }
  .ev-sum { display: flex; flex-wrap: wrap; gap: var(--sp-1); }
  .ev-strip td { vertical-align: middle; }
  .ev-detail { display: flex; flex-direction: column; gap: var(--sp-2); max-width: 40rem; }
  .ev-detail .ev-ready { display: block; overflow: visible; }
  .ev-detail-head { display: flex; align-items: center; justify-content: space-between; gap: var(--sp-2); }
  .ev-shadows { display: flex; flex-direction: column; gap: var(--sp-2); }
  .ev-shadows > h2, .ev-shadows > p { margin: 0; }
  .ev-shadow summary { display: flex; align-items: baseline; gap: var(--sp-2); cursor: pointer; }
  .ev-shadow-title { margin: 0; font-size: var(--text-sm); font-weight: 600; }
  .ev-shadow[open] > :not(summary) { margin-top: var(--sp-2); }
  @media (max-width: 720px) {
    .ev-board .ev-table { min-width: 0 !important; }
    .ev-board .ev-table tbody th { position: static; }
    /* A phone stacks the rows into cards with no header to line up with: the dots wrap at a size a finger can hit. */
    .ev-dots, .ev-group { flex-wrap: wrap; max-width: 100%; }
    .ev-group-head { display: none; }
    .ev-dots { --ev-d: 14px; --ev-g: 6px; --ev-gg: 12px; }
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
  .ev-level { white-space: nowrap; }
  .ev-attention td:first-child { box-shadow: inset 3px 0 0 var(--c-warn); }
  .state-box { display: flex; flex-direction: column; align-items: center; gap: var(--sp-2); padding: var(--sp-8) var(--sp-4); text-align: center; color: var(--c-text-3); }
  .state-box h2 { color: var(--c-text-2); }
</style>
