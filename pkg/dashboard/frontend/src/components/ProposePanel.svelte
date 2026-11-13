<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.ts';
  import { deliveryChangeUrl } from '../lib/router.ts';
  import { typeLabel, predictionLabel, shortDigest, sentence, timeLabel, outcomeTone } from '../lib/delivery.ts';
  import VerdictList from './VerdictList.svelte';
  import RefusalNotice from './RefusalNotice.svelte';

  // Plan first, then propose. The plan says what the promoter would do; the
  // proposal carries the plan's intent digest, so anything that moved since is
  // refused with a fresh plan instead of acting on a stale one.
  // `preview` is a plan already made (the Why card's): shown, never proposed from here.
  let { request, toVersion = '', miraURL = '', onClose = undefined, preview = null } = $props();

  // svelte-ignore state_referenced_locally
  let plan = $state(preview);
  let planError = $state(null);
  let reason = $state('');
  let busy = $state(false);
  let refusal = $state(null);
  let moved = $state(false);
  let headingEl = $state(null);

  async function loadPlan() {
    planError = null;
    try { plan = await api.deliveryPlan(request); } catch (e) { planError = e; }
  }

  onMount(() => { if (!preview) { headingEl?.focus(); loadPlan(); } });

  async function propose(e) {
    e.preventDefault();
    busy = true;
    refusal = null;
    try {
      const out = await api.deliveryPropose({ ...request, reason: reason || undefined, intentDigest: plan.intentDigest });
      location.hash = deliveryChangeUrl(out.operation.metadata.namespace || request.namespace, out.operation.metadata.name);
    } catch (err) {
      if (err?.code === 'PlanStale' && err.plan) { plan = err.plan; moved = true; } else { refusal = err; }
    } finally {
      busy = false;
    }
  }

  function onKeydown(e) {
    if (e.key === 'Escape') { e.preventDefault(); onClose?.(); }
  }
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<section class="pp" aria-labelledby={preview ? undefined : 'pp-title'} onkeydown={onKeydown} data-testid="propose-panel" aria-label={preview ? 'The planned promote' : undefined}>
  <h3 id={preview ? undefined : 'pp-title'} class="t-subsection-title" tabindex="-1" bind:this={headingEl}>
    {typeLabel(request.type)} {request.namespace}/{request.stage}{#if request.to}: {toVersion || shortDigest(request.to)}{/if}
  </h3>
  {#if planError}
    <RefusalNotice error={planError} />
  {:else if !plan}
    <p class="t-body-2" role="status">Asking the cluster what would happen…</p>
  {:else}
    {#if moved}<p class="pp-moved t-body-2" role="alert">Something changed since the plan. This is the fresh plan; propose again if it still holds.</p>{/if}
    <p class="t-body" data-testid="plan-prediction"><strong>{predictionLabel(plan.predicted)}</strong></p>
    {#if plan.question}<p class="t-body-2">The question a person would be asked: {plan.question}</p>{/if}
    {#if plan.floor?.owners?.length}<p class="t-body-2">Only {plan.floor.owners.join(' or ')} may answer it.</p>{/if}
    {#if plan.conflicts?.length}<p class="t-body-2">In the way: {plan.conflicts.join(', ')}</p>{/if}
    {#if plan.next?.length}
      <div data-testid="plan-next"><p class="t-body-2"><strong>What happens next</strong></p>
        <ol class="pp-list t-body-2">{#each plan.next as line, i (i)}<li>{line}</li>{/each}</ol></div>
    {/if}
    {#if plan.order?.moveFirst?.length || plan.order?.unknown?.length}
      <div data-testid="plan-order"><p class="t-body-2"><strong>What must move first</strong></p>
        <ul class="pp-list t-body-2">
          {#each plan.order.moveFirst ?? [] as m, i (i)}<li><bdi>{m.message}</bdi></li>{/each}
          {#each plan.order.unknown ?? [] as u, i (i)}<li class="pp-unknown">Not known: <bdi>{u}</bdi></li>{/each}
        </ul></div>
    {/if}
    {#if plan.precedent}
      <div data-testid="plan-precedent"><p class="t-body-2"><strong>Earlier changes to this version</strong>{#if plan.precedent.completeness === 'partial'} <span class="t-meta">(partial: some records could not be read)</span>{/if}</p>
        {#if plan.precedent.entries?.length}
          <ul class="pp-list t-body-2">
            {#each plan.precedent.entries as e (e.stream + e.seq)}
              <li><span class="badge {outcomeTone(e.outcome)}">{e.outcome}</span> {typeLabel(e.type)}{#if e.finished}, {timeLabel(e.finished)}{/if}{#if e.reason}: <bdi>{e.reason}</bdi>{/if}</li>
            {/each}
          </ul>
        {:else}<p class="t-body-2">None recorded.</p>{/if}
        {#if plan.precedent.note}<p class="t-meta">{sentence(plan.precedent.note)}</p>{/if}
      </div>
    {/if}
    <VerdictList verdicts={plan.verdicts ?? []} {miraURL} testid="plan-verdicts" />
    {#if !preview}
    <form class="pp-form" onsubmit={propose}>
      <label class="pp-field">
        <span>Why (kept with the change)</span>
        <input class="input" type="text" bind:value={reason} maxlength="500" />
      </label>
      <div class="pp-btns">
        <button type="submit" class="btn btn-primary" disabled={busy}>{busy ? 'Proposing…' : `Propose ${typeLabel(request.type).toLowerCase()}`}</button>
        <button type="button" class="btn" onclick={() => onClose?.()}>Cancel</button>
      </div>
    </form>
    {/if}
  {/if}
  {#if refusal}<RefusalNotice error={refusal} />{/if}
  {#if planError}<button type="button" class="btn btn-sm" onclick={() => onClose?.()}>Close</button>{/if}
</section>

<style>
  .pp { border: 1px solid var(--c-accent-border); border-radius: var(--radius-md); padding: var(--sp-4); display: flex; flex-direction: column; gap: var(--sp-2); background: var(--c-surface); }
  .pp h3:focus { outline: none; }
  .pp p { margin: 0; }
  .pp-moved { color: var(--c-warn); }
  .pp-list { margin: var(--sp-1) 0 0; padding-inline-start: var(--sp-4); overflow-wrap: anywhere; }
  .pp-unknown { color: var(--c-text-2); }
  .pp-form { display: flex; flex-direction: column; gap: var(--sp-2); }
  .pp-field { display: flex; flex-direction: column; gap: 2px; font-size: var(--text-sm); color: var(--c-text-2); }
  .pp-btns { display: flex; gap: var(--sp-2); }
</style>
