<script>
  import IdentityBadge from './IdentityBadge.svelte';
  import CappedList from './CappedList.svelte';
  import { guaranteeBadge, lifecycleBadge } from '../lib/behavior.ts';

  // The behavior verdicts on one dependency, as badges for a row: whether the provider
  // meets what the dependency requires, and each deprecated interface of the provider.
  // Both were computed by the backend; the same row reads the same on the provider's
  // page (who still depends on a deprecated interface) and on the consumer's.
  let { status = '', reason = '', deprecated = [], today = undefined } = $props();
  const guarantee = $derived(guaranteeBadge(status, reason));
  const badges = $derived((deprecated ?? []).map((d) => {
    const b = lifecycleBadge(d, today);
    return { ...b, label: `${d.interface}: ${b.label.toLowerCase()}`, title: `Interface ${d.interface}. ${b.title}` };
  }));
</script>

{#if guarantee}<IdentityBadge label={guarantee.label} tone={guarantee.tone} title={guarantee.title} />{/if}
{#if badges.length}
  <CappedList items={badges} max={2} row label="Deprecated interfaces of the provider">
    {#snippet item(b)}<IdentityBadge label={b.label} tone={b.tone} title={b.title} />{/snippet}
  </CappedList>
{/if}
