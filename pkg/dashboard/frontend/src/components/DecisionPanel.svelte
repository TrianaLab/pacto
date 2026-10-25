<script>
  import { untrack } from 'svelte';
  import { api } from '../lib/api.ts';
  import { deliverySession } from '../lib/deliverySession.svelte.ts';
  import { decisionAsked, decisionLabel, cliDecision, loginUrl, signIn, hostSignsIn, keepPending, pendingFor, dropPending, shortDigests,
    versionLabel, personLabel, isBreaking, andList, authTimeText, timeLabel, UNKNOWN_DOCS } from '../lib/delivery.ts';
  import CopyableIdentifier from './CopyableIdentifier.svelte';
  import RefusalNotice from './RefusalNotice.svelte';
  import MoveFirstList from './MoveFirstList.svelte';

  // The decision on a held change. The dashboard offers it only to a person
  // in a team the question names, in a shared dashboard; everyone else is told
  // why and, on a local dashboard, given the command that decides instead. A
  // decision needs a fresh sign-in: the person's choice is kept while they sign
  // in again and shown back to them, never sent on its own. Before the
  // buttons it says what each answer does, and the safer way out when there
  // is one; approving a breaking change needs a note saying why.
  let { op, actions = [], onDecided, versions = null, people = null, stage = null } = $props();

  const ns = $derived(op.metadata.namespace || '');
  const name = $derived(op.metadata.name);
  const asked = $derived(decisionAsked(op));
  const owners = $derived(op.status?.plan?.floor?.owners ?? []);
  const canDecide = $derived(actions.includes('approve') || actions.includes('accept-unknown'));
  const s = $derived(deliverySession.data);
  const caller = $derived(s?.caller);
  // Who may answer a question that names no team depends on the published
  // policy, which the session carries with the caller.
  const ownerless = $derived(!caller ? 'No team is declared.'
    : s?.requireOwnerTeam ? 'No team is declared, so nobody may answer; only break-glass can override.'
    : 'Any person may answer.');

  const st = $derived(op.status ?? {});
  const applied = $derived(!!st.applied);
  const env = $derived(`${ns}/${op.spec?.stage ?? ''}`);
  const from = $derived(op.spec?.params?.from || st.plan?.from || '');
  const to = $derived(op.spec?.params?.to || st.plan?.to || '');
  const toV = $derived(to ? versionLabel(to, versions) : 'this change');
  const fromV = $derived(from ? versionLabel(from, versions) : '');
  const consumers = $derived(st.plan?.floor?.consumers ?? []);
  const moveFirst = $derived(!applied && (st.plan?.order?.moveFirst?.length ?? 0) > 0);
  // Breaking matters before it ships: once applied, the question is only the Unknown.
  const breaking = $derived(!applied && isBreaking(st.plan?.verdicts));
  const asker = $derived(op.spec?.actor?.user ? personLabel(op.spec.actor.user, people) : 'whoever asked');
  // A rejected promote that already shipped fails, and the controller rolls a
  // failed promote back to the pointer it moved from, unless that version
  // already failed on this Stage (operation.go finish).
  const before = $derived(st.pointer?.before || '');
  const beforeFailed = $derived(!!before && (stage?.failedDigests ?? []).includes(before));
  const rollsBack = $derived(applied && op.spec?.type === 'pacto.dev/promote' && !!before && !beforeFailed);
  const backV = $derived(before ? versionLabel(before, versions) : '');

  // What each answer does, as the promoter acts on it.
  const ifApprove = $derived([
    applied
      ? `${toV} stays on ${env}, and the checks that ended Unknown count as accepted.`
      : `${toV} goes to ${env} now. The checks after the change still run.`,
    ...(!applied && consumers.length
      ? [asked === 'approve'
        ? `${andList(consumers)} do not accept it and may break until they are updated.`
        : `Pacto could not tell whether ${andList(consumers)} accept it.`]
      : []),
    `It is kept as your decision, with your name, your note and when you signed in.`,
    ...(applied ? [] : ['You can undo it from the history of the environment.']),
  ]);
  const ifReject = $derived([
    rollsBack ? `${env} rolls back to ${backV}.`
      : applied ? `Nothing moves: ${env} keeps running ${toV}${beforeFailed ? `, because ${backV} already failed here` : ''}.`
      : `Nothing moves: ${env} keeps running ${fromV || 'what it runs now'}.`,
    `The change ends, and ${asker} sees your name and note as the reason.`,
  ]);
  const approveLabel = $derived(asked === 'approve'
    ? `Approve shipping ${toV} to ${env}`
    : applied ? `Accept the unknown and keep ${toV}` : `Accept the unknown and ship ${toV}`);
  const rejectLabel = $derived(rollsBack ? `Reject and roll back to ${backV}` : 'Reject: nothing moves');

  // A decision kept across the fresh sign-in, if this is the change it was for.
  // Read once: the page is keyed by the change, so it never needs re-reading.
  let kept = $state(untrack(() => pendingFor(op.metadata.namespace || '', op.metadata.name)));
  let note = $state(untrack(() => kept?.note ?? ''));
  let busy = $state(false);
  let refusal = $state(null);
  let needsFresh = $state(null);
  let decided = $state('');
  const noteMissing = $derived(breaking && !note.trim());
  // A session the server already calls stale goes to sign in first, instead of
  // pressing a button only to be told to; back here, the kept choice is the
  // one button to press, and it says so.
  const stale = $derived(s?.fresh === false);
  const confirming = $derived(!!kept && !!s?.fresh);
  const signedInAt = $derived(caller?.authTime ? timeLabel(authTimeText(caller.authTime)) : '');
  const label = (decision, text) => (confirming && kept.decision === decision
    ? `Confirm: ${text}${signedInAt ? ` (signed in at ${signedInAt})` : ''}` : text);

  // Back from signing in, focus lands on the one button left to press.
  let approveBtn = $state(null);
  let rejectBtn = $state(null);
  $effect(() => { if (confirming) (kept.decision === 'reject' ? rejectBtn : approveBtn)?.focus(); });

  async function decide(decision) {
    if (stale) {
      needsFresh = { decision, message: '' };
      signInAgain();
      return;
    }
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
  {#if asked === 'accept-unknown'}
    <p class="t-body-2" data-testid="decision-unknown">Pacto is asking because a check ended Unknown: it could not see enough to say yes or no, and Unknown is never a yes. Accepting the unknown is recorded as accepted, never as a pass. <a class="dp-link" href={UNKNOWN_DOCS} target="_blank" rel="noopener noreferrer">What Unknown means and how to resolve it</a></p>
  {/if}
  <p class="t-body-2">Asked: <strong>{decisionLabel(asked)}</strong> or reject. {owners.length ? `Only ${owners.join(' or ')} may answer.` : ownerless}{#if breaking}{' '}<span class="badge badge-warn" data-testid="decision-breaking">Breaking</span>{/if}</p>

  {#if !decided}
    <div class="dp-outcomes" data-testid="decision-consequences">
      <div class="dp-outcome">
        <h3 class="t-body">If you approve</h3>
        <ul>{#each ifApprove as line (line)}<li>{line}</li>{/each}</ul>
      </div>
      <div class="dp-outcome">
        <h3 class="t-body">If you reject</h3>
        <ul>{#each ifReject as line (line)}<li>{line}</li>{/each}</ul>
      </div>
    </div>
    {#if moveFirst}
      <div class="dp-safer" data-testid="decision-safer">
        <h3 class="t-body">Safer: update these first</h3>
        <p class="t-body-2">Pacto keeps this list current while the change waits. Each team drops out once its service runs a version that accepts the new one, and when nobody is left the change can be approved as it is. Pacto never moves them for you.</p>
        <MoveFirstList order={st.plan.order} />
      </div>
    {/if}
  {/if}

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
      <p class="dp-kept t-body-2" role="status" data-testid="decision-kept">You signed in again. Your choice was <strong>{decisionLabel(kept.decision)}</strong>, and nothing was sent yet: confirm it below to send it.</p>
    {/if}
    {#if needsFresh}
      <div class="dp-fresh" role="alert" data-testid="fresh-login">
        <p><strong>Deciding needs a fresh sign-in.</strong> {needsFresh.message}</p>
        <p class="t-body-2">Your choice and note are kept. After you sign in you come back here and press it once more.</p>
        <button type="button" class="btn btn-primary btn-sm" onclick={signInAgain}>Sign in again</button>
      </div>
    {/if}
    <label class="dp-field">
      <span>Note (kept with the decision){#if breaking}, required to approve a breaking change{/if}</span>
      <textarea class="input" rows="2" maxlength="500" bind:value={note} aria-describedby={noteMissing ? 'dp-note-why' : undefined}></textarea>
    </label>
    {#if stale && !needsFresh}<p class="t-body-2" data-testid="decision-stale">A decision needs a recent sign-in, and yours is older. Pressing a button below takes you to sign in again; your choice and note are kept, and you confirm it once you are back.</p>{/if}
    {#if noteMissing}<p id="dp-note-why" class="t-body-2" data-testid="decision-note-required">This change is breaking: say why it should ship anyway before you approve it.</p>{/if}
    <div class="dp-btns">
      <button type="button" class="btn" data-testid="decide-approve" bind:this={approveBtn} class:dp-was={kept?.decision === asked} disabled={busy || noteMissing} onclick={() => decide(asked)}>{label(asked, approveLabel)}</button>
      <button type="button" class="btn" data-testid="decide-reject" bind:this={rejectBtn} class:dp-was={kept?.decision === 'reject'} disabled={busy} onclick={() => decide('reject')}>{label('reject', rejectLabel)}</button>
    </div>
  {:else if caller?.user && (caller.user === op.spec?.actor?.user || caller.user === op.spec?.onBehalfOf?.user)}
    <p class="t-body-2" data-testid="decide-own">You asked for this change, so someone else must decide it.</p>
  {:else if caller?.class === 'agent' || caller?.class === 'controller'}
    <p class="t-body-2" data-testid="decide-agent">The cluster says you are an agent ({caller.user}), and agents may not decide.</p>
  {:else if !owners.length}
    <p class="t-body-2" data-testid="decide-no-owner">
      No team is declared for this service, so only break-glass can override, from the command line. Declare service.owner so a team can answer.
    </p>
  {:else}
    <p class="t-body-2" data-testid="decide-not-team">
      Only {owners.join(' or ')} may answer this question{#if caller?.groups?.length}; you are in {caller.groups.join(', ')}{/if}.
    </p>
  {/if}
  {#if refusal}<RefusalNotice error={refusal} />{/if}
</section>

<style>
  .dp { border: 1px solid var(--c-warn-border); background: var(--c-warn-bg); border-radius: var(--radius-md); padding: var(--sp-4); display: flex; flex-direction: column; gap: var(--sp-2); }
  .dp p { margin: 0; }
  .dp-question { font-weight: 600; }
  .dp-field { display: flex; flex-direction: column; gap: 2px; font-size: var(--text-sm); color: var(--c-text-2); }
  .dp-btns { display: flex; gap: var(--sp-2); flex-wrap: wrap; }
  .dp-btns .btn { flex: 1 1 0; min-width: 12rem; }
  .dp-outcomes { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(260px, 100%), 1fr)); gap: var(--sp-2); }
  .dp-outcome, .dp-safer { background: var(--c-surface); border: 1px solid var(--c-border); border-radius: var(--radius-sm); padding: var(--sp-3); display: flex; flex-direction: column; gap: var(--sp-1); }
  .dp-outcome h3, .dp-safer h3 { margin: 0; font-weight: 600; }
  .dp-outcome ul { margin: 0; padding-left: var(--sp-5); }
  .dp-was { outline: 2px solid var(--c-accent); outline-offset: 2px; }
  /* The warn tint is translucent: on the panel's own warn tint it loses contrast, so lay it on the surface. */
  .dp .badge-warn { background: linear-gradient(var(--c-warn-bg), var(--c-warn-bg)), var(--c-surface); }
  .dp-link { color: var(--c-accent); text-decoration: underline; }
  .dp-ok { color: var(--c-ok); font-weight: 600; }
  .dp-fresh { border: 1px solid var(--c-accent-border); background: var(--c-surface); border-radius: var(--radius-sm); padding: var(--sp-3); display: flex; flex-direction: column; gap: var(--sp-1); align-items: flex-start; }
</style>
