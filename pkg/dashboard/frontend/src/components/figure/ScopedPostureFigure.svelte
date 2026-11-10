<script>
  // The posture figure of one owner's or one domain's services (SPEC 7: owner and domain
  // pages): the backend's compliance tally over every service the filter selects, and
  // each row lists exactly those services, so a count and its list never disagree. The
  // aside says how many services, which domains (an owner page) or owners (a domain
  // page) they span, up to three named and "N more" to the filtered list, how many have
  // no identified owner (a domain page) and when.
  import { onDestroy } from 'svelte';
  import { api } from '../../lib/api.ts';
  import { createProductLoader } from '../../lib/productLoader.svelte.ts';
  import { complianceSegments, statusHrefs } from '../../lib/distributions.ts';
  import { fleetServicesUrl, fleetDomainsUrl, fleetOwnersUrl, hashForHref } from '../../lib/router.ts';
  import { formatCount } from '../../lib/format.ts';
  import { formatDate } from '../../lib/dateFormat.ts';
  import { elidable } from '../../lib/elide.ts';
  import PostureFigure from './PostureFigure.svelte';

  /** @type {{ filter: { ownerKey?: string, businessDomain?: string } }} */
  let { filter } = $props();
  const CHIPS = 3;
  const isOwner = $derived(!!filter.ownerKey);
  // An owner key names a team or a person (dri:), and the question says which.
  const who = $derived(filter.ownerKey?.startsWith('dri:') ? 'this person owns' : 'this team owns');
  const question = $derived(isOwner ? `How compliant are the services ${who}?` : 'How compliant are the services in this domain?');

  const loader = createProductLoader(() => api.fleetEntities({ kinds: ['service'], limit: 1, ...filter }));
  $effect(() => { loader.sync(JSON.stringify(filter), JSON.stringify(filter)); });
  onDestroy(() => loader.destroy());
  // The other kind this scope spans, ranked as its table is: the first three are named.
  const spanLoader = createProductLoader(() => api.fleetEntities(filter.ownerKey
    ? { kinds: ['domain'], ownerKey: filter.ownerKey, limit: CHIPS }
    : { kinds: ['owner'], businessDomain: filter.businessDomain, limit: CHIPS }));
  $effect(() => { spanLoader.sync(`span@@${JSON.stringify(filter)}`, JSON.stringify(filter)); });
  onDestroy(() => spanLoader.destroy());

  const agg = $derived(loader.data?.aggregate ?? null);
  const meta = $derived(loader.data?.meta ?? null);
  const total = $derived(agg?.services ?? 0);
  const segments = $derived(complianceSegments(agg?.serviceCompliance, statusHrefs((status) => fleetServicesUrl({ ...filter, status }))));
  const span = $derived(spanLoader.data ?? null);
  const spanAll = $derived(isOwner ? fleetDomainsUrl({ ownerKey: filter.ownerKey }) : fleetOwnersUrl({ businessDomain: filter.businessDomain }));
  const unidentified = $derived((agg?.ownership?.unowned ?? 0) + (agg?.ownership?.conflicting ?? 0) + (agg?.unidentifiedOwnership ?? 0));
</script>

{#if total > 0}
  <PostureFigure {question} {segments} {total} {meta} scope="{formatCount(total)} {total === 1 ? 'service' : 'services'} {isOwner ? who : 'in this domain'}">
    {#snippet aside()}
      <dl class="facts">
        <dt>Services</dt><dd><a href={fleetServicesUrl(filter)}>{formatCount(total)}</a></dd>
        <dt>{isOwner ? 'Domains' : 'Owners'}</dt>
        <dd class="span-chips" data-testid="posture-span">
          {#if span}
            {#each span.entities as ref (ref.key)}
              <a class="badge chip t-neutral" href={hashForHref(ref.href)} title={ref.label}><bdi class:fit={elidable(ref.label)}>{ref.label}</bdi></a>
            {:else}<span class="t-meta">None</span>{/each}
            <!-- data-lists: the list holds every one of them, the named ones too. -->
            {#if span.total > span.entities.length}<a href={spanAll} data-lists={span.total}>{formatCount(span.total - span.entities.length)} more</a>{/if}
          {/if}
        </dd>
        {#if !isOwner}
          <dt>No identified owner</dt><dd>{#if unidentified}<a href={fleetServicesUrl({ ...filter, ownership: 'unidentified' })}>{formatCount(unidentified)}</a>{:else}<span data-nofilter>0</span>{/if}</dd>
        {/if}
        {#if meta?.asOf}<dt>As of</dt><dd>{formatDate(meta.asOf)}</dd>{/if}
      </dl>
    {/snippet}
  </PostureFigure>
{/if}

<style>
  .span-chips { display: flex; flex-wrap: wrap; align-items: center; gap: var(--sp-1) var(--sp-2); min-width: 0; }
</style>
