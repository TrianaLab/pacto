<script>
  import { onDestroy, tick } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { deliveryInboxUrl, deliveryHistoryUrl, serviceUrl } from '../lib/router.ts';
  import { typeLabel, shortDigest, predictionLabel, decisionLabel, authTimeText } from '../lib/delivery.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import DeliveryNav from '../components/DeliveryNav.svelte';
  import DeliveryProblem from '../components/DeliveryProblem.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import VerdictList from '../components/VerdictList.svelte';
  import DecisionPanel from '../components/DecisionPanel.svelte';
  import ProposePanel from '../components/ProposePanel.svelte';
  import RefusalNotice from '../components/RefusalNotice.svelte';

  // One change: the plan, what Pacto predicted next to what happened with the
  // proof behind each check, who decided, the affected services, a timeline and
  // what the caller may do about it.
  let { namespace, name, refreshTick = 0, miraURL = '' } = $props();

  const loader = createProductLoader(() => api.deliveryOperation(namespace, name));
  $effect(() => { loader.sync(String(refreshTick)); });
  onDestroy(() => loader.destroy());

  const data = $derived(loader.data);
  const e = $derived(data?.explanation);
  const op = $derived(data?.operation);
  const st = $derived(op?.status);
  const actions = $derived(data?.actions ?? []);
  const consumers = $derived(st?.plan?.floor?.consumers ?? []);

  const TIMES = [
    ['admitted', 'Admitted'], ['started', 'Started'], ['applied', 'Applied'], ['finished', 'Finished'],
    ['approvalDeadline', 'Decision due by'], ['executionDeadline', 'Must finish by'], ['verifyDeadline', 'Checks end by'],
  ];
  const timeline = $derived(TIMES.filter(([k]) => st?.times?.[k]).map(([k, label]) => ({ label, at: st.times[k], k }))
    .sort((a, b) => a.at.localeCompare(b.at)));

  // Said here, not in the decision panel: the change moves on once decided and
  // the panel goes with it, but the person should still read that it worked.
  let decided = $state('');
  let rollingBack = $state(false);
  let confirmCancel = $state(false);
  let cancelRefusal = $state(null);
  let cancelled = $state(false);
  // A cancelled change is deleted, so the next refresh answers 404: the page
  // keeps how it last looked, says it is gone and offers nothing to do.
  const gone = $derived(cancelled || (loader.error?.status === 404 && loader.error?.code !== 'DeliveryNotInstalled'));
  let rollbackBtn = $state(null);
  let cancelBtn = $state(null);
  let keepBtn = $state(null);
  async function askCancel(open) {
    confirmCancel = open;
    await tick();
    (open ? keepBtn : cancelBtn)?.focus();
  }
  async function cancel() {
    cancelRefusal = null;
    try { await api.deliveryCancel(namespace, name); cancelled = true; loader.refresh(); }
    catch (err) { cancelRefusal = err; }
    confirmCancel = false;
  }
</script>

<div class="product-page">
  <Breadcrumbs trail={[{ label: 'Delivery', href: deliveryInboxUrl() }, { label: `${namespace}/${name}` }]} />
  <PageHeader
    eyebrow="Change"
    title={e ? `${typeLabel(e.spec.type)} on ${namespace}/${e.spec.stage}` : `${namespace}/${name}`}
    subtitle={e?.summary || ''}
    revalidating={!!data && loader.loading}
  />
  <DeliveryNav current="" />

  {#if !data && loader.loading}
    <EmptyState loading={true} />
  {:else if !data && loader.error}
    {#if loader.error.status === 404 && loader.error.code !== 'DeliveryNotInstalled'}
      <EmptyState error={true} title="No such change" message={`${namespace}/${name} does not exist, or it finished and was cleaned up. ${loader.error.message}`} />
    {:else}
      <DeliveryProblem error={loader.error} onRetry={() => loader.refresh()} />
    {/if}
  {:else if data}
    <section class="cd-status" aria-label="Where it stands">
      <span class="badge badge-neutral">{e.status.phase}</span>
      {#if e.status.outcome}<span class="badge {e.status.outcome === 'Succeeded' ? 'badge-ok' : e.status.outcome === 'Failed' || e.status.outcome === 'Errored' ? 'badge-err' : 'badge-neutral'}">{e.status.outcome}</span>{/if}
      {#if e.status.verification}<span class="badge badge-info">{e.status.verification}</span>{/if}
      {#if e.status.reason}<span class="t-body-2">{e.status.reason}{#if e.status.message}: {e.status.message}{/if}</span>{/if}
    </section>

    <dl class="cd-facts t-body-2">
      <dt>Environment</dt><dd><a href={deliveryHistoryUrl({ namespace, stage: e.spec.stage })}>{namespace}/{e.spec.stage}</a>{#if e.stage.frozen} (paused{#if e.stage.frozenReason}: {e.stage.frozenReason}{/if}){/if}</dd>
      {#if e.spec.params?.from}<dt>From</dt><dd><code title={e.spec.params.from}>{shortDigest(e.spec.params.from)}</code></dd>{/if}
      {#if e.spec.params?.to}<dt>To</dt><dd><code title={e.spec.params.to}>{shortDigest(e.spec.params.to)}</code></dd>{/if}
      <dt>Asked by</dt><dd>{e.spec.actor.user}{#if e.status.actorClass}{` (${e.status.actorClass})`}{/if}{#if e.spec.onBehalfOf?.user}{`, for ${e.spec.onBehalfOf.user}`}{/if}</dd>
      {#if e.spec.reason}<dt>Why</dt><dd>{e.spec.reason}</dd>{/if}
    </dl>

    {#if cancelled}
      <p class="t-body-2" role="status">Cancelled. Nothing changed.</p>
    {:else if gone}
      <p class="cd-stale t-body-2" role="alert" data-testid="change-gone">This change is gone: it was cancelled or cleaned up. This is how it last looked.</p>
    {:else if loader.error}
      <p class="cd-stale t-body-2" role="alert" data-testid="change-stale">The last refresh failed, so this may be out of date. <button type="button" class="btn btn-sm" onclick={() => loader.refresh()}>Retry</button></p>
    {/if}
    {#if !gone && (actions.includes('cancel') || actions.includes('rollback'))}
      <div class="cd-actions">
        {#if actions.includes('rollback')}
          <button type="button" class="btn" bind:this={rollbackBtn} onclick={() => (rollingBack = true)}>Roll back this change</button>
        {/if}
        {#if actions.includes('cancel')}
          {#if confirmCancel}
            <span class="t-body-2">Cancel this change? Nothing has moved yet, and nothing will.</span>
            <button type="button" class="btn btn-sm" onclick={cancel}>Yes, cancel it</button>
            <button type="button" class="btn btn-sm" bind:this={keepBtn} onclick={() => askCancel(false)}>Keep it</button>
          {:else}
            <button type="button" class="btn" bind:this={cancelBtn} onclick={() => askCancel(true)}>Cancel this change</button>
          {/if}
        {/if}
      </div>
    {/if}
    {#if cancelRefusal}<RefusalNotice error={cancelRefusal} />{/if}
    {#if rollingBack && !gone}
      <ProposePanel request={{ namespace, stage: e.spec.stage, type: 'pacto.dev/rollback', undoes: name }} {miraURL}
        onClose={() => { rollingBack = false; rollbackBtn?.focus(); }} />
    {/if}

    {#if decided}<p class="cd-decided" role="status" data-testid="decision-recorded">{decided}</p>{/if}
    {#if e.status.phase === 'Held' && !gone}
      <DecisionPanel {op} {actions} onDecided={(msg) => { decided = msg; loader.refresh(); }} />
    {/if}

    <section aria-labelledby="cd-affected">
      <h2 id="cd-affected" class="t-section-title">Affected services</h2>
      {#if consumers.length}
        <p class="t-body-2">These services are why a person is needed. Open one to see it on the operational graph.</p>
        <ul class="cd-consumers" data-testid="affected-services">
          {#each consumers as c (c)}<li><a href={serviceUrl(c)}>{c}</a></li>{/each}
        </ul>
      {:else}
        <p class="t-body-2">The plan names no other service this change affects.</p>
      {/if}
    </section>

    <div class="cd-compare">
      <section aria-labelledby="cd-predicted">
        <h2 id="cd-predicted" class="t-section-title">What Pacto predicted</h2>
        {#if st?.plan}
          <p class="t-body-2">{predictionLabel(st.plan.predicted)}</p>
          <VerdictList verdicts={st.plan.verdicts ?? []} {miraURL} testid="predicted-verdicts" />
        {:else}
          <p class="t-body-2">No plan was recorded for this change.</p>
        {/if}
      </section>
      <section aria-labelledby="cd-actual">
        <h2 id="cd-actual" class="t-section-title">What happened</h2>
        <VerdictList verdicts={e.verdicts ?? []} {miraURL} testid="actual-verdicts" />
      </section>
    </div>

    <section aria-labelledby="cd-decisions">
      <h2 id="cd-decisions" class="t-section-title">Decisions</h2>
      {#if st?.approvals?.length}
        <ul class="cd-approvals">
          {#each st.approvals as a (a.name)}
            <li class="t-body-2"><strong>{decisionLabel(a.decision)}</strong> by {a.by}{#if a.authTime}, signed in at {authTimeText(a.authTime)}{/if}{#if a.note}: “{a.note}”{/if}</li>
          {/each}
        </ul>
      {:else}
        <p class="t-body-2">Nobody has decided on this change.</p>
      {/if}
    </section>

    <section aria-labelledby="cd-timeline">
      <h2 id="cd-timeline" class="t-section-title">Timeline</h2>
      {#if timeline.length}
        <ol class="cd-timeline t-body-2">
          {#each timeline as t (t.k)}<li><time datetime={t.at}>{t.at}</time> {t.label}</li>{/each}
        </ol>
      {:else}
        <p class="t-body-2">Nothing has happened yet.</p>
      {/if}
    </section>
  {/if}
</div>

<style>
  .cd-stale { color: var(--c-err); margin: 0; }
  .cd-decided { color: var(--c-ok); font-weight: 600; margin: 0; }
  .cd-status { display: flex; gap: var(--sp-2); align-items: center; flex-wrap: wrap; }
  .cd-facts { display: grid; grid-template-columns: max-content 1fr; gap: 2px var(--sp-3); margin: 0; }
  .cd-facts dt { color: var(--c-text-3); }
  .cd-facts dd { margin: 0; overflow-wrap: anywhere; }
  .cd-facts a, .cd-consumers a { color: var(--c-accent); text-decoration: underline; }
  .cd-actions { display: flex; gap: var(--sp-2); align-items: center; flex-wrap: wrap; }
  .cd-consumers { display: flex; gap: var(--sp-3); flex-wrap: wrap; list-style: none; padding: 0; margin: var(--sp-2) 0 0; }
  .cd-compare { display: grid; grid-template-columns: repeat(auto-fit, minmax(320px, 1fr)); gap: var(--sp-4); }
  .cd-compare section { display: flex; flex-direction: column; gap: var(--sp-2); }
  .cd-approvals, .cd-timeline { margin: var(--sp-2) 0 0; padding-left: var(--sp-5); }
</style>
