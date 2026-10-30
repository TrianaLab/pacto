<script>
  import GuaranteeTerms from './GuaranteeTerms.svelte';
  import CappedList from './CappedList.svelte';
  import { mismatchText } from '../lib/behavior.ts';

  // What a dependency requires of each provider interface, what the provider refuses
  // and, when the check could not run, why. A refusal is a confirmed contradiction; a
  // check that could not run says so in its own words and lists nothing as refused.
  let { requires = [], unmet = [], status = '', reason = '' } = $props();
</script>

<div class="rq">
  {#each requires ?? [] as r, i (`${r.interface}:${i}`)}
    <div class="rq-row">
      <span class="rq-k">Requires from <code>{r.interface}</code></span>
      <GuaranteeTerms guarantees={r} required label={`Required from ${r.interface}`} />
    </div>
  {/each}
  {#if (unmet ?? []).length}
    <div class="rq-row">
      <span class="rq-k rq-refused">Refused</span>
      <CappedList items={unmet} max={3} label="Refused guarantees">
        {#snippet item(m)}<span class="rq-m">{mismatchText(m)}</span>{/snippet}
      </CappedList>
    </div>
  {/if}
  {#if status === 'unknown' && reason}<p class="rq-why">Not verified: {reason}.</p>{/if}
</div>

<style>
  .rq { display: flex; flex-direction: column; gap: var(--sp-1); min-width: 0; }
  .rq-row { display: flex; flex-wrap: wrap; gap: var(--sp-1) var(--sp-2); align-items: baseline; min-width: 0; }
  .rq-k { color: var(--c-text-3); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); letter-spacing: var(--role-label-tracking); }
  .rq-k code { font-variant-caps: normal; overflow-wrap: anywhere; }
  .rq-refused { color: var(--c-err); }
  .rq-m { font-size: var(--text-xs); color: var(--c-text); overflow-wrap: anywhere; }
  .rq-why { margin: 0; font-size: var(--text-xs); color: var(--c-text-3); overflow-wrap: anywhere; }
</style>
