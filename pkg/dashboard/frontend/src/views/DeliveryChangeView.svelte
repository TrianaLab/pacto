<script>
  import Person from '../components/Person.svelte';
  import { onDestroy, tick } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { deliveryInboxUrl, deliveryHistoryUrl, deliveryChangeUrl } from '../lib/router.ts';
  import { deliveryServiceUrl } from '../lib/deliveryLinks.svelte.ts';
  import { typeLabel, shortDigest, shortDigests, predictionLabel, decisionLabel, authTimeText, changeSummary, actorBadge,
    undoCause, versionLabel, levelLabel, levelHelp, timeLabel, AUTONOMY_TYPE, reasonWords, reasonAfter, gateLabel, andList, decisionAsked } from '../lib/delivery.ts';
  import DeliveryLead from '../components/DeliveryLead.svelte';
  import CappedList from '../components/CappedList.svelte';
  import { formatCount } from '../lib/format.ts';
  import { deliverySession } from '../lib/deliverySession.svelte.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import DeliveryNav from '../components/DeliveryNav.svelte';
  import DeliveryProblem from '../components/DeliveryProblem.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import VerdictList from '../components/VerdictList.svelte';
  import DecisionPanel from '../components/DecisionPanel.svelte';
  import ProposePanel from '../components/ProposePanel.svelte';
  import RefusalNotice from '../components/RefusalNotice.svelte';
  import Fold from '../components/Fold.svelte';
  import HelpTip from '../components/HelpTip.svelte';

  // One change. It leads with where it stands and, while held, the decision;
  // the affected services, the checks with their proof and the details (facts,
  // decisions, timeline) fold below. A fold with a problem in it starts open.
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
  const me = $derived({ user: deliverySession.data?.caller?.user, name: deliverySession.data?.name });
  const asker = $derived(actorBadge(e?.status?.actorClass));
  // What Pacto saw after the change only exists once it applied; before that
  // it is the plan's checks again, so it stays hidden.
  const happened = $derived(!!st?.applied || e?.status?.phase === 'Concluded');
  const after = $derived(st?.pointer?.after || '');
  const blocked = $derived(!!after && (e?.stage?.failedDigests ?? []).includes(after));
  const held = $derived(e?.status?.phase === 'Held');
  const checks = $derived([...(st?.plan?.verdicts ?? []), ...(happened ? e?.verdicts ?? [] : [])]);
  const concluded = $derived(e?.status?.phase === 'Concluded');
  // A concluded change says its failing check in its answer line, so the checks stay
  // folded; before then anything but a pass is a problem worth reading, so they open.
  const checksOpen = $derived(!concluded && checks.some((v) => v.result && v.result !== 'Pass'));
  const failing = $derived([...(happened ? e?.verdicts ?? [] : []), ...(st?.plan?.verdicts ?? [])].find((v) => v.result === 'Fail') ?? null);
  // The reason in words, without the code the summary already says ("AcceptedUnknown: ...").
  const reasonText = $derived.by(() => {
    const r = reasonAfter(e?.status?.reason, e?.status?.verification, e?.status?.outcome);
    const m = shortDigests(e?.status?.message);
    return [reasonWords(r), m].filter(Boolean).join(': ');
  });

  // While held, the page's title is the question, and one line says what breaks.
  const toD = $derived(e?.spec?.params?.to || st?.plan?.to || '');
  const toV = $derived(!toD ? '' : e?.spec?.type === AUTONOMY_TYPE ? levelLabel(toD) : versionLabel(toD, data?.versions));
  const env = $derived(`${namespace}/${e?.spec?.stage ?? ''}`);
  const heldTitle = $derived.by(() => {
    if (!e) return '';
    if (st?.applied) return `Keep ${toV || 'this change'} on ${env}?`;
    switch (e.spec.type) {
      case 'delivery.pacto.trianalab.io/promote': return `Ship ${toV || 'this version'} to ${env}?`;
      case 'delivery.pacto.trianalab.io/rollback': return `Roll ${env} back to ${toV || 'the version before'}?`;
      case AUTONOMY_TYPE: return `Move ${env} to ${toV || 'a new level'}?`;
      default: return `${typeLabel(e.spec.type)} on ${env}?`;
    }
  });
  const heldLead = $derived.by(() => {
    if (!op) return null;
    if (decisionAsked(op) === 'approve' && consumers.length && !st?.applied) {
      return { head: `It breaks ${andList(consumers, 3)}.`, rest: `${consumers.length === 1 ? 'Its declared version range does' : 'Their declared version ranges do'} not accept ${toV || 'it'}.` };
    }
    const q = shortDigests(st?.question ?? '').split(/(?<=[.?])\s/)[0];
    return decisionAsked(op) === 'accept-unknown' ? { head: 'A check ended Unknown.', rest: q } : q ? { head: q, rest: '' } : null;
  });
  const checksMeta = $derived.by(() => {
    const n = {};
    for (const v of checks) n[v.result || 'Not run'] = (n[v.result || 'Not run'] ?? 0) + 1;
    return Object.entries(n).map(([r, c]) => `${formatCount(c)} ${r.toLowerCase()}`).join(' · ');
  });

  // The change that undid this one: the server reads it from the
  // environment's history, so the page never downloads the whole history.
  const undo = $derived(data?.undoneBy ?? null);

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
  // The button pressed goes away with what it did, so focus moves to the
  // message that says what happened, not to the top of the page.
  let doneMsg = $state(null);
  async function settleFocus() {
    await tick();
    doneMsg?.focus();
  }
  async function cancel() {
    cancelRefusal = null;
    try { await api.deliveryCancel(namespace, name); cancelled = true; loader.refresh(); }
    catch (err) { cancelRefusal = err; }
    confirmCancel = false;
    if (cancelled) await settleFocus();
  }
</script>

{#snippet changeActions()}
  {#if !gone && (actions.includes('cancel') || actions.includes('rollback'))}
    <div class="cd-actions">
      {#if actions.includes('rollback')}
        <button type="button" class="btn" bind:this={rollbackBtn} onclick={() => (rollingBack = true)} data-testid="change-rollback">Roll back this change</button>
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
{/snippet}

<div class="product-page">
  <Breadcrumbs trail={[{ label: 'Delivery', href: deliveryInboxUrl() }, { label: `${namespace}/${name}` }]} />
  <PageHeader
    eyebrow="Change"
    title={e ? (held ? heldTitle : `${typeLabel(e.spec.type)} on ${namespace}/${e.spec.stage}`) : `${namespace}/${name}`}
    subtitle={changeSummary(e, namespace, data?.versions)}
    revalidating={!!data && loader.loading}
  />
  <!-- The tab this change is listed under: the inbox while it waits for this person,
       history once it has moved on. A held change for another team is in neither. -->
  <DeliveryNav current={!e ? '' : !held ? 'delivery-history' : actions.some((a) => a === 'approve' || a === 'accept-unknown') ? 'delivery-inbox' : ''} />

  {#if !data && loader.loading}
    <EmptyState loading={true} />
  {:else if !data && loader.error}
    {#if loader.error.status === 404 && loader.error.code !== 'DeliveryNotInstalled'}
      <EmptyState error={true} title="No such change" message={`${namespace}/${name} does not exist, or it finished and was cleaned up. ${loader.error.message}`} />
    {:else}
      <DeliveryProblem error={loader.error} onRetry={() => loader.refresh()} />
    {/if}
  {:else if data}
    <!-- Where it stands is the subtitle's sentence; this line adds why, never the phase
         and outcome again as chips. While held, the line says what breaks. -->
    {#if held}
      {#if heldLead}<DeliveryLead head={heldLead.head} rest={heldLead.rest} testid="change-answer" />{/if}
    {:else if failing}
      <DeliveryLead head={`${gateLabel(failing.gate)} failed${failing.message ? `: ${shortDigests(failing.message)}` : ''}.`} rest={reasonText && !/^A check failed/.test(reasonText) ? reasonText : ''} testid="change-answer" />
    {:else if reasonText}
      <p class="cd-status t-body-2" data-testid="change-answer" title={e.status.reason}>{reasonText}</p>
    {/if}

    {#if undo || blocked}
      <div class="cd-undone t-body-2" data-testid="change-undone">
        {#if undo}<p>Undone by <a href={deliveryChangeUrl(namespace, undo.operation)}>{undo.operation}</a>{#if undo.actorClass === 'controller'}{@const cause = undoCause(e.status.reason, e.status.message)}{' '}(automatic{#if cause.by}: <Person user={cause.by} people={data?.people} {me} /> rejected the unknown{:else if cause.text}: {cause.text}{/if}){:else if undo.actor}, by <Person user={undo.actor} people={data?.people} {me} />{/if}.</p>{/if}
        {#if blocked}<p>{versionLabel(after, data?.versions)} is blocked on {namespace}/{e.spec.stage}: it failed its checks here, so Pacto never promotes it here again.</p>{/if}
      </div>
    {/if}

    {#if cancelled}
      <p class="t-body-2" role="status" tabindex="-1" bind:this={doneMsg}>Cancelled. Nothing changed.</p>
    {:else if gone}
      <p class="cd-stale t-body-2" role="alert" data-testid="change-gone">This change is gone: it was cancelled or cleaned up. This is how it last looked.</p>
    {:else if loader.error}
      <p class="cd-stale t-body-2" role="alert" data-testid="change-stale">The last refresh failed, so this may be out of date. <button type="button" class="btn btn-sm" onclick={() => loader.refresh()}>Retry</button></p>
    {/if}
    {#if !held}{@render changeActions()}{/if}
    {#if cancelRefusal}<RefusalNotice error={cancelRefusal} />{/if}
    {#if rollingBack && !gone}
      <ProposePanel request={{ namespace, stage: e.spec.stage, type: 'delivery.pacto.trianalab.io/rollback', undoes: name }} {miraURL}
        onClose={() => { rollingBack = false; rollbackBtn?.focus(); }} />
    {/if}

    {#if decided}<p class="cd-decided" role="status" tabindex="-1" data-testid="decision-recorded" bind:this={doneMsg}>{decided}</p>{/if}
    {#if e.status.phase === 'Held' && !gone}
      <DecisionPanel {op} {actions} stage={e.stage} versions={data?.versions} people={data?.people} onDecided={(msg) => { decided = msg; loader.refresh(); settleFocus(); }} />
    {/if}

    <div class="cd-folds">
      {#if consumers.length}
        <Fold title={`Affected services (${formatCount(consumers.length)})`} id="cd-affected" labelledby="cd-affected" meta="why a person is needed">
          <div class="cd-consumers">
            <CappedList items={consumers} max={12} inline={true} label="Affected services" testid="affected-services">{#snippet item(c)}<a href={deliveryServiceUrl(c)}>{c}</a>{/snippet}</CappedList>
          </div>
        </Fold>
      {/if}

      <Fold title="Checks" meta={checksMeta || 'none recorded'} open={checksOpen} testid="change-checks">
        <div class="cd-compare">
          <section aria-labelledby="cd-predicted">
            <h3 id="cd-predicted" class="t-subsection-title">What Pacto predicted</h3>
            {#if st?.plan}
              <p class="t-body-2">{predictionLabel(st.plan.predicted)}</p>
              <VerdictList verdicts={st.plan.verdicts ?? []} {miraURL} testid="predicted-verdicts" />
            {:else}
              <p class="t-body-2">No plan was recorded for this change.</p>
            {/if}
          </section>
          {#if happened}
            <section aria-labelledby="cd-actual">
              <h3 id="cd-actual" class="t-subsection-title">What happened</h3>
              <VerdictList verdicts={e.verdicts ?? []} {miraURL} testid="actual-verdicts" />
            </section>
          {/if}
        </div>
      </Fold>

      <Fold title="Details" meta="environment, versions, decisions, timeline" testid="change-details">
        <dl class="cd-facts t-body-2">
          {#if st?.question}<dt>Question</dt><dd data-testid="change-question">{shortDigests(st.question)}</dd>{/if}
          <dt>Environment</dt><dd><a href={deliveryHistoryUrl({ namespace, stage: e.spec.stage })}>{namespace}/{e.spec.stage}</a>{#if e.stage.frozen}{' '}(frozen{#if e.stage.frozenReason}: {shortDigests(e.stage.frozenReason)}{/if}){/if}</dd>
          {#if data.level}<dt>Level</dt><dd data-testid="change-level"><span class="badge badge-neutral">{levelLabel(data.level)}</span>{' '}<HelpTip label={levelLabel(data.level)} text={levelHelp(data.level)} /></dd>{/if}
          {#each [['From', e.spec.params?.from], ['To', e.spec.params?.to]] as [label, d] (label)}
            {#if d && e.spec.type === AUTONOMY_TYPE}<dt>{label}</dt><dd>{levelLabel(d)}</dd>
            {:else if d}<dt>{label}</dt><dd>{#if data.versions?.[d]}{data.versions[d]}{' '}{/if}<code class="cd-digest" title={d}>{shortDigest(d)}</code>{#if !data.versions?.[d]}{' '}<span class="cd-digest">(version unknown)</span>{/if}</dd>{/if}
          {/each}
          <dt>Asked by</dt><dd><Person user={e.spec.actor.user} people={data?.people} {me} />{#if asker}{' '}<span class="badge {asker.tone}" data-testid="actor-badge">{asker.label}</span>{/if}{#if e.spec.onBehalfOf?.user}, for <Person user={e.spec.onBehalfOf.user} people={data?.people} {me} />{/if}</dd>
          {#if e.spec.reason}<dt>Why</dt><dd>{e.spec.reason}</dd>{/if}
        </dl>

        <section aria-labelledby="cd-decisions">
          <h3 id="cd-decisions" class="t-subsection-title">Decisions</h3>
          {#if st?.approvals?.length}
            <ul class="cd-approvals">
              {#each st.approvals as a (a.name)}
                <li class="t-body-2"><strong>{decisionLabel(a.decision)}</strong> by <Person user={a.by} people={data?.people} {me} />{#if a.authTime}, signed in at {authTimeText(a.authTime)}{/if}{#if a.note}: “{a.note}”{/if}</li>
              {/each}
            </ul>
          {:else}
            <p class="t-body-2">Nobody has decided yet.</p>
          {/if}
        </section>
        <section aria-labelledby="cd-timeline">
          <h3 id="cd-timeline" class="t-subsection-title">Timeline</h3>
          {#if timeline.length}
            <ol class="cd-timeline t-body-2">
              {#each timeline as t (t.k)}<li><time datetime={t.at} title={t.at}>{timeLabel(t.at)}</time> {t.label}</li>{/each}
            </ol>
          {:else}
            <p class="t-body-2">Nothing has happened yet.</p>
          {/if}
        </section>
        {#if held}{@render changeActions()}{/if}
      </Fold>
    </div>
  {/if}
</div>

<style>
  .cd-stale { color: var(--c-err); margin: 0; }
  .cd-undone p { margin: 0; }
  .cd-undone a { color: var(--c-accent); text-decoration: underline; }
  .cd-decided { color: var(--c-ok); font-weight: 600; margin: 0; }
  .cd-status { margin: 0; }
  .cd-facts { display: grid; grid-template-columns: max-content 1fr; gap: 2px var(--sp-3); margin: 0; }
  .cd-facts dt { color: var(--c-text-3); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); letter-spacing: var(--role-label-tracking); }
  .cd-facts dd { margin: 0; overflow-wrap: anywhere; }
  .cd-digest { color: var(--c-text-2); }
  .cd-facts a, .cd-consumers a { color: var(--c-accent); text-decoration: underline; }
  .cd-actions { display: flex; gap: var(--sp-2); align-items: center; flex-wrap: wrap; }
  .cd-consumers { margin: var(--sp-2) 0 0; display: flex; flex-direction: column; gap: var(--sp-1); align-items: flex-start; }
  .cd-compare { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(320px, 100%), 1fr)); gap: var(--sp-4); }
  .cd-compare section { display: flex; flex-direction: column; gap: var(--sp-2); }
  .cd-approvals, .cd-timeline { margin: var(--sp-1) 0 0; padding-left: var(--sp-5); }
  .cd-folds section h3 { margin: 0; }
  .cd-folds { display: flex; flex-direction: column; }
</style>
