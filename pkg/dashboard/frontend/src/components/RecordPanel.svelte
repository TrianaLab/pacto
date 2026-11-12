<script>
  import Person from './Person.svelte';
  import { onMount } from 'svelte';
  import { api } from '../lib/api.ts';
  import { deliveryChangeUrl } from '../lib/router.ts';
  import { typeLabel, decisionLabel, authTimeText, versionLabel, sentence, timeLabel, actorBadge, shortDigest, causeText } from '../lib/delivery.ts';
  import VerdictList from './VerdictList.svelte';
  import RefusalNotice from './RefusalNotice.svelte';

  // One record of a Stage's stream, read by id: the change it records, who
  // asked and for whom, who approved, every verdict with its proof, and
  // whether its signature was checked. Only a record in the stream answers.
  let { namespace, stage, id, miraURL = '', onClose } = $props();

  let rec = $state(null);
  let error = $state(null);
  let headingEl = $state(null);
  const e = $derived(rec?.entry);
  const v = (d) => versionLabel(d, rec?.versions);

  onMount(async () => {
    headingEl?.focus();
    try { rec = await api.deliveryRecord(namespace, stage, id); } catch (err) { error = err; }
  });
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<section class="hp" aria-labelledby="rp-{namespace}-{stage}-{id}" data-testid="record-panel"
  onkeydown={(ev) => { if (ev.key === 'Escape') { ev.preventDefault(); onClose?.(); } }}>
  <h3 id="rp-{namespace}-{stage}-{id}" class="t-subsection-title" tabindex="-1" bind:this={headingEl} title={id}>Record {shortDigest(id)}</h3>
  {#if error}
    <RefusalNotice {error} />
  {:else if !rec}
    <p class="t-body-2" role="status">Reading the record…</p>
  {:else}
    <p class="t-body" data-testid="record-change">
      {#if e.operation}<a href={deliveryChangeUrl(namespace, e.operation)}>{typeLabel(e.type)}</a>{:else}{typeLabel(e.type)}{/if}
      by <strong><Person user={e.actor} people={rec.people} /></strong>{#if actorBadge(e.actorClass)}{@const ab = actorBadge(e.actorClass)}{' '}<span class="badge {ab.tone}">{ab.label}</span>{/if}{#if rec.onBehalfOf}, for <strong><Person user={rec.onBehalfOf} people={rec.people} /></strong>{/if}{#if e.from}, from {v(e.from)}{/if}{#if e.to}{' '}to {v(e.to)}{/if}{#if e.finished}, finished <time datetime={e.finished}>{timeLabel(e.finished)}</time>{/if}.
      {#if rec.intentReason}Why: “{rec.intentReason}”.{/if}
    </p>
    {#if e.applied || e.cause || e.correlationID}
      <p class="t-body-2 rp-facts">
        {#if e.applied}<span>Applied <time datetime={e.applied}>{timeLabel(e.applied)}</time></span>{/if}
        {#if causeText(e.cause)}<span>Cause <bdi>{causeText(e.cause)}</bdi></span>{/if}
        {#if e.correlationID}<span>Correlation <bdi class="rp-mono">{e.correlationID}</bdi></span>{/if}
      </p>
    {/if}
    {#if e.approvals?.length}
      <ul class="hp-list t-body-2">
        {#each e.approvals as a, i (i)}
          <li><strong>{decisionLabel(a.decision)}</strong> by <Person user={a.by} people={rec.people} />{#if a.authTime}, signed in at {authTimeText(a.authTime)}{/if}{#if a.note}: “{a.note}”{/if}</li>
        {/each}
      </ul>
    {:else}
      <p class="t-body-2">No person approved it.</p>
    {/if}
    <VerdictList verdicts={rec.verdicts ?? []} {miraURL} testid="record-verdicts" />
    <p class="t-body-2" data-testid="record-note">{sentence(rec.note)}</p>
  {/if}
  <div><button type="button" class="btn btn-sm" onclick={() => onClose?.()}>Close</button></div>
</section>

<style>
  .hp { border: 1px solid var(--c-border); border-radius: var(--radius-md); padding: var(--sp-4); display: flex; flex-direction: column; gap: var(--sp-2); background: var(--c-surface); overflow-wrap: anywhere; }
  .hp h3:focus { outline: none; }
  .hp p { margin: 0; }
  .hp a { color: var(--c-accent); text-decoration: underline; }
  .hp-list { margin: 0; padding-left: var(--sp-5); }
  .rp-facts { display: flex; flex-wrap: wrap; gap: var(--sp-1) var(--sp-3); color: var(--c-text-2); }
  .rp-mono { font-family: var(--font-mono); }
</style>
