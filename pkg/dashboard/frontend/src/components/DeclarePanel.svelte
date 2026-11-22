<script>
  import { api } from '../lib/api.ts';
  import { rangeRows, draftLine, draftMark, signUrl, moved, completenessLine } from '../lib/declare.ts';
  import { formatCount } from '../lib/format.ts';
  import CopyableIdentifier from './CopyableIdentifier.svelte';
  import FieldProvenanceRow from './FieldProvenanceRow.svelte';
  import Fold from './Fold.svelte';
  import ProvenanceMark from './ProvenanceMark.svelte';
  import RefusalNotice from './RefusalNotice.svelte';

  // Declare an inferred service: what pacto declare would publish, read with the
  // person's own cluster credentials, and what they can do with it here. Essentials
  // first (version, deploy, signer, one row per range with an accept checkbox), then
  // the two files and the drafts, collapsed. The plan is asked for only once the
  // panel opens (`open`), never on page view. Signing goes through a fresh login at
  // the identity provider; back from it (`signed` is the intent it was started for)
  // the panel shows the whole plan again and publishes only on a click. Without a
  // signing login it saves a draft, and the exact command is always there.
  let { serviceKey = '', open = false, signed = '' } = $props();

  // A draft being accepted rides across the signing login here, keyed by the intent
  // digest that covers its digest, so a changed draft never matches.
  const draftKey = (intent) => `pacto-declare-draft:${intent}`;

  let answer = $state(null);
  let error = $state(null);
  let loading = $state(false);
  let accept = $state([]);
  let draft = $state(null);
  let busy = $state(false);
  let result = $state(null);
  let refusal = $state(null);
  let movedText = $state('');
  let asked = false;

  const p = $derived(answer?.plan ?? null);
  const signing = $derived(answer?.signing ?? { mode: 'none' });
  const write = $derived(answer?.write ?? { available: false });
  const rows = $derived(rangeRows(p, accept));
  const drafts = $derived(answer?.drafts ?? []);
  const writes = $derived(!!p && !p.unchanged && write.available);
  const fresh = $derived(signing.mode === 'fresh-login');
  // Back from a signing login only while the server still holds that login, and
  // until a refusal (a used or expired login) sends the person to sign again.
  const backSigned = $derived(fresh && !!signed && !!answer?.signedIn && p?.intentDigest === signed && !result && !refusal);
  const signHref = $derived(fresh && writes && answer?.signTicket ? signUrl(answer.signTicket, serviceKey, p.intentDigest) : '');
  // A published version ends the panel's actions: nothing is left to sign or save.
  const done = $derived(!!result && !result.draft);
  const signs = $derived(!done && ((backSigned && writes) || !!signHref));
  const noSign = $derived(signing.reason || write.reason || (p?.unchanged ? `nothing changed since ${p.unchanged}` : ''));
  const completeness = $derived(completenessLine(p));

  function adopt(a) {
    answer = a;
    accept = (a?.plan?.ranges ?? []).filter((r) => r.accepted).map((r) => r.dependency);
  }

  async function load(body = {}) {
    loading = true;
    error = null;
    try {
      adopt(await api.fleetDeclare({ serviceKey, ...body }));
    } catch (e) {
      answer = null;
      error = e;
    } finally {
      loading = false;
    }
  }

  async function openPlan() {
    let from = null;
    if (signed) {
      try { from = JSON.parse(sessionStorage.getItem(draftKey(signed)) || 'null'); } catch { from = null; }
    }
    draft = from;
    await load({ intentDigest: signed || undefined, fromDraft: from || undefined });
    if (!signed || !p || !fresh) return;
    if (!answer.signedIn) movedText = 'Your signing login expired or was used: check your ranges and sign again';
    else if (p.intentDigest !== signed) movedText = 'The plan changed while you signed in, so sign again';
  }

  $effect(() => {
    if (open && !asked) {
      asked = true;
      openPlan();
    }
  });

  function reset() {
    result = refusal = null;
    movedText = '';
  }

  // A control that is busy stays focusable (aria-disabled) and does nothing, so a
  // keyboard reader keeps their place.
  function toggle(e, dep) {
    if (loading || busy) {
      e.preventDefault();
      return;
    }
    reset();
    accept = accept.includes(dep) ? accept.filter((a) => a !== dep) : [...accept, dep];
    load({ accept, fromDraft: draft || undefined });
  }

  // Accepting a draft first loads that draft's own plan, so the person reads what
  // nothing backs before anything is signed. Its requested ranges come back
  // unknown: the person ticks the ones they accept.
  function acceptDraft(d) {
    if (loading) return;
    reset();
    draft = { tag: d.tag, digest: d.digest };
    load({ fromDraft: draft });
  }

  function leaveDraft() {
    reset();
    draft = null;
    load({ accept: [] });
  }

  function startSign() {
    try { if (draft) sessionStorage.setItem(draftKey(p.intentDigest), JSON.stringify(draft)); } catch { /* the draft is opened again by hand */ }
  }

  async function send(mode) {
    if (busy || loading) return;
    reset();
    busy = true;
    const before = p;
    try {
      const out = await api.fleetDeclareAccept({ serviceKey, accept, intentDigest: before.intentDigest, mode, fromDraft: draft || undefined });
      if (out.refusal?.code === 'PlanStale' && out.plan) {
        adopt(out.plan);
        movedText = `The plan changed since you read it: ${moved(before, out.plan.plan)} moved. Read it again`;
      } else if (out.refusal) {
        refusal = out.refusal;
      } else {
        result = out;
        try { if (signed) sessionStorage.removeItem(draftKey(signed)); } catch { /* nothing kept */ }
        if (out.draft) {
          draft = null;
          await load({ accept });
        }
      }
    } catch (e) {
      refusal = e;
    } finally {
      busy = false;
    }
  }
</script>

<div class="dp" data-testid="declare">
  {#if loading && !answer}
    <p class="t-meta dp-p">Reading the cluster as you…</p>
  {:else if error}
    <RefusalNotice {error} />
  {:else if answer}
    {#if answer.refusal}
      <RefusalNotice error={answer.refusal} />
    {/if}
    {#if p}
      {#if draft}
        <p class="t-body-2 dp-p" data-testid="declare-draft-plan">Accepting <bdi>{draft.tag}</bdi>. <button type="button" class="dp-link" onclick={leaveDraft}>Back to a new declaration</button></p>
      {/if}
      <dl class="dp-facts">
        <div><dt>Version</dt><dd><bdi>{p.version}</bdi>{#if p.unchanged} (nothing changed since {p.unchanged}, so nothing is written){/if}</dd></div>
        <div><dt>Deploy</dt><dd><bdi title={p.deploy}>{p.deploy}</bdi></dd></div>
        <div><dt>Signer</dt><dd data-testid="declare-signer">{#if signs}You, <bdi title={signing.identity}>{signing.identity}</bdi>, after you sign in again{:else if done}You, <bdi title={signing.identity}>{signing.identity}</bdi>{:else}You do not sign here{#if noSign}: <bdi>{noSign}</bdi>{/if}{/if}</dd></div>
        {#if p.owner}<div><dt>Owner</dt><dd><bdi title={p.owner}>{p.owner}</bdi></dd></div>{/if}
      </dl>
      {#if rows.length}
        <dl class="dp-rows" data-testid="declare-ranges">
          {#each rows as r (r.dependency)}
            {#snippet box()}<input type="checkbox" class="dp-box" checked={r.checked} disabled={!r.acceptable} aria-disabled={loading || busy ? 'true' : undefined} aria-label={`Accept ${r.label}`} onclick={(e) => toggle(e, r.dependency)} />{/snippet}
            <FieldProvenanceRow label={r.label} value={r.value} mark={r.mark} provenance={r.checked ? 'accepted' : 'inferred'} inferred={!r.checked} control={box} />
          {/each}
        </dl>
      {/if}
      {#if completeness}<p class="t-meta dp-p" data-testid="declare-completeness">{completeness}</p>{/if}
      {#each p.notes ?? [] as n, i (i)}<p class="t-meta dp-p">{n}</p>{/each}
      {#if p.policyNote}<p class="t-meta dp-p">{p.policyNote}</p>{/if}
      {#if draft && (p.unbacked?.length || p.unbackedUnchecked)}
        <div class="dp-unbacked" data-testid="declare-unbacked">
          {#if p.unbacked?.length}
            <p class="dp-p">Nothing backs {p.unbacked.length === 1 ? 'this change' : 'these changes'} of the draft: neither the published version, the cluster read nor an accepted range.</p>
            <ul class="dp-list">{#each p.unbacked as u, i (i)}<li><bdi>{u}</bdi></li>{/each}</ul>
          {/if}
          {#if p.unbackedUnchecked}<p class="dp-p">{p.unbackedUnchecked}</p>{/if}
        </div>
      {/if}
      {#if movedText}<p class="dp-p dp-moved" role="status" data-testid="declare-moved">{movedText}</p>{/if}

      {#if !done}
        <div class="dp-actions">
          {#if backSigned && writes}
            <p class="dp-p" data-testid="declare-signed-return">Signed in again as <bdi>{signing.identity}</bdi>.</p>
            <button type="button" class="btn btn-sm btn-primary" aria-disabled={busy ? 'true' : undefined} onclick={() => send('sign')}>Publish {p.version}</button>
          {:else if signHref}
            <a class="btn btn-sm btn-primary" href={signHref} onclick={startSign} data-testid="declare-sign"><span class="dp-nowrap">Sign as</span> <bdi class="dp-id">{signing.identity}</bdi></a>
          {/if}
          {#if writes && !draft}
            <button type="button" class="btn btn-sm" aria-disabled={busy || loading ? 'true' : undefined} onclick={() => send('draft')} data-testid="declare-save-draft">Save draft</button>
          {:else if !write.available}
            <p class="t-meta dp-p" data-testid="declare-no-write">{write.reason}</p>
          {/if}
        </div>
      {/if}
      {#if result}
        <p class="dp-p" role="status" data-testid="declare-result">
          {#if result.draft}Saved <bdi>{result.tag}</bdi>. A person who may update the workload accepts it from the drafts below or with the command.{:else}Published <bdi>{result.tag}</bdi>{#if result.signed}, signed by you as <bdi>{signing.identity}</bdi>{/if}.{/if}
        </p>
        {#if result.draft && result.command}<div class="dp-command" data-testid="declare-accept-command"><span class="t-meta">Accept it with</span> <CopyableIdentifier value={result.command} label="command" /></div>{/if}
      {/if}
      {#if refusal}
        <RefusalNotice error={refusal} />
        {#if refusal.command}<CopyableIdentifier value={refusal.command} label="command" />{/if}
      {/if}
    {/if}
    {#if answer.command}
      <div class="dp-command" data-testid="declare-command"><span class="t-meta">Or run</span> <CopyableIdentifier value={answer.command} label="command" /></div>
    {/if}
    {#if answer.prCli}
      <div class="dp-command" data-testid="declare-pr-cli"><span class="t-meta">Open a pull request with your own Git token from your machine:</span> <CopyableIdentifier value={answer.prCli} label="pull request command" /></div>
    {/if}
    {#if p}
      <Fold title="pacto.yaml" level={3} meta={p.contract?.truncated ? 'truncated' : ''} testid="declare-contract">
        <!-- svelte-ignore a11y_no_noninteractive_tabindex (a long line scrolls inside the box; focus lets a keyboard reader scroll it) -->
        <pre class="dp-yaml" tabindex="0" aria-label="pacto.yaml"><code>{p.contract?.text ?? ''}</code></pre>
        {#if p.contract?.truncated}<p class="t-meta dp-p">Showing the first 64 KiB.</p>{/if}
      </Fold>
      <Fold title="pacto.inferred.yaml" level={3} meta={p.record?.truncated ? 'truncated' : ''} testid="declare-record">
        <!-- svelte-ignore a11y_no_noninteractive_tabindex (a long line scrolls inside the box; focus lets a keyboard reader scroll it) -->
        <pre class="dp-yaml" tabindex="0" aria-label="pacto.inferred.yaml"><code>{p.record?.text ?? ''}</code></pre>
        {#if p.record?.truncated}<p class="t-meta dp-p">Showing the first 64 KiB.</p>{/if}
      </Fold>
    {/if}
    {#if drafts.length}
      <Fold title="Drafts" level={3} meta={formatCount(answer.draftsTotal || drafts.length)} testid="declare-drafts">
        <ul class="dp-list dp-drafts">
          {#each drafts as d (d.tag)}
            <li data-testid="declare-draft" data-state={d.state}>
              {#if d.state !== 'unreadable'}<ProvenanceMark start label={`Who proposed ${d.tag}`} text={draftMark(d)} />{/if}
              <bdi class="dp-draft">{draftLine(d)}</bdi>
              {#if d.state !== 'unreadable' && d.digest && draft?.tag !== d.tag}
                <button type="button" class="btn btn-sm" aria-disabled={loading ? 'true' : undefined} onclick={() => acceptDraft(d)} aria-label={`Accept ${d.tag}`}>Accept</button>
              {/if}
            </li>
          {/each}
        </ul>
        {#if (answer.draftsTotal ?? 0) > drafts.length}<p class="t-meta dp-p">{formatCount(answer.draftsTotal - drafts.length)} more</p>{/if}
      </Fold>
    {/if}
  {/if}
</div>

<style>
  .dp { container-type: inline-size; display: flex; flex-direction: column; gap: var(--sp-2); min-inline-size: 0; }
  .dp-p { margin: 0; overflow-wrap: anywhere; }
  .dp-facts, .dp-rows { margin: 0; display: flex; flex-direction: column; }
  .dp-facts > div { display: grid; grid-template-columns: minmax(0, 1fr); gap: 2px var(--sp-3); padding: var(--sp-1) 0; min-inline-size: 0; }
  @container (min-width: 28rem) {
    .dp-facts > div { grid-template-columns: minmax(14ch, 1fr) minmax(16ch, 2fr); }
  }
  .dp-facts dt { color: var(--c-text-2); }
  .dp-facts dd { margin: 0; overflow-wrap: anywhere; min-inline-size: 0; }
  .dp-list { margin: 0; padding: 0; list-style: none; display: flex; flex-direction: column; gap: var(--sp-1); }
  .dp-drafts li { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; min-inline-size: 0; }
  .dp-draft { overflow-wrap: anywhere; min-inline-size: 0; flex: 1 1 20ch; }
  .dp-unbacked { border-inline-start: 3px solid var(--c-warn-border); padding-inline-start: var(--sp-2); display: flex; flex-direction: column; gap: var(--sp-1); }
  .dp-moved { color: var(--c-text); }
  .dp-actions { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .dp-actions :global(.btn) { overflow-wrap: anywhere; max-inline-size: 100%; }
  /* "Sign as" stays one line; the identity beside it wraps. */
  .dp-nowrap { white-space: nowrap; flex: none; }
  .dp-id { min-inline-size: 0; }
  .dp-link { border: 0; background: none; padding: 0; color: var(--c-accent); font: inherit; text-decoration: underline; cursor: pointer; }
  .dp-command { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; min-inline-size: 0; }
  /* One long YAML line scrolls inside its box instead of widening the page. */
  .dp-yaml { margin: 0; overflow: auto; max-block-size: 24rem; max-inline-size: 100%; padding: var(--sp-2); border: 1px solid var(--c-border); border-radius: var(--radius-sm); font-family: var(--font-mono); font-size: var(--text-xs); }
  @media (pointer: coarse) {
    .dp-link, .dp :global(.btn) { min-block-size: 44px; }
    .dp-box { inline-size: 24px; block-size: 24px; }
    .dp-rows :global(.fpf-control) { min-block-size: 44px; }
  }
</style>
