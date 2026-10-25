<script>
  import { untrack } from 'svelte';
  import { api } from '../lib/api.ts';
  import { deliverySession } from '../lib/deliverySession.svelte.ts';
  import { decisionAsked, decisionLabel, cliDecision, loginUrl, signIn, hostSignsIn, keepPending, pendingFor, dropPending, shortDigests } from '../lib/delivery.ts';
  import CopyableIdentifier from './CopyableIdentifier.svelte';
  import RefusalNotice from './RefusalNotice.svelte';

  // The decision on a held change. The dashboard offers it only to a person
  // in a team the question names, in a shared dashboard; everyone else is told
  // why and, on a local dashboard, given the command that decides instead. A
  // decision needs a fresh sign-in: the person's choice is kept while they sign
  // in again and shown back to them, never sent on its own.
  let { op, actions = [], onDecided } = $props();

  const ns = $derived(op.metadata.namespace || '');
  const name = $derived(op.metadata.name);
  const asked = $derived(decisionAsked(op));
  const owners = $derived(op.status?.plan?.floor?.owners ?? []);
  const canDecide = $derived(actions.includes('approve') || actions.includes('accept-unknown'));
  const s = $derived(deliverySession.data);
  const caller = $derived(s?.caller);

  // A decision kept across the fresh sign-in, if this is the change it was for.
  // Read once: the page is keyed by the change, so it never needs re-reading.
  let kept = $state(untrack(() => pendingFor(op.metadata.namespace || '', op.metadata.name)));
  let note = $state(untrack(() => kept?.note ?? ''));
  let busy = $state(false);
  let refusal = $state(null);
  let needsFresh = $state(null);
  let decided = $state('');

  async function decide(decision) {
    busy = true;
    refusal = null;
    needsFresh = null;
    try {
      const out = await api.deliveryDecide(ns, name, decision, note);
      dropPending();
      decided = `${decisionLabel(decision)} recorded as ${out.approval}. The promoter acts on it shortly.`;
      onDecided?.(decided);
    } catch (e) {
      if (e?.code === 'FreshLoginRequired') {
        needsFresh = { decision, message: e.message };
      } else {
        refusal = e;
      }
    } finally {
      busy = false;
    }
  }

  function signInAgain() {
    const pending = { namespace: ns, name, decision: needsFresh.decision, note };
    keepPending(pending);
    // A host that signs people in itself never reloads this page, so the kept
    // choice is shown back from here once the session is fresh.
    if (hostSignsIn()) { kept = pending; needsFresh = null; }
    signIn(location.hash, true);
  }
</script>

<section class="dp" aria-labelledby="dp-title" data-testid="decision-panel">
  <h2 id="dp-title" class="t-section-title">The decision</h2>
  {#if op.status?.question}<p class="dp-question t-body">{shortDigests(op.status.question)}</p>{/if}
  <p class="t-body-2">Asked: <strong>{decisionLabel(asked)}</strong> or reject. {owners.length ? `Only ${owners.join(' or ')} may answer.` : 'Any person may answer.'}</p>

  {#if decided}
    <p class="dp-ok" role="status">{decided}</p>
  {:else if s?.mode === 'local'}
    <p class="t-body-2" data-testid="decide-cli">A dashboard on your own machine does not decide, because any program here could do it as you. Decide from the command line:</p>
    <CopyableIdentifier value={cliDecision(ns, name, asked)} label="command" />
  {:else if s && !s.signedIn}
    <p class="t-body-2">Sign in to decide.</p>
    <a class="btn btn-sm btn-primary" href={loginUrl(location.hash)}>Sign in</a>
  {:else if canDecide}
    {#if kept && s?.fresh}
      <p class="dp-kept t-body-2" role="status" data-testid="decision-kept">You signed in again. Your choice was <strong>{decisionLabel(kept.decision)}</strong>; press it again to send it.</p>
    {/if}
    {#if needsFresh}
      <div class="dp-fresh" role="alert" data-testid="fresh-login">
        <p><strong>Deciding needs a fresh sign-in.</strong> {needsFresh.message}</p>
        <p class="t-body-2">Your choice and note are kept. After you sign in you come back here and press it once more.</p>
        <button type="button" class="btn btn-primary btn-sm" onclick={signInAgain}>Sign in again</button>
      </div>
    {/if}
    <label class="dp-field">
      <span>Note (kept with the decision)</span>
      <textarea class="input" rows="2" maxlength="500" bind:value={note}></textarea>
    </label>
    <div class="dp-btns">
      <button type="button" class="btn btn-primary" class:dp-was={kept?.decision === asked} disabled={busy} onclick={() => decide(asked)}>{decisionLabel(asked)}</button>
      <button type="button" class="btn" class:dp-was={kept?.decision === 'reject'} disabled={busy} onclick={() => decide('reject')}>Reject</button>
    </div>
  {:else if caller?.user && (caller.user === op.spec?.actor?.user || caller.user === op.spec?.onBehalfOf?.user)}
    <p class="t-body-2" data-testid="decide-own">You asked for this change, so someone else must decide it.</p>
  {:else if caller?.class === 'agent' || caller?.class === 'controller'}
    <p class="t-body-2" data-testid="decide-agent">The cluster says you are an agent ({caller.user}), and agents may not decide.</p>
  {:else}
    <p class="t-body-2" data-testid="decide-not-team">
      Only {owners.join(' or ') || 'the named team'} may answer this question{#if caller?.groups?.length}; you are in {caller.groups.join(', ')}{/if}.
    </p>
  {/if}
  {#if refusal}<RefusalNotice error={refusal} />{/if}
</section>

<style>
  .dp { border: 1px solid var(--c-warn-border); background: var(--c-warn-bg); border-radius: var(--radius-md); padding: var(--sp-4); display: flex; flex-direction: column; gap: var(--sp-2); }
  .dp p { margin: 0; }
  .dp-question { font-weight: 600; }
  .dp-field { display: flex; flex-direction: column; gap: 2px; font-size: var(--text-sm); color: var(--c-text-2); }
  .dp-btns { display: flex; gap: var(--sp-2); }
  .dp-was { outline: 2px solid var(--c-accent); outline-offset: 2px; }
  .dp-ok { color: var(--c-ok); font-weight: 600; }
  .dp-fresh { border: 1px solid var(--c-accent-border); background: var(--c-surface); border-radius: var(--radius-sm); padding: var(--sp-3); display: flex; flex-direction: column; gap: var(--sp-1); align-items: flex-start; }
</style>
