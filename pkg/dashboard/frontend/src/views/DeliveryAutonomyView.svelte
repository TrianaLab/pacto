<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { deliveryHistoryUrl, deliveryAutonomyUrl } from '../lib/router.ts';
  import { deliveryServiceUrl } from '../lib/deliveryLinks.svelte.ts';
  import { urlInt, limitParam } from '../lib/paging.ts';
  import { formatCount, countOf } from '../lib/format.ts';
  import Pager from '../components/Pager.svelte';
  import ListFilter from '../components/ListFilter.svelte';
  import CappedList from '../components/CappedList.svelte';
  import { AUTONOMY_TYPE, levelLabel, levelHelp, proposalText, shadowLabel, blockerLabel, blockerService, sentence } from '../lib/delivery.ts';
  import PageHeader from '../components/PageHeader.svelte';
  import DeliveryNav from '../components/DeliveryNav.svelte';
  import DeliveryProblem from '../components/DeliveryProblem.svelte';
  import ListingNote from '../components/ListingNote.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import ProposePanel from '../components/ProposePanel.svelte';
  import DeliveryLead from '../components/DeliveryLead.svelte';
  import HelpTip from '../components/HelpTip.svelte';

  // How far each Stage runs on its own, what Pacto proposes next and what
  // stands in the way. The page only reads: a level moves through a proposed
  // operation a second person approves, and a fix is something to copy and run.
  // Stages and blockers page on the server, each with its place in the URL.
  let { offset = '', limit = '', q = '', blockers: blockersAt = '', refreshTick = 0, miraURL = '' } = $props();

  const query = $derived({ offset: urlInt(offset), limit: urlInt(limit), q, blockers: urlInt(blockersAt) });
  const loader = createProductLoader(() => api.deliveryAutonomy({
    offset: query.offset || undefined, limit: query.limit || undefined, q: q || undefined, blockers: query.blockers || undefined,
  }));
  const question = $derived(JSON.stringify(query));
  // The same question without its page: turning a page keeps the rows on screen.
  const listQuestion = $derived(q);
  $effect(() => { loader.sync(`${question}@@${refreshTick}`, question, listQuestion); });
  onDestroy(() => loader.destroy());

  const answer = $derived(loader.answer(question, listQuestion));
  const data = $derived(answer.data);
  const hrefFor = (key, pageKey) => (off, lim) => deliveryAutonomyUrl({
    ...query, [key]: off, limit: limitParam(lim, limit, data?.[pageKey]?.limit ?? lim),
  });
  /** A share as a whole percent, and never 0% for something that is there. */
  const percent = (n, of) => (n > 0 && n / of < 0.01 ? '<1%' : `${Math.round((n / of) * 100)}%`);
  const stages = $derived(data?.stages ?? []);
  const blockers = $derived(data?.blockers ?? []);
  // The page's blockers in runs of one kind (the server sorts them so), each
  // with how many of that kind match over every page.
  const groups = $derived(blockers.reduce((gs, b) => {
    if (gs.at(-1)?.kind !== b.kind) gs.push({ kind: b.kind, items: [], total: data?.blockerKinds?.find((k) => k.kind === b.kind)?.count });
    gs.at(-1).items.push(b);
    return gs;
  }, []));
  const share = $derived(data?.contracted);
  const seen = $derived(share ? share.services + share.uncontractedCallers : 0);
  const promotions = $derived(data?.promotionsWithoutPerson ?? { count: 0, of: 0, untyped: 0 });

  // The page's answer: what stands in the way, over every page, and which
  // Stages on this page can move up now.
  const lead = $derived.by(() => {
    if (!data) return null;
    const total = data.blockersPage?.total ?? blockers.length;
    const ready = stages.filter((s) => s.proposal?.level).length;
    const onPage = (data.page?.total ?? 0) > stages.length ? ' on this page' : '';
    return {
      head: total ? `${countOf(total, 'blocker')} ${total === 1 ? 'stands' : 'stand'} in the way.` : 'Nothing Pacto can see blocks the next level.',
      rest: ready ? `${countOf(ready, 'Stage')} can move up now${onPage}.` : '',
    };
  });

  let proposing = $state(null);
  let proposingLabel = $state('');
  let opener = null;
  function propose(s, e) {
    opener = e.currentTarget;
    proposingLabel = levelLabel(s.proposal.level);
    proposing = { namespace: s.namespace, stage: s.stage, type: AUTONOMY_TYPE, from: s.level, to: s.proposal.level };
  }
  function closePlan() { proposing = null; opener?.focus(); }

  let copied = $state('');
  async function copy(key, text) {
    try {
      await navigator.clipboard.writeText(text);
      copied = key;
    } catch {
      // ponytail: no clipboard (an insecure context); the fix stays on the page to select.
      copied = '';
    }
  }
</script>

<div class="product-page">
  <PageHeader
    title="Path to autonomy"
    asOf={data?.asOf}
    revalidating={!!data && loader.loading}
    paging={answer.paging}
  >
    {#if lead}<DeliveryLead head={lead.head} rest={lead.rest} tipLabel="the path to autonomy"
      tip="How far each Stage runs on its own, what Pacto proposes next and what stands in the way. A person confirms every step up; this page only reads." />{/if}
  </PageHeader>
  <DeliveryNav current="delivery-autonomy" />
  <ListFilter value={q} href={(v) => deliveryAutonomyUrl({ q: v })} label="Filter Stages and blockers by service, environment or kind"
    placeholder="Service, environment or blocker" testid="autonomy-filter" />

  {#if !data && loader.loading}
    <EmptyState loading={true} />
  {:else if !data && loader.error}
    <DeliveryProblem error={loader.error} onRetry={() => loader.refresh()} />
  {:else if data}
    <ListingNote listing={data} error={loader.error} onRetry={() => loader.refresh()} />

    <dl class="au-metrics">
      <div class="au-metric" data-testid="autonomy-contracted">
        <dt>Services with a contract</dt>
        {#if share && seen}
          <dd class="au-value">{percent(share.services, seen)}</dd>
          <dd class="t-body-2">{formatCount(share.services)} of the {formatCount(seen)} services Pacto has seen</dd>
          {#if share.uncontractedCallers}<dd class="t-meta au-callers">{countOf(share.uncontractedCallers, 'uncontracted caller')} <HelpTip label="uncontracted callers" text="A workload that calls a contracted service with no contract of its own." /></dd>{/if}
        {:else}
          <dd class="t-body-2">Pacto has seen no service here yet, so it cannot say.</dd>
        {/if}
      </div>
      <div class="au-metric" data-testid="autonomy-promotions">
        <dt>Promotions without a person <HelpTip label="this count" text="Counted over the last 20 operations per environment." /></dt>
        {#if promotions.of}
          <dd class="au-value">{formatCount(promotions.count)} of {formatCount(promotions.of)}</dd>
          {#if promotions.untyped}
            <dd class="t-body-2">Pacto cannot tell what {formatCount(promotions.untyped)} more {promotions.untyped === 1 ? 'operation was' : 'operations were'}.</dd>
          {/if}
        {:else if promotions.untyped}
          <dd class="t-body-2">Pacto cannot tell yet which operations were promotions.</dd>
        {:else}
          <dd class="t-body-2">No promotion succeeded in the last 20 operations per environment.</dd>
        {/if}
      </div>
    </dl>

    <section aria-labelledby="au-blockers">
      <div class="au-head">
        <h2 id="au-blockers" class="t-section-title">What stands in the way</h2>
        {#if blockers.length}<HelpTip label="fixes" text="Copying a fix changes nothing in the cluster: you review it and run it yourself." />{/if}
      </div>
      {#if blockers.length === 0}
        <p class="t-body-2" data-testid="autonomy-no-blockers">{q ? `No blocker matches “${q}”.` : 'Nothing Pacto can see blocks the next level.'}</p>
      {:else}
        <div class="au-groups" data-testid="autonomy-blockers">
          {#each groups as g, gi (g.kind + gi)}
            <section class="au-group" aria-label={blockerLabel(g.kind)}>
              <h3 class="au-group-title">{blockerLabel(g.kind)} <span class="t-meta" data-testid="blocker-kind-count">{formatCount(g.total ?? g.items.length)}{g.total > g.items.length ? ` in all, ${formatCount(g.items.length)} on this page` : ''}</span></h3>
              <ul class="au-blockers">
                {#each g.items as b, i (b.subject + ' ' + i)}
                  {@const key = b.kind + ' ' + b.subject}
                  {@const fix = b.fix?.command || b.fix?.snippet || ''}
                  {@const svc = blockerService(b)}
                  <li class="au-blocker">
                    <details class="disclosure">
                      <summary class="au-row">
                        <span class="disclosure-caret" data-motion aria-hidden="true">&#9656;</span>
                        <!-- Plain text, not a link: a link inside a summary is a control inside a control. The link is in the detail. -->
                        {#if b.subject}<code class="au-subject cell-clip" title={b.subject}>{b.subject}</code>{:else}<span class="au-subject cell-clip">{b.stages?.length ? `${countOf(b.stages.length, 'Stage')}` : 'Every Stage'}</span>{/if}
                        <span class="t-meta au-count">{#if !b.subject}{:else if b.stages?.length}{countOf(b.stages.length, 'Stage')}{:else}No Stage{/if}</span>
                        <span class="t-meta au-msg cell-clip" title={b.message ? sentence(b.message) : undefined}>{b.message ? sentence(b.message) : ''}</span>
                        {#if fix}<span class="badge badge-neutral au-hasfix">Copy fix</span>{/if}
                      </summary>
                      <div class="au-detail">
                        <!-- The row clips a long name; here it is whole. -->
                        {#if b.subject}<p class="au-full"><code>{b.subject}</code>{#if svc}{' '}<a href={deliveryServiceUrl(svc)}>Open {svc}</a>{/if}</p>{/if}
                        {#if b.stages?.length}<p class="t-meta au-stages">On {countOf(b.stages.length, 'Stage')}: <CappedList items={b.stages} max={3} inline={true} label={`Stages ${b.subject} blocks`}>{#snippet item(st)}{st}{/snippet}</CappedList></p>{/if}
                        {#if b.message}<p class="t-body-2">{sentence(b.message)}</p>{/if}
                        {#if fix}
                          <pre class="au-fix"><code>{fix}</code></pre>
                          <button type="button" class="btn btn-sm" onclick={() => copy(key, fix)} aria-label={`Copy fix for ${b.subject}`}>Copy fix</button>
                          <span class="t-meta" role="status">{copied === key ? 'Copied.' : ''}</span>
                        {/if}
                      </div>
                    </details>
                  </li>
                {/each}
              </ul>
            </section>
          {/each}
        </div>
        <Pager page={data.blockersPage} href={hrefFor('blockers', 'blockersPage')} label="Pages of blockers" noun="blockers" testid="autonomy-blockers-pager" />
      {/if}
    </section>
    <section aria-labelledby="au-stages">
      <h2 id="au-stages" class="t-section-title">Each Stage</h2>
      {#if stages.length === 0}
        <p class="t-body-2" data-testid="autonomy-empty">{q ? `No Stage you can see matches “${q}”.` : `No Stage exists ${data.completeness === 'partial' ? 'that you can see' : 'in this cluster'}.`}</p>
      {:else}
        <div class="data-scroll">
          <table class="data-table au-table" data-testid="autonomy-stages">
            <caption class="visually-hidden">The level of each Stage and what Pacto proposes next</caption>
            <colgroup><col style="width: 28%" /><col style="width: 30%" /><col style="width: 42%" /></colgroup>
            <thead><tr><th scope="col">Stage</th><th scope="col">Level <HelpTip label="the levels" text="Observe: Pacto plans and moves nothing. Advise: a person proposes or approves every promote. Auto non-breaking: Pacto ships non-breaking changes on its own. Autonomous: Pacto ships any change the contract graph clears; Unknown still waits for a person." /></th><th scope="col">Next step <HelpTip label="would-have lines" text="On an observing Stage, what Pacto would have done with the newest candidate on Autonomous. Advisory and unsigned." /></th></tr></thead>
            <tbody>
              {#each stages as s (s.namespace + '/' + s.stage)}
                <tr data-stage={`${s.namespace}/${s.stage}`}>
                  <th scope="row"><a href={deliveryHistoryUrl({ namespace: s.namespace, stage: s.stage })}>{s.service}</a> <span class="t-meta">in {s.environment}</span></th>
                  <td data-label="Level"><span class="au-level"><span class="badge badge-neutral" data-testid="level" title={levelHelp(s.level)}>{levelLabel(s.level)}</span></span></td>
                  <td data-label="Next step">
                    {#if s.proposal?.level}<span>{proposalText(s.level, s.proposal)}</span>{:else if !s.proposal?.blockers?.length && !s.lastShadow}<span class="au-none" aria-label="Nothing to propose yet">—</span>{/if}
                    {#if s.proposal?.level}
                      <button type="button" class="btn btn-sm" onclick={(e) => propose(s, e)} aria-label={`Propose ${levelLabel(s.proposal.level)} for ${s.service} in ${s.environment}`}>Propose</button>
                    {:else if s.proposal?.blockers?.length}
                      <span class="t-meta">{countOf(s.proposal.blockers.length, 'blocker')} below.</span>
                    {/if}
                    {#if s.lastShadow}<span class="t-meta au-help" data-testid="would-have">Pacto would have chosen to {shadowLabel(s.lastShadow)}.</span>{/if}
                  </td>
                </tr>
                {#if proposing && proposing.namespace === s.namespace && proposing.stage === s.stage}
                  <tr class="au-plan-row"><td colspan="3">
                    {#key proposing}
                      <ProposePanel request={proposing} toVersion={proposingLabel} {miraURL} onClose={closePlan} />
                    {/key}
                  </td></tr>
                {/if}
              {/each}
            </tbody>
          </table>
        </div>
        <Pager page={data.page} href={hrefFor('offset', 'page')} label="Pages of Stages" noun="Stages" testid="autonomy-pager" />
      {/if}
    </section>

  {/if}
</div>

<style>
  .au-metrics { display: grid; grid-template-columns: repeat(auto-fit, minmax(16rem, 1fr)); gap: var(--sp-3); margin: 0; }
  .au-metric { background: var(--c-surface); border: 1px solid var(--c-border); border-radius: var(--radius-sm); padding: var(--sp-3) var(--sp-4); }
  .au-metric dt { color: var(--c-text-3); font-size: var(--text-sm); font-weight: 600; }
  .au-metric dd { margin: var(--sp-1) 0 0; }
  .au-value { font-size: var(--text-2xl, 1.75rem); font-weight: 700; color: var(--c-text); }
  .au-table { background: var(--c-surface); border-collapse: collapse; width: 100%; font-size: var(--text-sm); }
  .au-table th, .au-table td { border-bottom: 1px solid var(--c-border); padding: var(--sp-2) var(--sp-3); text-align: left; vertical-align: top; }
  .au-table thead th { color: var(--c-text-3); font-weight: 600; }
  .au-table th a { color: var(--c-accent); }
  .au-table td > * + * { margin-left: var(--sp-2); }
  .au-head { display: flex; align-items: center; gap: var(--sp-2); }
  .au-head h2 { margin: 0; }
  .au-level { display: inline-flex; align-items: center; gap: var(--sp-2); }
  .au-callers { display: flex; align-items: center; gap: var(--sp-1); }
  .au-none { color: var(--c-text-3); }
  .au-help { display: block; margin: var(--sp-1) 0 0 !important; }
  .au-groups { display: flex; flex-direction: column; gap: var(--sp-3); }
  .au-group-title { margin: var(--sp-2) 0 var(--sp-1); font: 600 var(--text-sm) / 1.4 var(--font-sans, inherit); font-style: normal; color: var(--c-text); }
  /* One line per blocker; the detail opens under it. */
  .au-blockers { list-style: none; margin: 0; padding: 0; background: var(--c-surface); border: 1px solid var(--c-border); border-radius: var(--radius-sm); }
  .au-blocker + .au-blocker { border-top: 1px solid var(--c-border-subtle, var(--c-border)); }
  .au-row { display: grid; grid-template-columns: auto minmax(0, 16rem) 6rem minmax(0, 1fr) auto; align-items: baseline; gap: var(--sp-3); padding: var(--sp-2) var(--sp-3); cursor: pointer; list-style: none; }
  .au-row::-webkit-details-marker { display: none; }
  .au-row:hover { background: var(--c-surface-hover); }
  .au-count { white-space: nowrap; }
  /* The clipped name opens up whole on hover and whenever the row or its link has
     keyboard focus, where a title tooltip never shows. */
  .au-row:hover .au-subject, .au-row:focus-visible .au-subject { white-space: normal; overflow-wrap: anywhere; }
  .au-detail { padding: 0 var(--sp-3) var(--sp-3) calc(var(--sp-3) * 2 + 0.75rem); }
  .au-detail p { margin: 0 0 var(--sp-1); }
  @media (max-width: 40rem) {
    .au-row { grid-template-columns: auto minmax(0, 1fr) auto; }
    .au-msg, .au-hasfix { display: none; }
  }
  .au-fix { background: var(--c-surface-inset); padding: var(--sp-2); border-radius: var(--radius-xs); overflow: auto; max-height: 16rem; font-size: var(--text-xs); margin: var(--sp-1) 0; }
  .au-blocker { overflow-wrap: break-word; }
  .au-plan-row td { background: var(--c-surface-inset); }
</style>
