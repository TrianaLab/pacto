<script>
  // The golden path figure (SPEC 7 r5: golden path entity): "What has this path
  // produced, and are those services compliant?". The template is an accent HubNode (the
  // subject's identity, S10) wired with an arrow into a PostureColumn of the services
  // created from it: the backend's compliance tally over every one of them (B5), each
  // row linking to exactly those services. Services hidden by the reader's access are
  // counted in the aside and never named or drawn. Ambient: no narration.
  import { onDestroy } from 'svelte';
  import { api } from '../../lib/api.ts';
  import { createProductLoader } from '../../lib/productLoader.svelte.ts';
  import { complianceSegments, statusHrefs } from '../../lib/distributions.ts';
  import { postureColumn } from '../../lib/fleetFigures.ts';
  import { fleetServicesUrl } from '../../lib/router.ts';
  import { formatCount, countOf } from '../../lib/format.ts';
  import { formatDate } from '../../lib/dateFormat.ts';
  import { elidable } from '../../lib/elide.ts';
  import Figure from './Figure.svelte';
  import HubNode from './HubNode.svelte';
  import PostureColumn from './PostureColumn.svelte';
  import ProvenanceStrip from './ProvenanceStrip.svelte';

  /** @type {{ path: string, name: string, version?: string, hidden?: number }} */
  let { path, name, version = '', hidden = 0 } = $props();

  const loader = createProductLoader(() => api.fleetEntities({ kinds: ['service'], template: 'exclude', createdFrom: path, limit: 1 }));
  $effect(() => { loader.sync(path, path); });
  onDestroy(() => loader.destroy());
  const agg = $derived(loader.data?.aggregate ?? null);
  const meta = $derived(loader.data?.meta ?? null);
  const total = $derived(agg?.services ?? 0);
  const segments = $derived(complianceSegments(agg?.serviceCompliance, statusHrefs((status) => fleetServicesUrl({ createdFrom: path, status }))));
  /** @type {import('../../lib/figure.ts').WireDef[]} */
  const lead = [{ id: 'wgp', from: 'gp-hub', fs: 'r', to: 'gp-col', ts: 'l', col: { fs: 'b', ts: 't' }, style: 'obs', tone: 'accent', head: true }];
  const wires = $derived([...lead, ...postureColumn(segments, total).wires]);
  const said = $derived(segments.filter((s) => s.value).map((s) => `${formatCount(s.value)} ${s.label.toLowerCase()}`).join(', '));
</script>

{#if agg && (total > 0 || hidden > 0)}
  <Figure id="golden-path" testid="golden-path-figure" question="What has this path produced, and are those services compliant?" {wires}
    alt={`${name} created ${countOf(total, 'service')} you can see: ${said || 'none'}.`}>
    {#snippet stage()}
      <div class="gpfig">
        <div class="gpfig-hub">
          <HubNode tone="accent" kind="golden-path" a="gp-hub" />
          <span class="gpfig-hl">
            <b class="id" title={name}><bdi class:fit={elidable(name)}>{name}</bdi></b>
            {#if version}<span class="t-meta">Template {version}</span>{/if}
            <span class="t-meta">created {countOf(total, 'service')}</span>
          </span>
        </div>
        <span></span>
        <div class="gpfig-col" data-a="gp-col"><PostureColumn {segments} {total} /></div>
      </div>
    {/snippet}
    {#snippet aside()}
      <dl class="facts">
        <dt>Services created</dt><dd>{#if total}<a href={fleetServicesUrl({ createdFrom: path })}>{formatCount(total)}</a>{:else}<span data-nofilter>0</span>{/if}</dd>
        {#if hidden}<dt>You may not see</dt><dd><span data-nofilter>{formatCount(hidden)}</span></dd>{/if}
        {#if meta?.asOf}<dt>As of</dt><dd>{formatDate(meta.asOf)}</dd>{/if}
      </dl>
    {/snippet}
    {#snippet caption()}
      {name} created {countOf(total, 'service')} you can see: {said || 'none'}. Unknown means no evidence; it is never counted as compliant.{#if hidden}{' '}{countOf(hidden, 'more service')} created from it {hidden === 1 ? 'is' : 'are'} hidden by your access and in no count.{/if}
      <ProvenanceStrip {meta} />
    {/snippet}
  </Figure>
{/if}
