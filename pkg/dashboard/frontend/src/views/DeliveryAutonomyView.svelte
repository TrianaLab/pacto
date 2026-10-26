<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { deliveryHistoryUrl, deliveryAutonomyUrl } from '../lib/router.ts';
  import { urlInt, limitParam } from '../lib/paging.ts';
  import { formatCount } from '../lib/format.ts';
  import Pager from '../components/Pager.svelte';
  import ListFilter from '../components/ListFilter.svelte';
  import CappedList from '../components/CappedList.svelte';
  import { AUTONOMY_TYPE, rungLabel, rungHelp, proposalText, shadowLabel, blockerLabel, sentence } from '../lib/delivery.ts';
  import PageHeader from '../components/PageHeader.svelte';
  import DeliveryNav from '../components/DeliveryNav.svelte';
  import DeliveryProblem from '../components/DeliveryProblem.svelte';
  import ListingNote from '../components/ListingNote.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import ProposePanel from '../components/ProposePanel.svelte';

  // How far each Stage runs on its own, what Pacto proposes next and what
  // stands in the way. The page only reads: a rung moves through a proposed
  // operation a second person approves, and a fix is something to copy and run.
  // Stages and blockers page on the server, each with its place in the URL.
  let { offset = '', limit = '', q = '', blockers: blockersAt = '', refreshTick = 0, miraURL = '' } = $props();

  const query = $derived({ offset: urlInt(offset), limit: urlInt(limit), q, blockers: urlInt(blockersAt) });
  const loader = createProductLoader(() => api.deliveryAutonomy({
    offset: query.offset || undefined, limit: query.limit || undefined, q: q || undefined, blockers: query.blockers || undefined,
  }));
  const question = $derived(JSON.stringify(query));
  $effect(() => { loader.sync(`${question}@@${refreshTick}`, question); });
  onDestroy(() => loader.destroy());

  const data = $derived(loader.dataTag === question ? loader.data : null);
  const hrefFor = (key, pageKey) => (off, lim) => deliveryAutonomyUrl({
    ...query, [key]: off, limit: limitParam(lim, limit, data?.[pageKey]?.limit ?? lim),
  });
  /** A share as a whole percent, and never 0% for something that is there. */
  const percent = (n, of) => (n > 0 && n / of < 0.01 ? '<1%' : `${Math.round((n / of) * 100)}%`);
  const stages = $derived(data?.stages ?? []);
  const blockers = $derived(data?.blockers ?? []);
  const share = $derived(data?.contracted);
  const seen = $derived(share ? share.services + share.uncontractedCallers : 0);
  const promotions = $derived(data?.promotionsWithoutPerson ?? { count: 0, of: 0, untyped: 0 });

  let proposing = $state(null);
  let proposingLabel = $state('');
  let opener = null;
  function propose(s, e) {
    opener = e.currentTarget;
    proposingLabel = rungLabel(s.proposal.rung);
    proposing = { namespace: s.namespace, stage: s.stage, type: AUTONOMY_TYPE, from: s.rung, to: s.proposal.rung };
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
    subtitle="How far each Stage runs on its own, what Pacto proposes next and what stands in the way. A person confirms every step up; this page only reads."
    asOf={data?.asOf}
    revalidating={!!data && loader.loading}
  />
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
          <dd class="t-body-2">{formatCount(share.services)} of the {formatCount(seen)} services Pacto has seen{#if share.uncontractedCallers}: {share.uncontractedCallers} workload{share.uncontractedCallers === 1 ? '' : 's'} call{share.uncontractedCallers === 1 ? 's' : ''} a contracted service with no contract of {share.uncontractedCallers === 1 ? 'its' : 'their'} own{/if}.</dd>
        {:else}
          <dd class="t-body-2">Pacto has seen no service here yet, so it cannot say.</dd>
        {/if}
      </div>
      <div class="au-metric" data-testid="autonomy-promotions">
        <dt>Promotions without a person</dt>
        {#if promotions.of}
          <dd class="au-value">{formatCount(promotions.count)} of {formatCount(promotions.of)}</dd>
          <dd class="t-body-2">Counted over the last 20 operations per environment.</dd>
          {#if promotions.untyped}
            <dd class="t-body-2">Pacto cannot tell what {promotions.untyped} more {promotions.untyped === 1 ? 'operation was' : 'operations were'}.</dd>
          {/if}
        {:else if promotions.untyped}
          <dd class="t-body-2">Pacto cannot tell yet which operations were promotions.</dd>
        {:else}
          <dd class="t-body-2">No promotion succeeded in the last 20 operations per environment.</dd>
        {/if}
      </div>
    </dl>

    <section aria-labelledby="au-stages">
      <h2 id="au-stages" class="t-section-title">Each Stage</h2>
      {#if stages.length === 0}
        <p class="t-body-2" data-testid="autonomy-empty">{q ? `No Stage you can see matches “${q}”.` : `No Stage exists ${data.completeness === 'partial' ? 'that you can see' : 'in this cluster'}.`}</p>
      {:else}
        <div class="data-scroll">
          <table class="data-table au-table" data-testid="autonomy-stages">
            <caption class="visually-hidden">The rung of each Stage and what Pacto proposes next</caption>
            <colgroup><col style="width: 28%" /><col style="width: 30%" /><col style="width: 42%" /></colgroup>
            <thead><tr><th scope="col">Stage</th><th scope="col">Rung</th><th scope="col">Next step</th></tr></thead>
            <tbody>
              {#each stages as s (s.namespace + '/' + s.stage)}
                <tr data-stage={`${s.namespace}/${s.stage}`}>
                  <th scope="row"><a href={deliveryHistoryUrl({ namespace: s.namespace, stage: s.stage })}>{s.service}</a> <span class="t-meta">in {s.environment}</span></th>
                  <td data-label="Rung"><span class="badge badge-neutral" data-testid="rung">{rungLabel(s.rung)}</span><span class="t-meta au-help">{rungHelp(s.rung)}</span></td>
                  <td data-label="Next step">
                    <span>{proposalText(s.rung, s.proposal)}</span>
                    {#if s.proposal?.rung}
                      <button type="button" class="btn btn-sm" onclick={(e) => propose(s, e)} aria-label={`Propose ${rungLabel(s.proposal.rung)} for ${s.service} in ${s.environment}`}>Propose</button>
                    {:else if s.proposal?.blockers?.length}
                      <span class="t-meta">{s.proposal.blockers.length} blocker{s.proposal.blockers.length === 1 ? '' : 's'} below.</span>
                    {/if}
                    {#if s.lastShadow}<span class="t-meta au-help">Newest candidate: on Autonomous, Pacto would have chosen to {shadowLabel(s.lastShadow)}.</span>{/if}
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

    <section aria-labelledby="au-blockers">
      <h2 id="au-blockers" class="t-section-title">What stands in the way</h2>
      {#if blockers.length === 0}
        <p class="t-body-2" data-testid="autonomy-no-blockers">{q ? `No blocker matches “${q}”.` : 'Nothing Pacto can see blocks the next rung.'}</p>
      {:else}
        <p class="t-body-2">Copying a fix changes nothing in the cluster: you review it and run it yourself.</p>
        <ul class="au-blockers" data-testid="autonomy-blockers">
          {#each blockers as b, i (b.kind + ' ' + b.subject + ' ' + i)}
            {@const key = b.kind + ' ' + b.subject}
            {@const fix = b.fix?.command || b.fix?.snippet || ''}
            <li class="au-blocker">
              <p><strong>{blockerLabel(b.kind)}</strong>: <code>{b.subject}</code></p>
              {#if b.stages?.length}<p class="t-meta au-stages">On {formatCount(b.stages.length)} Stage{b.stages.length === 1 ? '' : 's'}: <CappedList items={b.stages} max={3} inline={true} label={`Stages ${b.subject} blocks`}>{#snippet item(st)}{st}{/snippet}</CappedList></p>{/if}
              {#if b.message}<p class="t-body-2">{sentence(b.message)}</p>{/if}
              {#if fix}
                <pre class="au-fix"><code>{fix}</code></pre>
                <button type="button" class="btn btn-sm" onclick={() => copy(key, fix)} aria-label={`Copy fix for ${b.subject}`}>Copy fix</button>
                <span class="t-meta" role="status">{copied === key ? 'Copied.' : ''}</span>
              {/if}
            </li>
          {/each}
        </ul>
        <Pager page={data.blockersPage} href={hrefFor('blockers', 'blockersPage')} label="Pages of blockers" noun="blockers" testid="autonomy-blockers-pager" />
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
  .au-help { display: block; margin: var(--sp-1) 0 0 !important; }
  .au-blockers { list-style: none; padding: 0; display: flex; flex-direction: column; gap: var(--sp-3); }
  .au-blocker { background: var(--c-surface); border: 1px solid var(--c-border); border-radius: var(--radius-sm); padding: var(--sp-3); }
  .au-blocker p { margin: 0 0 var(--sp-1); }
  .au-fix { background: var(--c-surface-inset); padding: var(--sp-2); border-radius: var(--radius-xs); overflow: auto; max-height: 16rem; font-size: var(--text-xs); margin: var(--sp-1) 0; }
  .au-blocker { overflow-wrap: break-word; }
  .au-plan-row td { background: var(--c-surface-inset); }
</style>
