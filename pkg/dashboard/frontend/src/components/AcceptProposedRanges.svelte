<script>
  import { api } from '../lib/api.ts';
  import { acceptSignUrl } from '../lib/declare.ts';
  import { session } from '../lib/session.svelte.ts';
  import CopyableIdentifier from './CopyableIdentifier.svelte';
  import ProvenanceMark from './ProvenanceMark.svelte';
  import RefusalNotice from './RefusalNotice.svelte';

  // One action row inside the Inferred block: "Accept all proposed ranges for <service>".
  // It asks the server for the plan pacto accept would sign and shows it with the exact
  // command a person runs to sign it. Acceptances already attached that do not count
  // (waiting for the owner, or signed by CI or an agent) are one muted line each, with a
  // mark. Where the dashboard can write (`execute`), the plan gains Sign (a fresh login
  // at the identity provider, then one click back here: `signed` is the intent it was
  // started for) and Save draft, and the acceptance drafts waiting are listed folded;
  // accepting one first loads that draft's own plan. The command is always there.
  // The plan reads acceptances with the dashboard's credentials, so only a local
  // viewer or a signed-in person is offered it; anyone else, and a session the
  // server refuses (a sign-in link), gets one line with the CLI dry run.
  let { serviceKey = '', service = '', signed = '' } = $props();
  // An acceptance draft being accepted rides across the signing login here.
  const draftKey = (intent) => `pacto-accept-draft:${intent}`;
  const planId = $props.id();
  const DRY_RUN = 'pacto accept <bundle repository> --proposed --dry-run';

  let answer = $state(null);
  let error = $state('');
  let loading = $state(false);
  // asked mounts the disclosure; open is its state, so a press reopens it and
  // the button's aria-expanded follows a native collapse.
  let asked = $state(false);
  let open = $state(false);
  let refused = $state(false);
  const canPlan = $derived(session.data?.mode === 'local' || !!session.data?.signedIn);

  let draft = $state('');
  let busy = $state(false);
  let result = $state(null);
  let failure = $state(null);

  async function plan(fromDraft = '') {
    asked = open = true;
    if (loading) return;
    loading = true;
    error = '';
    draft = fromDraft;
    try {
      answer = await api.fleetAccept(serviceKey, fromDraft || undefined);
    } catch (e) {
      answer = null;
      refused = e?.status === 403;
      error = e?.status === 404
        ? 'This dashboard does not plan acceptances: run pacto accept <bundle repository> --proposed --dry-run.'
        : (e?.message || 'Pacto could not plan the acceptance.');
    } finally {
      loading = false;
    }
  }

  // Back from a signing login: plan again, the draft it was started for included.
  let resumed = false;
  $effect(() => {
    if (signed && canPlan && !resumed) {
      resumed = true;
      let from = '';
      try { from = sessionStorage.getItem(draftKey(signed)) || ''; } catch { from = ''; }
      plan(from);
    }
  });

  const offer = $derived(answer?.plan ? answer.execute ?? null : null);
  const fresh = $derived(offer?.signing?.mode === 'fresh-login' && !!offer?.write?.available);
  const backSigned = $derived(fresh && !!signed && answer?.plan?.intentDigest === signed && !result);
  const signHref = $derived(fresh && offer?.signTicket ? acceptSignUrl(offer.signTicket, serviceKey, answer.plan.intentDigest) : '');
  const drafts = $derived(answer?.plan?.drafts ?? []);

  function startSign() {
    try { if (draft) sessionStorage.setItem(draftKey(answer.plan.intentDigest), draft); } catch { /* the draft is planned again by hand */ }
  }

  async function send(mode) {
    busy = true;
    result = failure = null;
    try {
      const out = await api.fleetAcceptExecute({ serviceKey, intentDigest: answer.plan.intentDigest, mode, fromDraft: draft || undefined });
      if (out.refusal) {
        failure = out.refusal;
      } else {
        try { sessionStorage.removeItem(draftKey(answer.plan.intentDigest)); } catch { /* nothing kept */ }
        result = out;
      }
    } catch (e) {
      failure = e;
    } finally {
      busy = false;
    }
  }

  function pick(tag) {
    result = failure = null;
    plan(tag);
  }

  const draftWords = (d) => (d.ranges ?? []).map((r) => `${r.field} ${r.value}`).join(', ') || (d.break ? `break ${d.break.provider}@${d.break.version}` : '');

  const waiting = $derived((answer?.plan?.existing ?? []).filter((a) => a.state !== 'counted'));
  const stateWords = { routed: 'waiting for the owner', proposal: 'a proposal, signed by a non-person', expired: 'expired', ignored: 'ignored' };
  const markText = (a) => `${stateWords[a.state] ?? a.state}. Signed by ${a.by || 'nobody named'}${a.issuer ? ` (${a.issuer})` : ''}.`
    + (a.record ? ` Record: ${a.record}.` : '') + (a.reason ? ` ${a.reason}` : '');
</script>

<div class="apr t-meta" data-testid="accept-proposed">
  {#if !canPlan || refused}
    <p class="apr-note" data-testid="accept-sign-in">Sign in to plan accepting the proposed ranges for <bdi>{service}</bdi>, or plan it with the CLI:</p>
    <CopyableIdentifier value={DRY_RUN} label="command" />
  {:else}
  <button type="button" class="apr-action" onclick={() => plan()} aria-expanded={open} aria-controls={planId}>
    Accept all proposed ranges for <bdi>{service}</bdi>
  </button>
  {#if asked}
    <details class="apr-plan disclosure" bind:open id={planId} data-testid="accept-plan">
      <summary><span class="disclosure-caret" data-motion aria-hidden="true"></span>{answer?.refusal ? 'Nothing to accept' : 'Plan'}</summary>
      {#if loading}
        <p class="apr-note">Reading the newest published version…</p>
      {:else if error}
        <p class="apr-error" role="alert">{error}</p>
      {:else if answer?.refusal}
        <p class="apr-text" data-testid="accept-refusal">{answer.refusal.message}</p>
        {#if answer.refusal.command}<CopyableIdentifier value={answer.refusal.command} label="command" />{/if}
      {:else if answer?.plan}
        <p class="apr-text">Signs one record for <bdi title={answer.plan.subject}>{answer.plan.subject}</bdi>. {answer.plan.whoMayAccept}</p>
        <ul class="apr-list">
          {#each answer.plan.ranges ?? [] as r (r.field)}
            <li><code><bdi>{r.field}</bdi></code> <bdi>{r.value}</bdi>{#if r.source}{" "}<span class="apr-src">from <bdi title={r.source}>{r.source}</bdi></span>{/if}</li>
          {/each}
        </ul>
        {#each waiting as a (a.record + a.state)}
          <p class="apr-waiting" data-testid="accept-waiting">
            <ProvenanceMark start label={`About ${a.state} acceptance`} text={markText(a)} />
            <span>{a.state === 'routed' ? 'Waiting for the owner' : 'Not counted'}: <bdi title={a.by}>{a.by}</bdi></span>
          </p>
        {/each}
        {#if answer.plan.draft}
          <p class="apr-text" data-testid="accept-draft-plan">Accepting <bdi>{answer.plan.draft}</bdi>{#if answer.plan.onBehalfOf}, written for <bdi title={answer.plan.onBehalfOf}>{answer.plan.onBehalfOf}</bdi>{/if}. <button type="button" class="apr-link" onclick={() => pick('')}>Back to the proposed ranges</button></p>
        {/if}
        {#each answer.plan.notes ?? [] as n, i (i)}<p class="apr-note">{n}</p>{/each}
        {#if offer}
          <div class="apr-actions" data-testid="accept-execute">
            {#if backSigned}
              <p class="apr-text" data-testid="accept-signed-return">Signed in again as <bdi title={offer.signing.identity}>{offer.signing.identity}</bdi>.</p>
              <button type="button" class="btn btn-sm btn-primary" disabled={busy} onclick={() => send('sign')}>Attach the acceptance</button>
            {:else if signHref}
              <a class="btn btn-sm btn-primary" href={signHref} onclick={startSign} data-testid="accept-sign">Sign as <bdi>{offer.signing.identity}</bdi></a>
            {/if}
            {#if offer.write?.available && !answer.plan.draft}
              <button type="button" class="btn btn-sm" disabled={busy} onclick={() => send('draft')} data-testid="accept-save-draft">Save draft</button>
            {:else if !offer.write?.available}
              <p class="apr-note" data-testid="accept-no-write">{offer.write?.reason}</p>
            {/if}
            {#if !fresh && offer.write?.available}<p class="apr-note" data-testid="accept-no-sign">Nobody signs here: <bdi>{offer.signing?.reason}</bdi></p>{/if}
          </div>
          {#if result}
            <p class="apr-text" role="status" data-testid="accept-result">
              {#if result.draft}Saved <bdi title={result.draft}>{result.draft}</bdi>. The owner accepts it from the drafts below or with the command.{:else}Attached <bdi title={result.record}>{result.record}</bdi>, signed by you{#if result.state}: {result.state}{/if}.{/if}
            </p>
            {#if result.command}<CopyableIdentifier value={result.command} label="command" />{/if}
          {/if}
          {#if failure}
            <RefusalNotice error={failure} />
            {#if failure.command}<CopyableIdentifier value={failure.command} label="command" />{/if}
          {/if}
        {/if}
        <p class="apr-text">Run this to sign it with your Sigstore login:</p>
        <CopyableIdentifier value={answer.command} label="command" />
        {#if drafts.length}
          <details class="disclosure" data-testid="accept-drafts">
            <summary><span class="disclosure-caret" data-motion aria-hidden="true"></span>Drafts ({answer.plan.draftsTotal ?? drafts.length})</summary>
            <ul class="apr-list">
              {#each drafts as d (d.tag)}
                <li class="apr-draft" class:apr-muted={d.state !== 'draft'}>
                  {#if d.state === 'draft'}
                    <ProvenanceMark start label={`About ${d.tag}`} text={`${d.signed ? 'Signed' : 'Unsigned'}${d.asker ? ` by ${d.asker}` : ''}. Whom it was written for is asserted by its writer; a person signs it to make it count.`} />
                    <span><bdi>{d.tag}</bdi>{#if d.onBehalfOf}{' '}for <bdi title={d.onBehalfOf}>{d.onBehalfOf}</bdi>{/if}: <bdi title={draftWords(d)}>{draftWords(d)}</bdi></span>
                    {#if fresh}<button type="button" class="apr-link" onclick={() => pick(d.tag)}>Accept</button>{/if}
                  {:else}
                    <span><bdi>{d.tag}</bdi> cannot be read: <bdi>{d.reason}</bdi></span>
                  {/if}
                </li>
              {/each}
            </ul>
          </details>
        {/if}
      {/if}
    </details>
  {/if}
  {/if}
</div>

<style>
  .apr { display: flex; flex-direction: column; gap: var(--sp-2); margin-top: var(--sp-2); min-inline-size: 0; container-type: inline-size; }
  .apr-action { align-self: flex-start; max-inline-size: 100%; padding: 0; border: none; background: none; color: var(--c-accent); font: inherit; cursor: pointer; text-decoration: underline; overflow-wrap: anywhere; text-align: start; min-block-size: var(--touch-min); }
  @media (pointer: coarse) { .apr-action { min-block-size: 44px; } }
  .apr-plan > :not(summary) { margin-top: var(--sp-1); }
  .apr-text, .apr-note, .apr-error, .apr-waiting { margin: 0; overflow-wrap: anywhere; }
  .apr-error { color: var(--c-warn); }
  .apr-note, .apr-waiting, .apr-src { color: var(--c-text-3); }
  .apr-waiting { display: flex; align-items: center; gap: var(--sp-2); min-block-size: var(--touch-min); }
  .apr-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-1); }
  .apr-list li { overflow-wrap: anywhere; min-inline-size: 0; }
  .apr-list code { font-size: var(--text-sm); }
  .apr-actions { display: flex; flex-wrap: wrap; align-items: center; gap: var(--sp-2); }
  .apr-link { padding: 0; border: none; background: none; color: var(--c-accent); font: inherit; cursor: pointer; text-decoration: underline; min-block-size: var(--touch-min); }
  @media (pointer: coarse) { .apr-link, .apr-actions .btn { min-block-size: 44px; } }
  .apr-draft { display: flex; flex-wrap: wrap; align-items: center; gap: var(--sp-2); }
  .apr-muted { color: var(--c-text-3); }
</style>
