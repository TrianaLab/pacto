<script>
  // The posture figure of one owner's or one domain's services (SPEC 7: owner and domain
  // pages): the backend's compliance tally over every service the filter selects, and
  // each row lists exactly those services, so a count and its list never disagree.
  import { onDestroy } from 'svelte';
  import { api } from '../../lib/api.ts';
  import { createProductLoader } from '../../lib/productLoader.svelte.ts';
  import { complianceSegments, statusHrefs } from '../../lib/distributions.ts';
  import { fleetServicesUrl } from '../../lib/router.ts';
  import { formatCount } from '../../lib/format.ts';
  import PostureFigure from './PostureFigure.svelte';

  /** @type {{ filter: { ownerKey?: string, businessDomain?: string }, question: string, scope: string }} */
  let { filter, question, scope } = $props();
  const loader = createProductLoader(() => api.fleetEntities({ kinds: ['service'], limit: 1, ...filter }));
  $effect(() => { loader.sync(JSON.stringify(filter), JSON.stringify(filter)); });
  onDestroy(() => loader.destroy());
  const agg = $derived(loader.data?.aggregate ?? null);
  const total = $derived(agg?.services ?? 0);
  const segments = $derived(complianceSegments(agg?.serviceCompliance, statusHrefs((status) => fleetServicesUrl({ ...filter, status }))));
</script>

{#if total > 0}
  <PostureFigure {question} {segments} {total} scope="{formatCount(total)} services {scope}" />
{/if}
