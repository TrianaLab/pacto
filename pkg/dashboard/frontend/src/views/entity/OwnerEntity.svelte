<script>
  import { fleetAttentionUrl } from '../../lib/router.ts';
  import EntityLink from '../../components/EntityLink.svelte';
  import SeverityBadge from '../../components/SeverityBadge.svelte';
  import PreviewSection from '../../components/PreviewSection.svelte';
  import EntityRefList from '../../components/EntityRefList.svelte';
  import PostureBars from '../../components/viz/PostureBars.svelte';

  // The owner page (requirement G): the owner-scoped operational posture, bounded
  // previews of the owner's services, revisions and deployments, and an attention
  // preview for the owner's estate. Every owned/affected entity is navigable via
  // EntityLink.
  //
  // The posture is drawn from `summary`, the backend aggregate over the owner's
  // COMPLETE service / revision / target populations — not from the capped previews
  // below it. It asks the same three questions in the same visual language a service
  // page does, one scope up: an owner opens this page to find out whether their whole
  // estate is behaving, and four lists could not tell them.
  let { detail } = $props();
  const d = $derived(detail.owner ?? {});
  const sum = $derived(d.summary ?? {});
  // The owner filter/action is built from the CANONICAL owner key, never the display
  // label (requirement F3): the key is the stable identity the backend owner filter
  // matches; a label is presentation and may not round-trip.
  const owner = $derived(detail.entity?.key || detail.entity?.label || '');
  const attnHref = $derived(fleetAttentionUrl({ owner }));
  // Every posture bucket drills into THIS owner's backlog, not the fleet's.
  const attentionUrl = $derived((category) => fleetAttentionUrl({ owner, category }));
</script>

<div class="owner-entity">
  <section class="oe-posture" id="sec-operational-summary" data-toc="Operational summary" aria-labelledby="oe-posture-h">
    <h2 id="oe-posture-h" class="t-section-title">Operational summary</h2>
    <p class="oe-lead t-body-2">
      {sum.services ?? 0} {(sum.services ?? 0) === 1 ? 'service' : 'services'} ·
      {sum.revisions ?? 0} {(sum.revisions ?? 0) === 1 ? 'revision' : 'revisions'}{(sum.invalidRevisions ?? 0) > 0 ? ` (${sum.invalidRevisions} invalid)` : ''} ·
      {sum.targets ?? 0} operational {(sum.targets ?? 0) === 1 ? 'target' : 'targets'}
    </p>
    <PostureBars
      summary={sum}
      {attentionUrl}
      empty="Nothing running has been observed for this owner's services, so there is no compliance, identity or freshness picture yet."
    />
  </section>

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
          <SeverityBadge severity={it.severity} />
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
  /* Same card as the service page's operational summary: one scope up should not look
     like a different product. */
  .oe-posture { border: 1px solid var(--c-border); border-radius: var(--radius-md); padding: var(--sp-4); background: var(--c-surface); display: flex; flex-direction: column; gap: var(--sp-3); }
  .oe-posture h2 { margin: 0; }
  .oe-lead { margin: 0; }
  .oe-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(240px, 1fr)); gap: var(--sp-3); }
  .oe-attn { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .oe-attn-row { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .oe-cat { font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.03em; color: var(--c-text-3); background: var(--c-surface-inset); padding: 1px 6px; border-radius: var(--radius-xs); }
  .oe-sum { color: var(--c-text-2); font-size: var(--text-sm); }
</style>
