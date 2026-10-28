<script>
  import PreviewSection from './PreviewSection.svelte';
  import LimitationsList from './LimitationsList.svelte';

  // The fold every entity page ends with: what Pacto could not determine about this
  // entity. `page` is the entity detail's bounded limitations preview; nothing renders
  // when it is empty.
  let { page } = $props();
</script>

{#if (page?.count ?? 0) > 0}
  <PreviewSection title="Limitations" tone="warn" collapsible open={false} summary="What Pacto could not determine" total={page.total ?? 0} count={page.count} truncated={page.truncated}>
    <LimitationsList items={page.items ?? []} />
  </PreviewSection>
{/if}
