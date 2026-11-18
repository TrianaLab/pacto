<script>
  import { untrack } from 'svelte';
  import { api } from '../lib/api.ts';
  import { session, me, signIn, hostSignsIn, personLabel, teamGroups } from '../lib/session.svelte.ts';
  import { decisionAsked, decisionLabel, cliDecision, keepPending, pendingFor, dropPending,
    versionLabel, isBreaking, andList, authTimeText, timeLabel, UNKNOWN_DOCS, APPROVING_DOCS, cannotApproveHere, samePerson } from '../lib/delivery.ts';
  import { formatCount } from '../lib/format.ts';
  import { formatRelative } from '../lib/dateFormat.ts';
  import CopyableIdentifier from './CopyableIdentifier.svelte';
  import RefusalNotice from './RefusalNotice.svelte';
  import SignInLink from './SignInLink.svelte';
  import MoveFirstList from './MoveFirstList.svelte';

  // The decision on a held change. The dashboard offers it only to a person
  // in a team the question names, in a shared dashboard; everyone else is told
  // why and, on a local dashboard, given the command that decides instead.
  // Where the Stage asks for a recent sign-in (freshness, in seconds; zero when
  // any signed-in person decides), the panel says so once the person's sign-in
  // is too old, and a refused decision opens a dialog that sends them to sign
  // in again: their choice and note are kept and shown back to them, never
  // sent on their own. Before the buttons it says what each answer does, and
  // the safer way out when there is one; approving a breaking change needs a
  // note saying why.
  // decisionRefusal is the server's code for why a held change offers this
  // caller no decision (operation endpoint), so the panel says the real cause.
  let { op, actions = [], decisionRefusal = '', onDecided, versions = null, people = null, stage = null, freshness = 0 } = $props();

  const ns = $derived(op.metadata.namespace || '');
  const name = $derived(op.metadata.name);
  const asked = $derived(decisionAsked(op));
  const owners = $derived(op.status?.plan?.floor?.owners ?? []);
  const canDecide = $derived(actions.includes('approve') || actions.includes('accept-unknown'));
  const s = $derived(session.data);
  const caller = $derived(session.caller);
  // Who may answer depends on the published policy, which delivery's caller
  // read carries: with owner teams narrowing it off, whoever may update the
  // Stage's workload answers and the teams are only who was asked.
  const unnarrowed = $derived(caller?.teamNarrowing === false);
  // Update rights on the workload decide whether teams narrow or not.
  const ownerless = $derived(!caller ? 'No team is declared.'
    : caller.requireOwnerTeam ? 'No team is declared, so nobody may answer; only break-glass can override.'
    : 'People who may update this workload may answer.');

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
  const rollsBack = $derived(applied && op.spec?.type === 'delivery.pacto.trianalab.io/promote' && !!before && !beforeFailed);
  const backV = $derived(before ? versionLabel(before, versions) : '');

  // What each answer does, as the promoter acts on it. Short lines: the
  // details of the change sit folded below the panel.
  const ifApprove = $derived([
    applied
      ? `${toV} stays on ${env}, and the Unknown checks count as accepted.`
      : `${toV} goes to ${env} now.`,
    ...(!applied && consumers.length
      ? [asked === 'approve'
        ? `${andList(consumers, 3)} may break until they are updated.`
        : `Pacto could not tell whether ${andList(consumers, 3)} accept it.`]
      : []),
    ...(applied ? [] : ['You can undo it later.']),
  ]);
  // Thirty owner teams are named three at a time, the rest on request.
  let allOwners = $state(false);
  const ownerList = $derived(andList(owners, allOwners ? Infinity : 3, 'or'));
  const ownersCut = $derived(owners.length > 4);
  // The team that lets this person answer, named back to them. The cluster maps a team
  // to a group by a prefix the session does not carry, so a group that ends in the team
  // name is taken as that team; failing a match the line still says they can answer.
  const myTeam = $derived(owners.find((t) => teamGroups(me().groups).some((g) => g === t || g.endsWith(t))) ?? '');
  // The page lists every affected service in a fold below the panel: open it, then go there.
  function toAffected() {
    const h = document.getElementById('cd-affected');
    const fold = h?.closest('details');
    if (fold) fold.open = true;
    h?.scrollIntoView?.();
  }
  const ifReject = $derived([
    rollsBack ? `${env} rolls back to ${backV}.`
      : applied ? `Nothing moves: ${env} keeps running ${toV}${beforeFailed ? `, because ${backV} already failed here` : ''}.`
      : `Nothing moves: ${env} keeps running ${fromV || 'what it runs now'}.`,
    `${asker} sees your note.`,
  ]);
  // A button names the version, cut short when it is long (the title keeps it
  // whole); the environment is in the panel's heading, not on the button.
  const clip = (v) => (v.length > 24 ? `${v.slice(0, 23)}…` : v);
  const approveLabel = $derived(asked === 'approve'
    ? `Approve shipping ${clip(toV)}`
    : applied ? `Accept the unknown and keep ${clip(toV)}` : `Accept the unknown and ship ${clip(toV)}`);
  const rejectLabel = $derived(rollsBack ? `Reject and roll back to ${clip(backV)}` : 'Reject: nothing moves');

  // A decision kept across the fresh sign-in, if this is the change it was for.
  // Read once: the page is keyed by the change, so it never needs re-reading.
  let kept = $state(untrack(() => pendingFor(op.metadata.namespace || '', op.metadata.name)));
  let note = $state(untrack(() => kept?.note ?? ''));
  let busy = $state(false);
  let refusal = $state(null);
  // The decision the server refused for an old sign-in: the dialog is open while set.
  let needsFresh = $state(null);
  let decided = $state('');
  const noteMissing = $derived(breaking && !note.trim());
  // The note and a button per answer are offered only to a signed-in person in a shared dashboard who may decide.
  const offer = $derived(!!s && s.mode !== 'local' && s.signedIn && canDecide);
  // Back from signing in again, the kept choice is the one button to press, and it says so.
  const confirming = $derived(!!kept);
  const signedInAt = $derived(caller?.authTime ? timeLabel(authTimeText(caller.authTime)) : '');
  // How old the sign-in is, read again every half minute while the Stage asks
  // for a recent one, so the line appears when it goes stale on an open page.
  let now = $state(Date.now());
  $effect(() => {
    if (!freshness) return;
    const t = setInterval(() => { now = Date.now(); }, 30_000);
    return () => clearInterval(t);
  });
  const signedIso = $derived(caller?.authTime ? authTimeText(caller.authTime) : '');
  const windowMinutes = $derived(Math.max(1, Math.round(freshness / 60)));
  const needs = $derived(`Deciding needs a sign-in in the last ${windowMinutes} minute${windowMinutes === 1 ? '' : 's'}.`);
  const tooOld = $derived(freshness > 0 && !!signedIso && (now - Date.parse(signedIso)) / 1000 > freshness);
  const staleLine = $derived(tooOld ? `${needs} You signed in ${formatRelative(signedIso, now)}.` : '');
  const verb = (decision) => (decision === 'reject' ? 'reject' : decision === 'accept-unknown' ? 'accept the unknown' : 'approve');
  const label = (decision, text) => (confirming && kept.decision === decision
    ? `Confirm: ${text}${signedInAt ? ` (signed in at ${signedInAt})` : ''}` : text);

  // Back from signing in, focus lands on the one button left to press.
  let approveBtn = $state(null);
  let rejectBtn = $state(null);
  $effect(() => { if (confirming) (kept.decision === 'reject' ? rejectBtn : approveBtn)?.focus(); });

  async function decide(decision) {
    busy = true;
    refusal = null;
    needsFresh = null;
    try {
      const out = await api.deliveryDecide(ns, name, decision, note);
      dropPending();
      decided = out.signing === 'recorder'
        ? `${decisionLabel(decision)} recorded as ${out.approval}. The recorder signs it for you into the registry, and the promoter acts once it is there.`
        : `${decisionLabel(decision)} recorded as ${out.approval}. The promoter acts on it shortly.`;
      onDecided?.(decided);
    } catch (e) {
      if (e?.code === 'FreshLoginRequired') {
        needsFresh = { decision };
      } else {
        refusal = e;
      }
    } finally {
      busy = false;
    }
  }

  // A token session with no identity provider signs in again by pasting a fresh token.
  const freshToken = $derived(s?.method === 'token' && !s.login);
  async function signInAgain() {
    const pending = { namespace: ns, name, decision: needsFresh.decision, note };
    keepPending(pending);
    if (freshToken) {
      // Signing out brings back the sign-in screen; the choice waits in this tab.
      await api.signOut().catch(() => '');
      location.reload();
      return;
    }
    // A host that signs people in itself never reloads this page, so the kept
    // choice is shown back from here once the session is fresh.
    if (hostSignsIn()) { kept = pending; closeDialog(); }
    signIn(location.hash, true);
  }

  // The dialog opens modal once rendered (a host without showModal shows it in
  // place). Closing it returns focus to the button that opened it, which was
  // disabled while the decision was sent and so lost it.
  let dialog = $state(null);
  function closeDialog() {
    if (typeof dialog?.close === 'function') dialog.close();
    else dialogClosed();
  }
  function dialogClosed() {
    const btn = needsFresh?.decision === 'reject' ? rejectBtn : approveBtn;
    needsFresh = null;
    btn?.focus();
  }
  $effect(() => {
    if (!dialog || dialog.open) return;
    if (typeof dialog.showModal === 'function') dialog.showModal();
    else dialog.setAttribute('open', '');
  });
</script>

<section class="dp" aria-labelledby="dp-title" data-testid="decision-panel">
  <h2 id="dp-title" class="t-section-title">The decision</h2>
  <p class="dp-chips t-meta">{#if breaking}<span class="badge badge-warn" data-testid="decision-breaking">Breaking</span>{/if}<span data-testid="decision-who">{!owners.length ? ownerless : unnarrowed ? (offer ? 'You can answer.' : `People who may update this workload may answer; asked of ${ownerList}`) : offer ? `You can answer${myTeam ? `: you are in ${myTeam}` : ''}.` : `Only people of ${ownerList} who may update this workload may answer`}</span>{#if ownersCut && !offer}<button type="button" class="btn btn-sm" aria-expanded={allOwners} onclick={() => { allOwners = !allOwners; }}>{allOwners ? 'Show fewer' : `Show all ${formatCount(owners.length)} teams`}</button>{/if}</p>
  {#if asked === 'accept-unknown'}
    <p class="t-body-2" data-testid="decision-unknown">A check ended Unknown: Pacto could not see enough to decide, and Unknown never passes. <a class="dp-link" href={UNKNOWN_DOCS} target="_blank" rel="noopener noreferrer">What Unknown means</a></p>
  {/if}

  {#if decided}
    <p class="dp-ok" role="status">{decided}</p>
  {:else}
    {#if offer}
      {#if kept}
        <p class="dp-kept t-body-2" role="status" data-testid="decision-kept">Your choice was <strong>{decisionLabel(kept.decision)}</strong>. Nothing is sent until you confirm it below.</p>
      {/if}
      {#if needsFresh}
        <dialog class="dp-fresh" bind:this={dialog} aria-labelledby="dp-fresh-title" aria-describedby="dp-fresh-why" data-testid="fresh-login"
          onclose={dialogClosed}>
          <h2 id="dp-fresh-title" class="t-section-title">Sign in again to decide</h2>
          <p id="dp-fresh-why">{staleLine || needs} Your choice and note are kept, and nothing is sent until you confirm it when you come back.</p>
          {#if freshToken}
            <p>Log in again at your identity provider (a cached or refreshed token keeps the old sign-in time), then paste the new token. Or decide from the command line:</p>
            <CopyableIdentifier value={cliDecision(ns, name, needsFresh.decision)} label="command" />
          {/if}
          <div class="dp-fresh-actions">
            <button type="button" class="btn btn-primary" data-testid="fresh-login-go" onclick={signInAgain}>{freshToken ? 'Sign out to paste a new token' : `Sign in again to ${verb(needsFresh.decision)}`}</button>
            <button type="button" class="btn" onclick={closeDialog}>Cancel</button>
          </div>
        </dialog>
      {/if}
      <label class="dp-field">
        <span><strong>Note</strong> (kept with the decision){#if breaking}{' · required to approve'}{/if}</span>
        <textarea class="input" rows="2" maxlength="500" bind:value={note} placeholder={breaking ? 'Why should it ship anyway?' : ''} aria-describedby={noteMissing ? 'dp-note-why' : undefined}></textarea>
      </label>
      {#if noteMissing}<p id="dp-note-why" class="visually-hidden" data-testid="decision-note-required">A breaking change needs a note to be approved.</p>{/if}
      <!-- Next to the buttons: deciding would ask this person to sign in again first. -->
      {#if staleLine}<p class="t-body-2" data-testid="decision-stale">{staleLine}</p>{/if}
    {/if}
    <!-- One card per answer: what it does, then its button. Approve is the primary one; it
         looks disabled while a required note is empty. -->
    <div class="dp-outcomes" data-testid="decision-consequences">
      <div class="dp-outcome dp-approve">
        <h3 class="t-body">If you approve</h3>
        <ul>{#each ifApprove as line (line)}<li>{line}</li>{/each}</ul>
        {#if !applied && consumers.length > 4}<button type="button" class="btn btn-sm dp-more" onclick={toAffected}>See all {formatCount(consumers.length)} affected services</button>{/if}
        {#if offer}<button type="button" class="btn btn-primary dp-btn" data-testid="decide-approve" bind:this={approveBtn} class:dp-was={kept?.decision === asked} disabled={busy || noteMissing} onclick={() => decide(asked)} title={asked === 'approve' ? `Approve shipping ${toV} to ${env}` : undefined}>{label(asked, approveLabel)}</button>{/if}
      </div>
      <div class="dp-outcome dp-reject">
        <h3 class="t-body">If you reject</h3>
        <ul>{#each ifReject as line (line)}<li>{line}</li>{/each}</ul>
        {#if offer}<button type="button" class="btn dp-btn" data-testid="decide-reject" bind:this={rejectBtn} class:dp-was={kept?.decision === 'reject'} disabled={busy} onclick={() => decide('reject')} title={rollsBack ? `Reject and roll back ${env} to ${backV}` : undefined}>{label('reject', rejectLabel)}</button>{/if}
      </div>
    </div>
    {#if moveFirst}
      <div class="dp-safer" data-testid="decision-safer"><MoveFirstList order={st.plan.order} /></div>
    {/if}

    {#if offer}
      <!-- the buttons are in the cards above -->
    {:else if s?.mode === 'local'}
      <p class="t-body-2" data-testid="decide-cli">On your own machine, any program could act as you in the dashboard, so you decide from the command line:</p>
      <CopyableIdentifier value={cliDecision(ns, name, asked, st.intentRecord?.ref)} label="command" />
    {:else if s && !s.signedIn}
      <p class="t-body-2">Sign in to decide.</p>
      <SignInLink hash={location.hash} class="btn btn-sm btn-primary">Sign in</SignInLink>
    {:else if samePerson(me().user, op.spec?.actor?.user) || samePerson(me().user, op.spec?.onBehalfOf?.user)}
      <p class="t-body-2" data-testid="decide-own">You asked for this change, so someone else must decide it.</p>
    {:else if caller?.class === 'agent' || caller?.class === 'controller'}
      <p class="t-body-2" data-testid="decide-agent">The cluster says you are an agent (<bdi>{me().user}</bdi>), and agents cannot approve.</p>
    {:else if freshness > 0 && cannotApproveHere(caller)}
      <p class="t-body-2" data-testid="decide-no-auth-time">{s?.method === 'token'
        ? 'This token does not say when you signed in, and this Stage asks for a recent sign-in. Sign in with your identity provider to approve.'
        : 'Approving is not possible on this cluster: it does not report when you signed in.'} <a class="dp-link" href={APPROVING_DOCS} target="_blank" rel="noopener noreferrer">Where approving works</a></p>
    {:else if decisionRefusal === 'CannotUpdateWorkload'}
      <p class="t-body-2" data-testid="decide-no-access">You cannot update this workload, and only people who may update it decide its changes.</p>
    {:else if decisionRefusal === 'ApproverUnverified'}
      <p class="t-body-2" data-testid="decide-unverified">Pacto could not check whether you may update this workload, so it offers no decision. Try again in a moment.</p>
    {:else if decisionRefusal === 'NoApprovalRights'}
      <p class="t-body-2" data-testid="decide-no-rights">Your Kubernetes RBAC does not let you create Approvals in <bdi>{ns}</bdi>. The edit role there, or the delivery approver role, lets you.</p>
    {:else if decisionRefusal === 'PolicyUnknown'}
      <p class="t-body-2" data-testid="decide-policy">Pacto could not read the delivery identity policy, so it cannot tell whether you may answer.</p>
    {:else if unnarrowed && (owners.length || !caller.requireOwnerTeam)}
      <p class="t-body-2" data-testid="decide-rule">Only people who may update this workload may answer this question.</p>
    {:else if !owners.length}
      <p class="t-body-2" data-testid="decide-no-owner">No team is declared for this service, so only break-glass can override, from the command line. Declare service.owner so a team can answer.</p>
    {:else}
      <p class="t-body-2" data-testid="decide-not-team">Only {ownerList} may answer this question{#if me().groups?.length}; you are in {andList(me().groups, 3)}{/if}.</p>
    {/if}
  {/if}
  {#if refusal}<RefusalNotice error={refusal} />{/if}
</section>

<style>
  .dp { border: 1px solid var(--c-warn-border); background: var(--c-warn-bg); border-radius: var(--radius-md); padding: var(--sp-4); display: flex; flex-direction: column; gap: var(--sp-2); }
  .dp p { margin: 0; }
  .dp-field { display: flex; flex-direction: column; gap: 2px; font-size: var(--text-sm); color: var(--c-text-2); }
  .dp-more { align-self: flex-start; }
  .dp-chips { display: flex; flex-wrap: wrap; align-items: center; gap: var(--sp-2); color: var(--c-text-2); }
  /* Each button closes its card, level with the other card's button. */
  .dp-btn { margin-top: auto; width: 100%; }
  .dp-outcome ul { margin-bottom: var(--sp-2); }
  .dp-outcomes { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(260px, 100%), 1fr)); gap: var(--sp-2); }
  .dp-outcome { background: var(--c-surface); border: 1px solid var(--c-border); border-radius: var(--radius-sm); padding: var(--sp-3); display: flex; flex-direction: column; gap: var(--sp-1); }
  .dp-outcome h3 { margin: 0; font-weight: 600; }
  .dp-outcome ul { margin-top: 0; padding-left: var(--sp-5); }
  .dp-was { outline: 2px solid var(--c-accent); outline-offset: 2px; }
  /* The warn tint is translucent: on the panel's own warn tint it loses contrast, so lay it on the surface. */
  .dp .badge-warn { background: linear-gradient(var(--c-warn-bg), var(--c-warn-bg)), var(--c-surface); }
  .dp-link { color: var(--c-accent); text-decoration: underline; }
  .dp-ok { color: var(--c-ok); font-weight: 600; }
  .dp-fresh { border: 1px solid var(--c-accent-border); background: var(--c-surface); color: var(--c-text); border-radius: var(--radius-md); padding: var(--sp-4); max-width: min(28rem, calc(100vw - 2 * var(--sp-4))); }
  .dp-fresh[open] { display: flex; flex-direction: column; gap: var(--sp-2); }
  .dp-fresh::backdrop { background: rgb(0 0 0 / 0.4); }
  .dp-fresh h2 { margin: 0; }
  .dp-fresh-actions { display: flex; flex-wrap: wrap; gap: var(--sp-2); }
</style>
