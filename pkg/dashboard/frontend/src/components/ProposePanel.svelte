<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.ts';
  import { deliveryChangeUrl } from '../lib/router.ts';
  import { typeLabel, predictionLabel, shortDigest } from '../lib/delivery.ts';
  import VerdictList from './VerdictList.svelte';
  import RefusalNotice from './RefusalNotice.svelte';

  // Plan first, then propose. The plan says what the promoter would do; the
  // proposal carries the plan's intent digest, so anything that moved since is
  // refused with a fresh plan instead of acting on a stale one.
  let { request, miraURL = '', onClose } = $props();

  let plan = $state(null);
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

  onMount(() => { headingEl?.focus(); loadPlan(); });

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
<section class="pp" aria-labelledby="pp-title" onkeydown={onKeydown} data-testid="propose-panel">
  <h3 id="pp-title" class="t-subsection-title" tabindex="-1" bind:this={headingEl}>
    {typeLabel(request.type)} {request.namespace}/{request.stage}{#if request.to}: {shortDigest(request.to)}{/if}
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
    <VerdictList verdicts={plan.verdicts ?? []} {miraURL} testid="plan-verdicts" />
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
  {#if refusal}<RefusalNotice error={refusal} />{/if}
  {#if planError}<button type="button" class="btn btn-sm" onclick={() => onClose?.()}>Close</button>{/if}
</section>

<style>
  .pp { border: 1px solid var(--c-accent-border); border-radius: var(--radius-md); padding: var(--sp-4); display: flex; flex-direction: column; gap: var(--sp-2); background: var(--c-surface); }
  .pp h3:focus { outline: none; }
  .pp p { margin: 0; }
  .pp-moved { color: var(--c-warn); }
  .pp-form { display: flex; flex-direction: column; gap: var(--sp-2); }
  .pp-field { display: flex; flex-direction: column; gap: 2px; font-size: var(--text-sm); color: var(--c-text-2); }
  .pp-btns { display: flex; gap: var(--sp-2); }
</style>
