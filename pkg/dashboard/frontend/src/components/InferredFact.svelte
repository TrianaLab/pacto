<script>
  // A value a source outside every contract reports (an owner or a business domain):
  // muted text and one discreet mark whose tip names the source, the record it read
  // and when. Never a link and never the value itself: Pacto does not route or gate on it.
  import ProvenanceMark from './ProvenanceMark.svelte';
  import { inferredFactText } from '../lib/entityLabels.ts';
  import { ownerKeyKind, ownerKeyLabel } from '../lib/owners.ts';

  // what: 'owner' (the value is an owner key: a team shows bare, a DRI with its kind) or 'domain'.
  let { fact, what } = $props();
  const label = $derived(what === 'owner'
    ? (ownerKeyKind(fact.value) === 'DRI' ? `DRI ${ownerKeyLabel(fact.value)}` : ownerKeyLabel(fact.value))
    : fact.value);
</script>

<span class="if-note" data-testid="{what}-inferred" title={label}>inferred: <bdi>{label}</bdi></span><span class="if-mark"><ProvenanceMark label="About the inferred {what}" text={inferredFactText(fact)} /></span>

<style>
  /* A long value truncates; its title carries it in full. */
  .if-note {
    display: inline-block; max-inline-size: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
    vertical-align: middle; margin-inline-start: var(--sp-2); color: var(--c-text-3); font-size: var(--text-xs);
  }
  .if-mark { display: inline-flex; vertical-align: middle; margin-inline-start: var(--sp-1); }
</style>
