<script>
  import { untrack } from 'svelte';

  // A searchable picker for a list the page already holds (hundreds of services or
  // versions). A native <select> of that size matches only prefixes, sizes itself to
  // its longest option and reads as one long identical column. A text field with a
  // native datalist matches anywhere in the label, keeps its width and costs no code
  // to keyboard or screen-reader users. It commits when the text is one of the
  // options, so a half-typed name never fires a pick; emptying it picks ''.
  let { value = '', options = [], label, placeholder = 'Type to search', id, testid = 'search-pick', onpick } = $props();

  const labelOf = (v) => options.find((o) => o.value === v)?.label ?? v;
  let draft = $state(untrack(() => labelOf(value)));
  $effect(() => { draft = labelOf(value); });

  function commit() {
    const t = draft.trim();
    if (!t) { if (value) onpick(''); return; }
    const hit = options.find((o) => o.label === t || o.value === t);
    if (hit && hit.value !== value) onpick(hit.value);
  }
</script>

<input class="input sp-input" type="search" {id} list={`${id}-options`} bind:value={draft} {placeholder} aria-label={label}
  onchange={commit} onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); commit(); } }} data-testid={testid} />
<datalist id={`${id}-options`}>
  {#each options as o (o.value)}<option value={o.label}></option>{/each}
</datalist>

<style>
  .sp-input { min-width: 0; width: 100%; max-width: 22rem; }
</style>
