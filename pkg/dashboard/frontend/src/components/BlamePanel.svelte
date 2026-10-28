<script>
  import Person from './Person.svelte';
  import { onMount } from 'svelte';
  import { api } from '../lib/api.ts';
  import { deliveryChangeUrl } from '../lib/router.ts';
  import { typeLabel, decisionLabel, authTimeText, versionLabel, sentence, timeLabel, actorBadge } from '../lib/delivery.ts';
  import VerdictList from './VerdictList.svelte';
  import RefusalNotice from './RefusalNotice.svelte';

  // Who put the version an environment runs in place: the change that moved
  // it there, who asked and for whom, who approved and what every check said.
  // No change on a partial history means unknown, never nobody.
  let { namespace, stage, miraURL = '', onClose } = $props();

  let blame = $state(null);
  let error = $state(null);
  let headingEl = $state(null);
  const c = $derived(blame?.change);

  onMount(async () => {
    headingEl?.focus();
    try { blame = await api.deliveryBlame(namespace, stage); } catch (e) { error = e; }
  });
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<section class="hp" aria-labelledby="bp-{namespace}-{stage}" data-testid="blame-panel"
  onkeydown={(e) => { if (e.key === 'Escape') { e.preventDefault(); onClose?.(); } }}>
  <h3 id="bp-{namespace}-{stage}" class="t-subsection-title" tabindex="-1" bind:this={headingEl}>
    Who put {blame ? versionLabel(blame.digest, blame.versions) || 'this version' : 'this version'} here
  </h3>
  {#if error}
    <RefusalNotice {error} />
  {:else if !blame}
    <p class="t-body-2" role="status">Reading the records…</p>
  {:else}
    {#if c}
      <p class="t-body" data-testid="blame-change">
        <a href={deliveryChangeUrl(namespace, c.operation)}>{typeLabel(c.type)}</a>
        by <strong><Person user={c.actor} people={blame?.people} /></strong>{#if actorBadge(c.actorClass)}{@const ab = actorBadge(c.actorClass)}{' '}<span class="badge {ab.tone}" data-testid="actor-badge">{ab.label}</span>{/if}{#if blame.onBehalfOf}, for <strong><Person user={blame.onBehalfOf} people={blame?.people} /></strong>{/if}{#if c.from}, from {versionLabel(c.from, blame.versions)}{/if}{#if c.finished}, finished <time datetime={c.finished}>{timeLabel(c.finished)}</time>{/if}.
        {#if blame.intentReason}Why: “{blame.intentReason}”.{/if}
      </p>
      {#if blame.approvals?.length}
        <ul class="hp-list t-body-2">
          {#each blame.approvals as a, i (i)}
            <li><strong>{decisionLabel(a.decision)}</strong> by <Person user={a.by} people={blame?.people} />{#if a.authTime}, signed in at {authTimeText(a.authTime)}{/if}{#if a.note}: “{a.note}”{/if}</li>
          {/each}
        </ul>
      {:else}
        <p class="t-body-2">No person approved it: no decision was needed.</p>
      {/if}
      <VerdictList verdicts={blame.verdicts ?? []} {miraURL} testid="blame-verdicts" />
    {/if}
    <p class="t-body-2" data-testid="blame-source">
      {#if c && !blame.unsigned?.length}Read from its signed record.{:else if c}No signed record vouches for this: {blame.unsigned.join(', ')} come from the change's own status.{/if}
      {sentence(blame.note)}{#if blame.completeness !== 'complete'}{' '}The history is partial, so this may not be the whole story.{/if}
    </p>
  {/if}
  <div><button type="button" class="btn btn-sm" onclick={() => onClose?.()}>Close</button></div>
</section>

<style>
  .hp { border: 1px solid var(--c-border); border-radius: var(--radius-md); padding: var(--sp-4); display: flex; flex-direction: column; gap: var(--sp-2); background: var(--c-surface); overflow-wrap: anywhere; }
  .hp h3:focus { outline: none; }
  .hp p { margin: 0; }
  .hp a { color: var(--c-accent); text-decoration: underline; }
  .hp-list { margin: 0; padding-left: var(--sp-5); }
</style>
