<script>
  import { fleetAttentionUrl, fleetServicesUrl } from '../../lib/router.ts';
  import { attentionCategoryLabel } from '../../lib/entityLabels.ts';
  import { ownershipSegments, ownershipHrefs } from '../../lib/distributions.ts';
  import { formatCount } from '../../lib/format.ts';
  import EntityLink from '../../components/EntityLink.svelte';
  import SeverityBadge from '../../components/SeverityBadge.svelte';
  import PreviewSection from '../../components/PreviewSection.svelte';
  import EntityRefList from '../../components/EntityRefList.svelte';
  import PostureBars from '../../components/viz/PostureBars.svelte';
  import DistributionBar from '../../components/viz/DistributionBar.svelte';

  // The business-domain page, one scope up from a service and drawn like an owner page:
  // the posture over every service in the domain, its declared ownership, bounded
  // previews of its services, owners and operational targets, and its attention. Every
  // drill-down stays scoped to this domain by its name, which is its key.
  let { detail } = $props();
  const d = $derived(detail.domain ?? {});
  const sum = $derived(d.summary ?? {});
  const key = $derived(detail.entity?.key ?? '');
  const services = $derived(sum.services ?? 0);
  // Every entity on this page is in this domain, so "domain <name>" on each row is the
  // same word again and is dropped from the previews.
  const local = (items) => (items ?? []).map((r) => (r?.businessDomain ? { ...r, businessDomain: undefined } : r));
  const attentionUrl = $derived((category) => fleetAttentionUrl({ businessDomain: key, category }));
  const ownership = $derived(ownershipSegments(d.ownership,
    ownershipHrefs((ownership) => fleetServicesUrl({ businessDomain: key, ownership }))));
</script>

<div class="domain-entity">
  <section class="de-posture" id="sec-operational-summary" data-toc="Operational summary" aria-labelledby="de-posture-h">
    <h2 id="de-posture-h" class="t-section-title">Operational summary</h2>
    <p class="de-lead t-body-2">
      {formatCount(services)} {services === 1 ? 'service' : 'services'} ·
      {formatCount(sum.revisions ?? 0)} {(sum.revisions ?? 0) === 1 ? 'revision' : 'revisions'}{(sum.invalidRevisions ?? 0) > 0 ? ` (${sum.invalidRevisions} invalid)` : ''} ·
      {formatCount(sum.targets ?? 0)} operational {(sum.targets ?? 0) === 1 ? 'target' : 'targets'}
    </p>
    <PostureBars summary={sum} {attentionUrl} empty="Nothing running has been seen for the services in this domain yet." />
    <DistributionBar
      title="Declared ownership"
      description="Authored on each contract revision, so a service is cleanly owned only when its revisions agree."
      scopeNote={`All ${formatCount(services)} ${services === 1 ? 'service' : 'services'} in this domain.`}
      segments={ownership}
      total={services}
      emptyLabel="No services in this domain yet."
    />
  </section>

  <div class="de-grid">
    <PreviewSection title="Services" total={d.services?.total ?? 0} count={d.services?.count ?? 0} truncated={d.services?.truncated}
      viewAllHref={fleetServicesUrl({ businessDomain: key })} viewAllLabel="View all in this domain" empty="No services.">
      <EntityRefList items={local(d.services?.items)} />
    </PreviewSection>
    <PreviewSection title="Owners" total={d.owners?.total ?? 0} count={d.owners?.count ?? 0} truncated={d.owners?.truncated} empty="No declared owner.">
      <EntityRefList items={d.owners?.items ?? []} showStatus={false} />
    </PreviewSection>
    <PreviewSection title="Operational targets" total={d.deployments?.total ?? 0} count={d.deployments?.count ?? 0} truncated={d.deployments?.truncated} empty="No running target observed.">
      <EntityRefList items={local(d.deployments?.items)} />
    </PreviewSection>
  </div>

  <PreviewSection title="Needs attention" total={d.attention?.total ?? 0} count={d.attention?.count ?? 0} truncated={d.attention?.truncated}
    viewAllHref={fleetAttentionUrl({ businessDomain: key })} viewAllLabel="View all for this domain" empty="Nothing needs attention.">
    <ul class="de-attn">
      {#each d.attention?.items ?? [] as it, i (i)}
        <li class="de-attn-row">
          <SeverityBadge severity={it.severity} />
          <span class="de-cat">{attentionCategoryLabel(it.category)}</span>
          <EntityLink ref={local([it.entity])[0]} showStatus={false} />
          <span class="de-sum">{it.summary || it.reason || it.label}</span>
        </li>
      {/each}
    </ul>
  </PreviewSection>
</div>

<style>
  .domain-entity { display: flex; flex-direction: column; gap: var(--sp-4); }
  .de-posture { border: 1px solid var(--c-border); border-radius: var(--radius-md); padding: var(--sp-4); background: var(--c-surface); display: flex; flex-direction: column; gap: var(--sp-3); }
  .de-posture h2, .de-lead { margin: 0; }
  .de-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(240px, 1fr)); gap: var(--sp-3); }
  .de-attn { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .de-attn-row { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; min-width: 0; }
  .de-cat { color: var(--c-text-3); background: var(--c-surface-inset); padding: 1px 6px; border-radius: var(--radius-xs); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); letter-spacing: var(--role-label-tracking); }
  .de-sum { color: var(--c-text-2); font-size: var(--text-sm); overflow-wrap: anywhere; }
</style>
