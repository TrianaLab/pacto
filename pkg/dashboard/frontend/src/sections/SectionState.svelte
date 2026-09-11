<script>
  import CollapsibleSection from '../CollapsibleSection.svelte';

  // Placeholder for a section that has nothing to show. Every domain section
  // renders one rather than disappearing, so a reader can tell "nothing is
  // declared here" from "this page forgot to load it" — and so the in-page
  // contents list never offers a heading that is not on the page.
  //
  // A section owns the decision to render this: the predicate for "has content"
  // is the same one that draws the section, so the two cannot drift.
  let { title, open = $bindable(false), id = '' } = $props();
</script>

<CollapsibleSection {title} bind:open {id}>
  <p class="section-state">None declared</p>
</CollapsibleSection>

<style>
  .section-state {
    margin: 0;
    padding: var(--sp-3);
    border: 1px dashed var(--c-border);
    border-radius: var(--radius-sm);
    color: var(--c-text-3);
    font-size: var(--text-sm);
    font-weight: 600;
  }
</style>
