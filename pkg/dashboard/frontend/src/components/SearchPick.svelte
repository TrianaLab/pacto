<script>
  import { untrack } from 'svelte';

  // The one picker for a list the page already holds. A short list is a native
  // <select>. A long one (hundreds of services or versions) is a text field with a
  // native datalist instead: a select of that size matches only prefixes, sizes
  // itself to its longest option and reads as one long identical column, while the
  // datalist matches anywhere in the label, keeps its width and needs no keyboard
  // code of ours. It commits when the text is one of the options, or appears in only
  // one, so an ambiguous half-typed name never fires a pick; emptying it picks ''.
  // Labels should be unique.
  let {
    value = '', options = [], label, placeholder = 'Type to search', id, testid = 'search-pick',
    onpick, disabled = false, max = 25,
  } = $props();

  const labelOf = (v) => options.find((o) => o.value === v)?.label ?? v;
  let draft = $state(untrack(() => labelOf(value)));
  $effect(() => { draft = labelOf(value); });

  function commit() {
    const t = draft.trim();
    if (!t) { if (value) onpick(''); return; }
    // A whole option, or the only one the text appears in.
    const q = t.toLowerCase();
    const some = options.filter((o) => o.label.toLowerCase().includes(q));
    const hit = options.find((o) => o.label === t || o.value === t) ?? (some.length === 1 ? some[0] : undefined);
    if (hit && hit.value !== value) onpick(hit.value);
  }
</script>

{#if options.length <= max}
  <select {id} {value} {disabled} aria-label={label} class="sp-select" onchange={(e) => onpick(e.currentTarget.value)} data-testid={testid}>
    <option value="">{placeholder}</option>
    {#each options as o, i (i)}<option value={o.value}>{o.label}</option>{/each}
  </select>
{:else}
  <input class="input sp-input" type="search" {id} {disabled} list={`${id}-options`} bind:value={draft} {placeholder} aria-label={label}
    onchange={commit} onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); commit(); } }} data-testid={testid} />
  <datalist id={`${id}-options`}>
    {#each options as o, i (i)}<option value={o.label}></option>{/each}
  </datalist>
{/if}

<style>
  .sp-input, .sp-select { min-width: 0; width: 100%; max-width: 22rem; }
</style>
