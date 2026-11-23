<script>
  import CopyableIdentifier from './CopyableIdentifier.svelte';
  import { ladderCommand } from '../lib/delivery.ts';

  // The inbox's one installation ladder block (511-S5): the chain the chart declares,
  // the rungs a person confirmed and, muted, the inferred rungs Pacto proposes, each a
  // leaf. One line; the sources sit in its details. The dashboard never signs: [Confirm]
  // hands a person the apiserver lets approve environmentladders the exact command.
  let { ladder } = $props();
  const panelId = $props.id();
  let open = $state(false);
  const declared = $derived(ladder.declared ?? []);
  const confirmed = $derived(ladder.confirmed ?? []);
  const proposal = $derived(ladder.proposal ?? []);
  const command = $derived(ladderCommand(ladder));
</script>

<details class="disclosure lb" data-testid="inbox-ladder">
  <summary>
    <span class="disclosure-caret" data-motion aria-hidden="true"></span>
    <span class="lb-line"><span class="lb-title">Installation ladder:</span>
      {#each declared as r, i (r.name)}{#if i}{' → '}{/if}<bdi title={r.name}>{r.name}</bdi>{/each}{#if declared.length}{' '}<span class="t-meta">(from the chart)</span>{/if}
      {#each confirmed as r (r.name)}{' + '}<bdi title={r.name}>{r.name}</bdi>{/each}
      {#if proposal.length}<span class="lb-inferred" data-testid="ladder-proposed">{#each proposal as r (r.name)}{' + '}<bdi title={r.name}>{r.name}</bdi>{/each}{' proposed'}</span>{/if}
    </span>
  </summary>
  <div class="lb-body">
    <ul class="lb-rungs">
      {#each declared as r (r.name)}<li><bdi title={r.name}>{r.name}</bdi>: declared, {r.source}</li>{/each}
      {#each confirmed as r (r.name)}<li><bdi title={r.name}>{r.name}</bdi>: confirmed by a person, {r.source}</li>{/each}
      {#each proposal as r (r.name)}<li class="lb-inferred" data-testid="ladder-rung"><bdi title={r.name}>{r.name}</bdi>: inferred from {r.source}</li>{/each}
    </ul>
    <p class="lb-text">An inferred environment feeds no other, so confirming it never changes what reaches the chain the chart declares.</p>
    {#if ladder.canConfirm}
      <button type="button" class="btn lb-btn" aria-expanded={open} aria-controls={panelId} onclick={() => (open = !open)}>Confirm</button>
      {#if open}
        <div class="lb-panel" id={panelId} data-testid="ladder-confirm">
          <CopyableIdentifier value={command} label="command" />
          <p class="lb-text">Confirming signs a record as you. Run it yourself; the proposal digest pins exactly what you read here.</p>
        </div>
      {/if}
    {/if}
  </div>
</details>

<style>
  .lb summary { display: flex; align-items: baseline; gap: var(--sp-2); cursor: pointer; min-block-size: var(--touch-min); }
  .lb-line { min-inline-size: 0; overflow-wrap: anywhere; font-size: var(--text-sm); }
  .lb-title { font-weight: 600; }
  .lb-inferred { color: var(--c-text-3); }
  .lb-body { display: flex; flex-direction: column; gap: var(--sp-2); margin-top: var(--sp-2); min-inline-size: 0; }
  .lb-rungs { margin: 0; padding-inline-start: var(--sp-4); font-size: var(--text-sm); overflow-wrap: anywhere; }
  .lb-text { margin: 0; font-size: var(--text-sm); overflow-wrap: anywhere; }
  .lb-btn { align-self: flex-start; min-block-size: var(--touch-min); }
  .lb-panel { display: flex; flex-direction: column; gap: var(--sp-2); min-inline-size: 0; }
  @media (pointer: coarse) {
    .lb summary, .lb-btn { min-block-size: 44px; }
    .lb-panel :global(.copyable-btn) { min-block-size: 44px; min-inline-size: 44px; }
  }
</style>
