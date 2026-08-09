<script>
  import { kindLabel } from '../lib/entityLabels.ts';
  import { abbreviateDigests } from '../lib/format.ts';
  import EntityStatusBadge from './EntityStatusBadge.svelte';
  import CopyableIdentifier from './CopyableIdentifier.svelte';

  // Renders enough identity to DISAMBIGUATE an entity from same-named entities in
  // other domains/scopes (requirement F): kind, human label, and the qualifying
  // context (domain / scope / parent service). `ref` is a product EntityRef.
  // showKind is turned off where a caption beside the link ALREADY names the kind
  // ("Owner" next to an OWNER chip). Two labels for one fact is how a page starts
  // reading like a form instead of a sentence.
  let { ref = {}, showStatus = true, showKey = false, showKind = true } = $props();

  const primary = $derived(ref.label || ref.key || '(unknown)');

  // A context bit the label already spells out is not disambiguation, it is the same
  // word twice: rows read "payments-service 1.2.0 · payments-service · 45cc…" and
  // "prod/k8s/orders-service · orders-service · prod". Whole segments only (bounded by
  // the start, the end or a separator), so a target whose own name genuinely differs
  // from its service still says which service it belongs to.
  function spelledOut(label, bit) {
    const i = label.indexOf(bit);
    if (i < 0) return false;
    const sep = (c) => c === undefined || '/ @:·,'.includes(c);
    return sep(label[i - 1]) && sep(label[i + bit.length]);
  }

  // The disambiguating context bits, in priority order. Same-named services in two
  // domains differ by domain; targets differ by scope + parent service.
  const context = $derived(
    [
      { raw: ref.domain, text: `domain ${ref.domain}` },
      { raw: ref.parentService && ref.parentService !== ref.key ? ref.parentService : '', text: ref.parentService },
      { raw: ref.scope, text: ref.scope },
      // secondary is the copyable-ish extra (a digest or scope); show it only when it
      // is not already the key and not already surfaced as scope.
      { raw: ref.secondary && ref.secondary !== ref.scope ? ref.secondary : '', text: ref.secondary },
    ]
      .filter((b) => b.raw && !spelledOut(primary, b.raw))
      .map((b) => b.text),
  );

  // A content digest is 71 characters that identify content exactly and read as noise;
  // full width it also overflowed narrow cards and got clipped mid-string, which is
  // neither readable NOR complete. Show a recognizable prefix here, keep the exact value
  // in the tooltip, and leave the authoritative full digest on the revision page's
  // Identifier disclosure. Non-digest context (domain, scope, parent service) is short
  // and meaningful, so it comes back from the helper untouched.
  const display = (bit) => abbreviateDigests(bit);
</script>

<span class="entity-identity">
  {#if showKind}<span class="ei-kind">{kindLabel(ref.kind)}</span>{/if}
  <span class="ei-label">{primary}</span>
  {#if showStatus}<EntityStatusBadge kind={ref.kind} status={ref.status} />{/if}
  {#if context.length}
    <span class="ei-context">
      {#each context as bit, i}{#if i > 0}<span class="ei-dot" aria-hidden="true">·</span>{/if}<span title={display(bit) === bit ? undefined : bit}>{display(bit)}</span>{/each}
    </span>
  {/if}
</span>
{#if showKey && ref.key}
  <div class="ei-key"><CopyableIdentifier value={ref.key} label="canonical key" /></div>
{/if}

<style>
  .entity-identity { display: inline-flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; min-width: 0; }
  .ei-kind {
    font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.04em;
    color: var(--c-text-3); background: var(--c-surface-inset);
    padding: 1px 6px; border-radius: var(--radius-xs); flex-shrink: 0;
  }
  .ei-label { font-weight: 600; color: var(--c-text); overflow-wrap: anywhere; }
  .ei-context { font-size: var(--text-xs); color: var(--c-text-3); display: inline-flex; gap: var(--sp-1); flex-wrap: wrap; min-width: 0; overflow-wrap: anywhere; }
  .ei-dot { color: var(--c-text-3); }
  .ei-key { margin-top: var(--sp-1); }
</style>
