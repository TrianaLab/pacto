<script>
  import { fleetAttentionUrl } from '../../lib/router.ts';
  import EntityLink from '../../components/EntityLink.svelte';
  import StatusBadge from '../../components/StatusBadge.svelte';
  import PreviewSection from '../../components/PreviewSection.svelte';
  import EntityRefList from '../../components/EntityRefList.svelte';

  // The owner page (requirement G): bounded previews of the owner's services,
  // revisions and deployments, plus an attention preview for the owner's estate.
  // Every owned/affected entity is navigable via EntityLink.
  let { detail } = $props();
  const d = $derived(detail.owner ?? {});
  // The owner filter/action is built from the CANONICAL owner key, never the display
  // label (requirement F3): the key is the stable identity the backend owner filter
  // matches; a label is presentation and may not round-trip.
  const owner = $derived(detail.entity?.key || detail.entity?.label || '');
  const attnHref = $derived(fleetAttentionUrl({ owner }));
</script>

<div class="owner-entity">
  <div class="oe-grid">
    <PreviewSection title="Services" total={d.services?.total ?? 0} count={d.services?.count ?? 0} truncated={d.services?.truncated} empty="No services.">
      <EntityRefList items={d.services?.items ?? []} />
    </PreviewSection>
    <PreviewSection title="Revisions" total={d.revisions?.total ?? 0} count={d.revisions?.count ?? 0} truncated={d.revisions?.truncated} empty="No revisions.">
      <EntityRefList items={d.revisions?.items ?? []} showStatus={false} />
    </PreviewSection>
    <PreviewSection title="Operational targets" total={d.deployments?.total ?? 0} count={d.deployments?.count ?? 0} truncated={d.deployments?.truncated} empty="No running target observed.">
      <EntityRefList items={d.deployments?.items ?? []} />
    </PreviewSection>
  </div>

  <PreviewSection title="Needs attention" total={d.attention?.total ?? 0} count={d.attention?.count ?? 0} truncated={d.attention?.truncated} viewAllHref={attnHref} viewAllLabel="View all for this owner" empty="Nothing needs attention.">
    <ul class="oe-attn">
      {#each d.attention?.items ?? [] as it, i (i)}
        <li class="oe-attn-row">
          <StatusBadge status={it.severity} />
          <span class="oe-cat">{it.category}</span>
          <EntityLink ref={it.entity} showStatus={false} />
          <span class="oe-sum">{it.summary || it.reason || it.label}</span>
        </li>
      {/each}
    </ul>
  </PreviewSection>
</div>

<style>
  .owner-entity { display: flex; flex-direction: column; gap: var(--sp-4); }
  .oe-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(240px, 1fr)); gap: var(--sp-3); }
  .oe-attn { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .oe-attn-row { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .oe-cat { font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.03em; color: var(--c-text-3); background: var(--c-surface-inset); padding: 1px 6px; border-radius: var(--radius-xs); }
  .oe-sum { color: var(--c-text-2); font-size: var(--text-sm); }
</style>
