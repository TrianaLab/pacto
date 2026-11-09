<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { deliveryHistoryUrl, deliveryEnvironmentsUrl } from '../lib/router.ts';
  import { deliveryServiceUrl } from '../lib/deliveryLinks.svelte.ts';
  import { shortDigest, levelLabel, levelHelp, shadowLabel, heldButRan, timeLabel, sentence, andList, exceptList, exceptParam } from '../lib/delivery.ts';
  import { urlInt, limitParam } from '../lib/paging.ts';
  import { formatCount, countOf } from '../lib/format.ts';
  import Pager from '../components/Pager.svelte';
  import ListFilter from '../components/ListFilter.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import DeliveryNav from '../components/DeliveryNav.svelte';
  import DeliveryProblem from '../components/DeliveryProblem.svelte';
  import DeliveryBadges from '../components/DeliveryBadges.svelte';
  import ListingNote from '../components/ListingNote.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import ProposePanel from '../components/ProposePanel.svelte';
  import Select from '../components/Select.svelte';
  import ShowAll from '../components/ShowAll.svelte';
  import DeliveryLead from '../components/DeliveryLead.svelte';
  import DeliveryServiceFilter from '../components/DeliveryServiceFilter.svelte';
  import HelpTip from '../components/HelpTip.svelte';
  import { cap } from '../lib/cap.svelte.ts';
  import EnvFleetFigure from '../components/figure/EnvFleetFigure.svelte';
  import PromotionFigure from '../components/figure/PromotionFigure.svelte';
  import { ENV_STATES, servicePath, sourceProblem, MAX_PATH_MARKS } from '../lib/envFigure.ts';

  // Every service in every environment: the version it runs and whether that
  // version is verified, failed, frozen or waiting. Promote and unfreeze start
  // from here, through a plan. The board pages by service on the server. Each
  // service is one row along its own path, in the order its Stages declare
  // (SPEC 21 r3), so no column claims an order the services do not share: up
  // to four environments the row holds the whole cells, past four a strip of
  // status marks, each opening its full cell. Above it, a
  // figure: every environment's Stages by state, or with one service, that
  // service's own promotion path (SPEC 21).
  let { offset = '', limit = '', q = '', environment = '', tier = '', state: stateFilter = '', service = '', domain = '', except = '', refreshTick = 0, miraURL = '' } = $props();
  // The figure's folded station: every environment but the ones it draws.
  const exceptEnvs = $derived(exceptList(except));

  const loader = createProductLoader(() => api.deliveryEnvironments({
    offset: urlInt(offset) || undefined, limit: urlInt(limit) || undefined, q: q || undefined,
    environment: environment || undefined, tier: tier || undefined, service: service || undefined, domain: domain || undefined,
    except: exceptEnvs.length ? exceptEnvs : undefined,
    // The router passes any string; the server refuses a state it does not know.
    state: /** @type {any} */ (stateFilter || undefined),
  }));
  const question = $derived(`${urlInt(offset)}@@${urlInt(limit)}@@${q}@@${environment}@@${tier}@@${stateFilter}@@${domain}/${service}@@${except}`);
  // The same question without its page: turning a page keeps the rows on screen.
  const listQuestion = $derived(`${q}@@${environment}@@${tier}@@${stateFilter}@@${domain}/${service}@@${except}`);
  $effect(() => { loader.sync(`${question}@@${refreshTick}`, question, listQuestion); });
  onDestroy(() => loader.destroy());

  const answer = $derived(loader.answer(question, listQuestion));
  const data = $derived(answer.data);
  const rows = $derived(data?.environments ?? []);
  // The server orders environments as the Stages declare their upstreams and
  // filters the rows; the columns follow the same environment and tier.
  const environments = $derived(data?.environmentNames ?? [...new Set(rows.map((r) => r.environment))]);
  const tiers = $derived(data?.environmentTiers ?? {});
  const shown = $derived((environment ? [environment] : tier ? environments.filter((e) => tiers[e] === tier) : environments).filter((e) => !exceptEnvs.includes(e)));
  const tierOptions = $derived(['dev', 'test', 'prod'].filter((t) => Object.values(tiers).includes(t)));
  const filtered = $derived(!!(q || environment || tier || stateFilter || service || exceptEnvs.length));
  const go = (over) => { location.hash = deliveryEnvironmentsUrl({ q, environment, tier, state: stateFilter, service, domain, except: except || undefined, ...over }); };
  // A figure count lands on the board filtered to its state and environment; the folded
  // station's, on every environment but the ones the figure draws.
  const stateHref = (st, env, ex) => deliveryEnvironmentsUrl({ q, service, domain, state: st || undefined, environment: env || undefined, except: exceptParam(ex) });
  // A cell a person can read is 12rem: past four environments the cells become the strip.
  const BOARD_MAX = 4;
  const strip = $derived(shown.length > BOARD_MAX);
  // Neither form has shared columns (SPEC 21 r3): services promote along their own
  // paths, so each row is its service's own chain in the order its Stages declare.
  // The strip shows up to MAX_PATH_MARKS marks, then a count chip to the whole path.
  const pathOf = (svc) => servicePath(rows.filter((r) => r.service === svc), shown.filter((e) => cell(svc, e).length));
  // A tier is inferred from the name only, so it reads as a guess, never as the order.
  const named = (env) => (tiers[env] ? ` (named like ${tiers[env]})` : '');
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
  // SPEC 21 r4: a Stage with no source Pacto can follow says so, with the reason.
  const deadOf = (here) => here.map(sourceProblem).filter(Boolean);
  const dotTitle = (env, here, fedBy = []) => {
    const vs = here.filter((r) => r.pointer).map((r) => r.version || shortDigest(r.pointer)).join(', ');
    const dead = deadOf(here);
    return `${env}${named(env)}: ${vs ? `${vs}. ` : ''}${stateOf(here).label}${here.length > 1 ? ` (${formatCount(here.length)} Stages)` : ''}${fedBy.length ? `. Fed by ${andList(fedBy, 3)}` : ''}${dead.length ? `. ${dead.join('. ')}` : ''}`;
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
  const href = (off, lim) => deliveryEnvironmentsUrl({ q, environment, tier, state: stateFilter, service, domain, except: except || undefined, offset: off, limit: limitParam(lim, limit, data?.page?.limit ?? lim) });
  const serviceTotal = $derived(data?.page?.total ?? services.length);
  const observing = $derived(rows.filter((r) => r.level === 'observe' && r.shadow?.length && shown.includes(r.environment)));
  const observingCap = cap(5);
  // The page's answer: what failed or froze, and what waits for a person, in
  // the environments shown, over every page: the server counts them. Without
  // its counts the page's own are said to be the page's.
  const live = $derived(rows.filter((r) => shown.includes(r.environment)));
  const onPage = $derived(!data?.counts && (data?.page?.total ?? 0) > services.length);
  const lead = $derived.by(() => {
    const c = data?.counts ?? {
      failed: live.filter((r) => r.failed).length,
      frozen: live.filter((r) => r.frozen).length,
      waiting: live.filter((r) => r.held > 0).length,
    };
    const bad = [c.failed && `${formatCount(c.failed)} failed`, c.frozen && `${formatCount(c.frozen)} frozen`].filter(Boolean);
    const head = `${bad.length ? andList(bad) : 'Nothing failed or frozen'}${onPage ? '' : '.'}`;
    const rest = `${onPage ? 'on this page.' : ''}${c.waiting ? ` ${countOf(c.waiting, 'Stage')} ${c.waiting === 1 ? 'waits' : 'wait'} for a person.` : ''}`.trim();
    return { head, rest };
  });
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
        <DeliveryBadges row={r} />
        {#if r.frozen && r.frozenReason}<span class="t-meta ev-frozen">Frozen: {sentence(r.frozenReason)}</span>{/if}
        <!-- svelte-ignore a11y_no_noninteractive_tabindex (focus only unclamps the reason for a keyboard reader; it is whole in the DOM for a screen reader) -->
        {#if r.ready && !r.ready.startsWith('True')}{@const why = `Cannot promote: ${sentence(r.readyMessage || r.ready.replace(/^\w+: /, ''))}`}<span class="t-meta ev-ready" title={why} tabindex="0">{why}</span>
        {:else if sourceProblem(r)}<!-- svelte-ignore a11y_no_noninteractive_tabindex (as above) --><span class="t-meta ev-ready" title={sourceProblem(r)} tabindex="0" data-testid="env-no-source">{sourceProblem(r)}</span>{/if}
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
    {/each}
{/snippet}

<div class="product-page">
  <PageHeader
    title="Stages"
    count={data ? `${countOf(serviceTotal, 'service')}, ${countOf(environments.length, 'environment')}` : ''}
    asOf={data?.asOf}
    revalidating={!!data && loader.loading}
    paging={answer.paging}
  >
    {#if data && rows.length}
      <DeliveryLead head={lead.head} rest={lead.rest} tipLabel="Stages"
        tip="Every service in every environment: the version it runs and whether it is verified, failed, frozen or waiting. A Stage is one service in one environment." />
    {/if}
  </PageHeader>
  <DeliveryNav current="delivery-environments" />
  <DeliveryServiceFilter {service} {domain} clear={deliveryEnvironmentsUrl({ q, environment, tier, state: stateFilter })} />
  <div class="ev-tools">
    <ListFilter value={q} href={(v) => deliveryEnvironmentsUrl({ q: v, environment, tier, state: stateFilter, service, domain })} label="Filter services by name" placeholder="Service" testid="environments-filter" />
    <span class="ev-picks">
      {#if tierOptions.length > 1 || tier}
        <Select label="Tier, inferred from names" value={tier} options={[{ value: '', label: 'Every tier' }, ...tierOptions.map((t) => ({ value: t, label: `Named like ${t}` }))]} onchange={(e) => go({ tier: e.currentTarget.value, environment: '', except: '' })} />
      {/if}
      {#if environments.length > 1 || environment}
        <Select label="Environment" value={environment} options={[{ value: '', label: 'Every environment' }, ...environments]} onchange={(e) => go({ environment: e.currentTarget.value, tier: '', except: '' })} />
      {/if}
      <Select label="State" value={stateFilter} options={[{ value: '', label: 'Any state' }, ...ENV_STATES.map((s) => ({ value: s.key, label: s.word[0].toUpperCase() + s.word.slice(1) }))]} onchange={(e) => go({ state: e.currentTarget.value })} />
    </span>
  </div>
  {#if exceptEnvs.length}
    <p class="t-body-2" data-testid="environments-except">Every environment but {andList(exceptEnvs, 3)}.
      <a href={deliveryEnvironmentsUrl({ q, tier, state: stateFilter, service, domain })}>Show every environment</a></p>
  {/if}

  {#if !data && loader.loading}
    <EmptyState loading={true} />
  {:else if !data && loader.error}
    <DeliveryProblem error={loader.error} onRetry={() => loader.refresh()} />
  {:else if data}
    <ListingNote listing={data} error={loader.error} onRetry={() => loader.refresh()} />
    {#if service && data.promotion?.nodes?.length}
      <PromotionFigure {service} graph={data.promotion} {rows} moreHref={(ex) => deliveryEnvironmentsUrl({ q, service, domain, except: exceptParam(ex) })} />
    {:else if !service && data.counts?.byEnvironment?.length}
      <EnvFleetFigure byEnvironment={data.counts.byEnvironment} href={stateHref} partial={data.completeness === 'partial'} asOf={timeLabel(data.asOf)} />
    {/if}
    {#if rows.length === 0 && filtered}
      <div class="state-box" data-testid="environments-empty"><h2>Nothing matches the filter</h2><p>No service you can see{service ? ` named ${service}` : ''}{q ? ` matches “${q}”` : ''}{environment ? ` has a Stage in ${environment}` : ''}{exceptEnvs.length ? ` has a Stage outside ${andList(exceptEnvs, 3)}` : ''}{tier ? ` has a Stage in an environment named like ${tier}` : ''}{stateFilter ? ` has a Stage that is ${ENV_STATES.find((x) => x.key === stateFilter)?.word ?? stateFilter}` : ''}.</p></div>
    {:else if rows.length === 0}
      <div class="state-box" data-testid="environments-empty">
        <h2>No Stages yet</h2>
        <p>No Stage exists {data.completeness === 'partial' ? 'that you can see' : 'in this cluster'}. A Stage is one service in one environment. Install pacto-operator with <code>delivery.enabled=true</code>, then create one.</p>
      </div>
    {:else}
      {#if strip}
        <div class="t-meta ev-legend ev-key" data-testid="environments-legend">
          {#each STATES as st (st.key)}<span><span class={`ev-dot s-${st.key}`} aria-hidden="true"></span>{st.label}</span>{/each}
          <span><span class="ev-link" aria-hidden="true"></span>Fed by the one before</span>
          <HelpTip label="the paths" text="Each row is its service's own path, in the order its Stages declare their upstreams. A line joins two environments when the second is fed by the first. A tier is only what a name says. Point at a dot or tab to it for its version, press it for the rest." />
        </div>
        <!-- The strip is as wide as what it holds: a full-width table left its right half empty
             while the names in it were cut. -->
        <!-- svelte-ignore a11y_no_noninteractive_tabindex (SPEC 24.2: a region that scrolls sideways takes focus so the arrow keys scroll it) -->
        <div class="ev-cq"><div class="ev-board ev-board-fit" role="region" aria-label="Services by environment, each along its own promotion path" tabindex="0">
          <table class="data-table ev-table ev-strip" data-testid="environments-board">
            <caption class="visually-hidden">Services by environment, each along its own promotion path</caption>
            <thead>
              <tr>
                <th scope="col">Service</th>
                <th scope="col">Path <span class="t-meta">as each service's Stages declare it</span></th>
                <th scope="col">State <span class="t-meta">in {formatCount(shown.length)} environments</span></th>
              </tr>
            </thead>
            <tbody>
              {#each services as svc (svc)}
                {@const sum = summaryOf(svc)}
                {@const path = pathOf(svc)}
                <tr data-testid={`env-row-${svc}`}>
                  <th scope="row"><a class="ev-name" href={deliveryServiceUrl(svc)}>{svc}</a></th>
                  <td>
                    <div class="ev-path" role="toolbar" aria-label={`Path of ${svc}`} tabindex="-1" onkeydown={roam}>
                      {#each path.slice(0, MAX_PATH_MARKS) as m, i (m.env)}
                        {@const here = cell(svc, m.env)}
                        {@const st = stateOf(here)}
                        {@const open = opened?.svc === svc && opened.env === m.env}
                        {@const fedBy = m.linked ? [] : m.fedBy}
                        <span class:linked={m.linked} data-testid="env-mark">
                          <button type="button" class="ev-mark" class:open data-testid="env-dot" data-env={m.env}
                            tabindex={i === 0 ? 0 : -1} title={dotTitle(m.env, here, fedBy)} aria-label={dotTitle(m.env, here, fedBy)}
                            aria-expanded={open} aria-controls={open ? cellId(svc) : undefined} onclick={() => toggle(svc, m.env)}><span class={`ev-dot s-${st.key}`} aria-hidden="true"></span><bdi class="ev-env">{m.env}</bdi>{#if deadOf(here).length}<span class="ev-nosrc" aria-hidden="true">?</span>{/if}</button>
                        </span>
                      {/each}
                      {#if path.length > MAX_PATH_MARKS}
                        {@const more = path.length - MAX_PATH_MARKS}
                        <span><a class="ev-more" href={deliveryEnvironmentsUrl({ service: svc, domain })} aria-label={`${formatCount(more)} more environments of ${svc}`} data-testid="env-more">+{formatCount(more)}</a></span>
                      {/if}
                    </div>
                  </td>
                  <td data-testid="env-summary"><div class="ev-sum">
                    {#each sum as st (st.key)}<span class={`badge badge-${st.tone}`}>{st.n} {st.label.toLowerCase()}</span>{/each}
                    {#if !sum.length}<span class="t-meta">Not delivered in these environments</span>{/if}
                  </div></td>
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
        </div></div>
      {:else}
        <!-- svelte-ignore a11y_no_noninteractive_tabindex (SPEC 24.2: a region that scrolls sideways takes focus so the arrow keys scroll it) -->
        <div class="ev-cq"><div class="ev-board" role="region" aria-label="Services by environment, each along its own promotion path" tabindex="0">
          <table class="data-table ev-table" data-testid="environments-board">
            <caption class="visually-hidden">Services by environment, each along its own promotion path</caption>
            <colgroup><col style="width: 13rem" /><col /></colgroup>
            <thead>
              <tr><th scope="col">Service</th><th scope="col">Path <span class="t-meta">as each service's Stages declare it</span></th></tr>
            </thead>
            <tbody>
              {#each services as svc (svc)}
                <tr data-testid={`env-row-${svc}`}>
                  <th scope="row"><a class="ev-name" href={deliveryServiceUrl(svc)}>{svc}</a></th>
                  <td>
                    <ol class="ev-steps" aria-label={`Path of ${svc}`}>
                      {#each pathOf(svc) as m (m.env)}
                        <li class="ev-step" data-testid="env-step" data-env={m.env}>
                          <span class="t-meta ev-step-env"><bdi class="ev-name">{m.env}</bdi>{named(m.env)}{m.fedBy.length ? `, fed by ${andList(m.fedBy, 3)}` : ''}</span>
                          {@render stack(svc, m.env)}
                        </li>
                      {/each}
                    </ol>
                  </td>
                </tr>
                {#if proposing && proposingService === svc}
                  <tr class="ev-plan-row"><td colspan="2">
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
        </div></div>
      {/if}
      <!-- One page needs no footer: the header already counts the services. -->
      {#if onPage || (data.page?.offset ?? 0) > 0}<Pager page={data.page} {href} label="Pages of services" noun="services" testid="environments-pager" />{/if}
      {#if observing.length}
        <section class="ev-shadows" data-testid="shadow-summary" aria-labelledby="ev-shadows-title">
          <div class="ev-shadows-head">
            <h2 id="ev-shadows-title" class="t-section-title">What Pacto would have done <span class="t-meta">{countOf(observing.length, 'observing Stage')} on this page, advisory and unsigned</span></h2>
            <HelpTip label="observing Stages" text="These Stages observe: GitOps applies and Pacto moves nothing. Each row is what Pacto would have done with a candidate on Autonomous, next to what happened. It comes from the Stage status and is unsigned. Seen in source is when GitOps' source showed the candidate, which can be before it rolled out." />
          </div>
          {#each observingCap.slice(observing) as r (r.namespace + '/' + r.stage)}
            <details class="ev-shadow disclosure" data-testid="shadow" data-stage={`${r.namespace}/${r.stage}`}>
              <summary>
                <span class="disclosure-caret" data-motion aria-hidden="true"></span>
                <h3 id={`shadow-${r.namespace}-${r.stage}`} class="ev-shadow-title"><bdi>{r.service}</bdi> in <bdi>{r.environment}</bdi></h3>
                <span class="t-meta">{countOf(r.shadow.length, 'candidate')}</span>
              </summary>
              <div class="ev-scroll">
                <table class="ev-table">
                  <caption class="visually-hidden">What Pacto would have done with each candidate on <bdi>{r.service}</bdi> in <bdi>{r.environment}</bdi>, newest first</caption>
                  <thead><tr><th scope="col">Candidate</th><th scope="col">Pacto would have</th><th scope="col">Seen in source</th><th scope="col">Verification</th></tr></thead>
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
  /* The board scrolls sideways, so it is the sticky header's scroll box: the shared
     top: var(--navbar-h) would push the header down over the first row. */
  .ev-board .ev-table thead th { top: 0; }
  .ev-plan-row td { background: var(--c-surface-inset); }
  /* Sized from the board's own container (.ev-cq), never the viewport: beside the
     sidebar the viewport is wider than the board, and the panel's actions fell outside it. */
  .ev-plan { position: sticky; left: var(--sp-3); max-width: min(60rem, calc(100cqi - 2 * var(--sp-3))); }
  .ev-stage { color: var(--c-text-2); font-family: var(--font-mono); }
  .ev-tools { display: flex; flex-wrap: wrap; gap: var(--sp-3); align-items: center; justify-content: space-between; }
  .ev-legend { margin: 0; }
  /* Page scroll down, sideways scroll only when the columns need it: no inner box that hides rows. */
  /* relative: the visually hidden labels in off-screen cells are absolute and would widen the page. */
  .ev-board { position: relative; overflow-x: auto; border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); }
  /* Up to four environments, each row is its service's own path as whole cells, in the
     order its Stages declare (SPEC 21 r3): no column is shared, so none claims an order. */
  .ev-steps { display: flex; flex-wrap: wrap; gap: var(--sp-3) var(--sp-4); margin: 0; padding: 0; list-style: none; }
  .ev-step { display: flex; flex-direction: column; gap: var(--sp-1); flex: 1 1 12rem; min-inline-size: min(12rem, 100%); max-inline-size: 20rem; }
  .ev-step-env { overflow-wrap: anywhere; }
  .ev-step-env .ev-name { display: inline; font-weight: 600; color: var(--c-text-2); }
  /* Names wrap rather than clip: the whole name is on the page. */
  .ev-name { display: block; overflow-wrap: anywhere; }
  .ev-board-fit { width: fit-content; max-width: 100%; }
  .ev-strip { table-layout: auto; width: auto; }
  .ev-strip .ev-name { max-width: 22rem; }
  .ev-picks { display: flex; flex-wrap: wrap; gap: var(--sp-3); }
  .ev-key { display: flex; flex-wrap: wrap; align-items: center; gap: var(--sp-1) var(--sp-3); }
  .ev-key > span { display: inline-flex; align-items: center; gap: var(--sp-1); }
  /* The strip: each row is its service's own path, marks in the order its Stages declare.
     The marks wrap within the cell, so a long path never scrolls the page. */
  .ev-path { display: flex; flex-wrap: wrap; align-items: center; gap: var(--sp-1) var(--sp-3); min-height: 1.5rem; max-inline-size: 48rem; }
  .ev-path > span { display: inline-flex; align-items: center; min-inline-size: 0; }
  /* A declared upstream draws a line from the mark before; anything else is a gap. */
  .ev-path > span.linked::before, .ev-link { content: ''; display: inline-block; inline-size: var(--sp-3); block-size: 2px; margin-inline-start: calc(-1 * var(--sp-3)); background: var(--c-text-3); }
  .ev-link { margin-inline-start: 0; }
  .ev-mark { display: inline-flex; align-items: center; gap: var(--sp-1); min-inline-size: 0; min-block-size: 1.5rem; padding: 0 var(--sp-1); border: 0; border-radius: var(--radius-sm); background: none; color: var(--c-text-2); font: inherit; cursor: pointer; }
  .ev-mark:hover, .ev-mark.open { outline: 2px solid var(--c-accent); outline-offset: 1px; }
  .ev-env { font-size: var(--text-xs); overflow-wrap: anywhere; text-align: start; }
  .ev-more { font-size: var(--text-xs); }
  /* A Stage with no source Pacto can follow: unknown, never drawn as a valid entry. */
  .ev-nosrc { color: var(--c-text-3); font-size: var(--text-xs); font-weight: 600; }
  .ev-dot { display: inline-block; flex: none; width: var(--ev-d, 12px); height: var(--ev-d, 12px);
    padding: 0; border: 2px solid transparent; border-radius: 50%; background: none; box-sizing: border-box; }
  .s-verified { background: var(--c-ok); }
  .s-waiting { background: var(--c-warn); }
  .s-failed { background: var(--c-err); border-radius: 2px; transform: rotate(45deg) scale(0.85); }
  .s-frozen { background: var(--c-warn); border-radius: 2px; }
  .s-autohold { background: var(--c-info); border-radius: 2px; }
  .s-unverified { border-color: var(--c-text-3); }
  .s-empty { border: 2px dashed var(--c-text-3); }
  .ev-sum { display: flex; flex-wrap: wrap; align-items: center; gap: var(--sp-1); min-height: 1.5rem; }
  /* Rows line up on their first line: the name, the path and the first chips share one 1.5rem line, however far they wrap. */
  .ev-strip tbody th { line-height: 1.5rem; }
  .ev-strip thead th { vertical-align: bottom; }
  .ev-detail { display: flex; flex-direction: column; gap: var(--sp-2); max-width: 40rem; }
  .ev-detail .ev-ready { display: block; overflow: visible; }
  .ev-detail-head { display: flex; align-items: center; justify-content: space-between; gap: var(--sp-2); }
  .ev-shadows { display: flex; flex-direction: column; gap: var(--sp-2); }
  .ev-shadows-head { display: flex; align-items: center; gap: var(--sp-2); }
  .ev-shadows h2 { margin: 0; }
  .ev-shadow summary { display: flex; align-items: baseline; gap: var(--sp-2); cursor: pointer; }
  .ev-shadow-title { margin: 0; }
  .ev-detail-head .ev-shadow-title { font-size: var(--text-sm); font-weight: 600; }
  .ev-shadow[open] > :not(summary) { margin-top: var(--sp-2); }
  /* The board's frame is the container (SPEC 24.2), so the card form follows the width
     the board gets beside the sidebar, as every data table's does. */
  .ev-cq { container: dtable / inline-size; }
  @container dtable (max-width: 48rem) {
    .ev-board .ev-table { min-width: 0 !important; }
    .ev-board-fit { width: auto; }
    .ev-board .ev-table tbody th { position: static; }
  }
  .ev-table th a, .ev-version { color: var(--c-accent); }
  .ev-cell { display: flex; flex-direction: column; gap: var(--sp-1); }
  .ev-cell + .ev-cell { margin-top: var(--sp-2); }
  .ev-version { font-weight: 600; }
  .ev-actions { display: flex; gap: var(--sp-1); flex-wrap: wrap; }
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
