<script>
  import { fleetServicesUrl } from '../../lib/router.ts';
  import { formatCount } from '../../lib/format.ts';
  import { inService } from '../../lib/identityContext.ts';
  import EntityLink from '../../components/EntityLink.svelte';
  import IdentityBadge from '../../components/IdentityBadge.svelte';
  import OwnershipFact from '../../components/OwnershipFact.svelte';
  import PreviewSection from '../../components/PreviewSection.svelte';
  import CopyableIdentifier from '../../components/CopyableIdentifier.svelte';
  import LifecycleBadge from '../../components/LifecycleBadge.svelte';
  import GuaranteeTerms from '../../components/GuaranteeTerms.svelte';
  import ConfigurationList from '../../components/ConfigurationList.svelte';
  import PolicyList from '../../components/PolicyList.svelte';
  import OfferingVersions from '../../components/OfferingVersions.svelte';
  import ProvisioningBadges from '../../components/ProvisioningBadges.svelte';
  import OfferingFigure from '../../components/figure/OfferingFigure.svelte';
  import OfferingUseFigure from '../../components/figure/OfferingUseFigure.svelte';

  // The platform-offering page: every service whose contract declares this
  // service.offering, with what a consumer needs to choose one (owner, versions,
  // interfaces, configuration and policies) and a snippet to consume it, then the
  // services that already consume it, with what their providers report about the
  // resources they asked for.
  let { detail } = $props();
  const d = $derived(detail.offering ?? {});
  const key = $derived(detail.entity?.key ?? '');
  const providers = $derived(d.providers ?? { items: [], total: 0, count: 0 });
  const consumers = $derived(d.consumers ?? { items: [], total: 0, count: 0 });
  const VIA = { dependency: 'dependency', configuration_reference: 'configuration', policy_reference: 'policy' };
  const RANGE_TONE = { satisfied: 'ok', unsatisfied: 'warn', unknown: 'neutral' };
  const hiddenConsumers = $derived(d.hiddenConsumers ?? 0);
  const DOCS = 'https://pacto.run/latest/provision-infrastructure/';
  // The request the reader opened: which use row and which of its statuses.
  let picked = $state(/** @type {{ row: number, i: number } | null} */ (null));
  const pickedUse = $derived(picked ? consumers.items?.[picked.row] : null);
  const pickedRequest = $derived(pickedUse?.provisioning?.[picked?.i ?? -1] ?? null);
</script>

<div class="offering-entity">
  <OfferingFigure offering={key} {providers} {consumers} hidden={hiddenConsumers} providersHref={fleetServicesUrl({ offering: key })} />
  <PreviewSection title="Providers" total={providers.total ?? 0} count={providers.count ?? 0} truncated={providers.truncated}
    viewAllHref={fleetServicesUrl({ offering: key })} viewAllLabel="View all providers" empty="No service provides this offering."
    help="Services whose contract declares this platform offering.">
    <ul class="oe-providers">
      {#each providers.items ?? [] as p (p.service?.key)}
        {@const self = p.service?.label ?? ''}
        <li class="inset-card oe-provider" data-testid="offering-provider">
          <div class="oe-head">
            <EntityLink ref={p.service} showStatus={false} showKind={false} />
            {#if p.ownership}<OwnershipFact ownership={p.ownership} />{/if}
            {#if p.offeringProvenance === 'observed'}<IdentityBadge label="observed" tone="info" title="Its targets report this offering. Its contract declares none." describe />{/if}
            <LifecycleBadge lifecycle={p.lifecycle} detail={false} />
          </div>
          {#if p.summary}<p class="oe-summary t-body-2">{p.summary}</p>{/if}
          {#if p.currentRevision}
            <div class="oe-row"><span class="oe-k">Current</span><EntityLink ref={inService(p.currentRevision, self)} showStatus={false} showKind={false} /></div>
          {/if}
          <OfferingVersions offering={key} provider={p.service} versions={p.versions} {self} />
          {#if (p.interfaces?.items ?? []).length}
            <div class="oe-row oe-col">
              <span class="oe-k">Interfaces</span>
              <ul class="oe-ifaces">
                {#each p.interfaces.items as i, ii (`${i.name}:${ii}`)}
                  <li>
                    <span class="oe-name">{i.name}</span>
                    {#if i.type}<IdentityBadge label={i.type} tone="info" />{/if}
                    <LifecycleBadge lifecycle={i.lifecycle} detail={false} />
                    {#if i.guarantees}<GuaranteeTerms guarantees={i.guarantees} label={`Guarantees of ${i.name}`} />{/if}
                  </li>
                {/each}
              </ul>
            </div>
          {/if}
          {#if (p.configurations?.items ?? []).length}
            <div class="oe-col"><span class="oe-k">Configuration</span><ConfigurationList items={p.configurations.items} /></div>
          {/if}
          {#if (p.policies?.items ?? []).length}
            <div class="oe-col"><span class="oe-k">Policies</span><PolicyList items={p.policies.items} /></div>
          {/if}
          <div class="oe-col" data-testid="consume">
            {#if p.consume?.yaml}
              <div class="oe-row">
                <span class="oe-caption">Add this to your contract to consume {key} from {self}.</span>
                <CopyableIdentifier value={p.consume.yaml} label="consume snippet" iconOnly />
              </div>
              <!-- svelte-ignore a11y_no_noninteractive_tabindex (a long snippet scrolls sideways; focus lets a keyboard reader scroll it) -->
              <pre class="consume-snippet" tabindex="0"><code>{p.consume.yaml}</code></pre>
            {:else}
              <span class="oe-note">Publish this provider to a registry to get a snippet.</span>
            {/if}
          </div>
        </li>
      {/each}
    </ul>
    {#if providers.truncated}<p class="oe-note">+{formatCount((providers.total ?? 0) - (providers.count ?? 0))} more providers</p>{/if}
  </PreviewSection>

  <PreviewSection title="Consumers" total={consumers.total ?? 0} count={consumers.count ?? 0} truncated={consumers.truncated}
    viewAllHref={fleetServicesUrl({ consumes: key })} viewAllLabel="View all consumers"
    empty="No service has a resolved declaration that consumes this offering. References that are not digest-pinned or locked cannot be resolved and are not counted." help="Services whose contract declares a dependency on a provider of this offering or references its configuration or policy, once Pacto resolves the declaration. A configuration or policy reference resolves only when it is digest-pinned or locked.">
    <ul class="oe-uses">
      {#each consumers.items ?? [] as u, ui (`${u.consumer?.key}:${u.provider?.key}:${u.via}:${ui}`)}
        <li data-testid="offering-consumer">
          <EntityLink ref={u.consumer} showStatus={false} showKind={false} />
          <span class="oe-arrow" aria-hidden="true">→</span><span class="visually-hidden">consumes from</span>
          <EntityLink ref={u.provider} showStatus={false} showKind={false} />
          <IdentityBadge label={VIA[u.via] ?? u.via} tone="neutral" />
          {#if u.rangeStatus}<IdentityBadge label={u.rangeStatus} tone={RANGE_TONE[u.rangeStatus] ?? 'neutral'} title={u.compatibility ? `Accepts ${u.compatibility}` : ''} describe />{/if}
          <LifecycleBadge lifecycle={u.providerLifecycle} detail={false} />
          <ProvisioningBadges statuses={u.provisioning ?? []} selected={picked?.row === ui ? picked.i : -1}
            onselect={(i) => { picked = i < 0 ? null : { row: ui, i }; }} />
        </li>
      {/each}
    </ul>
  </PreviewSection>
  {#if pickedUse && pickedRequest}
    <OfferingUseFigure consumer={pickedUse.consumer?.label ?? ''} request={pickedRequest}
      useKey={`${pickedUse.consumer?.key}:${pickedUse.provider?.key}:${pickedUse.via}:${picked?.i}`} />
  {/if}
  <!-- Outside the section so the count shows when every consumer is hidden. -->
  {#if hiddenConsumers > 0}<p class="oe-note" data-testid="hidden-consumers">{formatCount(hiddenConsumers)} {hiddenConsumers === 1 ? 'consumer' : 'consumers'} you may not see</p>{/if}

  <p class="oe-foot t-body-2">Pacto validates what consumers request and shows what providers report. Provisioning runs in your platform's tools. <a href={DOCS} target="_blank" rel="noopener noreferrer">Provision infrastructure through contracts</a></p>
</div>

<style>
  .offering-entity { display: flex; flex-direction: column; gap: var(--sp-4); min-width: 0; }
  .oe-providers, .oe-uses, .oe-ifaces { list-style: none; margin: 0; padding: 0; }
  .oe-providers { display: flex; flex-direction: column; gap: var(--sp-3); }
  .oe-provider { display: flex; flex-direction: column; gap: var(--sp-2); min-width: 0; }
  .oe-head, .oe-row, .oe-uses li, .oe-ifaces li { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; min-width: 0; }
  .oe-col { display: flex; flex-direction: column; gap: var(--sp-1); min-width: 0; }
  .oe-ifaces, .oe-uses { display: flex; flex-direction: column; gap: var(--sp-1); }
  .oe-summary { margin: 0; color: var(--c-text-2); overflow-wrap: anywhere; }
  .oe-name { font-weight: 600; overflow-wrap: anywhere; }
  .oe-k { color: var(--c-text-3); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); letter-spacing: var(--role-label-tracking); }
  .oe-note, .oe-caption { color: var(--c-text-3); font-size: var(--text-sm); overflow-wrap: anywhere; }
  .oe-note { margin: 0; }
  .oe-arrow { color: var(--c-text-3); }
  /* The snippet scrolls by itself and never widens the page. */
  .consume-snippet { margin: 0; padding: var(--sp-2) var(--sp-3); max-width: 100%; overflow-x: auto; background: var(--c-surface-inset); border: 1px solid var(--c-border); border-radius: var(--radius-sm); font-size: var(--text-sm); }
  .oe-foot { margin: 0; color: var(--c-text-3); }
</style>
