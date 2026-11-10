<script>
  // The Delivery Publish figure (SPEC 7): "Can every published version be traced to who
  // built it?". Build, Signature and Registry in one row; a signed version passes the
  // seal (solid), an unsigned one bypasses it underneath (dashed, warn). Under the
  // registry, three lanes partition the services: every version signed, an unsigned
  // version (up to 4 named, "N more" to the list), and a version whose signature
  // Pacto cannot tell from the Stages you can list (a downstream Stage, one fed by
  // another cluster's records). An unknown is never drawn as unsigned. No wire runs from the registry to
  // the lanes: at fleet widths it would cross the lane words. Ambient: no narration.
  import Figure from './Figure.svelte';
  import Node from './Node.svelte';
  import Lane from './Lane.svelte';
  import { formatCount, countOf } from '../../lib/format.ts';
  import { sharePct } from '../../lib/figure.ts';

  /** @type {{ counts: import('../../lib/api.ts').DeliveryPublished['counts'], href: (signed: string) => string, serviceHref: (name: string) => string }} */
  let { counts, href, serviceHref } = $props();
  const unknownSvcs = $derived(counts.unknownServices ?? 0);
  const signedSvcs = $derived(counts.services - counts.unsignedServices - unknownSvcs);
  // The bypass is drawn only when a version took it.
  const wires = $derived([
    { id: 'wb', from: 'pb', to: 'ps', style: 'obs', tone: 'neutral', head: true, col: { from: 'pb', fs: 'b', to: 'ps', ts: 't' } },
    { id: 'wsr', from: 'ps', to: 'pr', style: 'obs', tone: 'ok', head: true, col: { from: 'ps', fs: 'b', to: 'pr', ts: 't' } },
    ...(counts.unsigned ? [{ id: 'wby', from: 'pb', fs: 'b', to: 'pr', ts: 'b', style: 'proj', tone: 'warn', head: true, when: ['wide', 'mid'] }] : []),
  ]);
  const chips = $derived((counts.firstUnsigned ?? []).map((name) => ({ key: name, label: name, href: serviceHref(name), ring: false })));
  const alt = $derived(`${counts.services} services with a known version: ${counts.unsignedServices} with an unsigned version, ${unknownSvcs} with a signature not known here, ${signedSvcs} with every version signed.`);
</script>

<Figure id="signed" testid="publish-figure" question="Can every published version be traced to who built it?" alt={alt} {wires} rowMin={0} compact={true}>
  {#snippet stage()}
    <div class="pub">
      <div class="pnode"><b>Build</b><span class="t-meta">CI builds the bundle</span><span class="pslot"><Node tone="neutral" big a="pb" m="pb" /></span></div>
      <div class="pnode seal"><b>Signature</b><span class="t-meta">the builder signs it</span><span class="pslot"><Node tone="accent" glyph="ok" big a="ps" m="ps" /></span></div>
      <div class="pnode"><b>Registry</b><span class="t-meta">{countOf(counts.versions, 'version')}</span><span class="pslot"><Node tone="neutral" shape="square" big a="pr" m="pr" /></span></div>
      <div class="lanes pub-lanes">
        <Lane k="sig" tone="ok" glyph="ok" label="Every version signed" n={signedSvcs} total={counts.services} href={href('yes')} />
        <Lane k="uns" tone="warn" glyph="warn" label="An unsigned version" n={counts.unsignedServices} total={counts.services} {chips} href={href('no')} />
        {#if unknownSvcs}<Lane k="unk" tone="neutral" glyph="unknown" label="Signature not known here" n={unknownSvcs} total={counts.services} href={href('unknown')} />{/if}
      </div>
    </div>
  {/snippet}
  {#snippet aside()}
    <dl class="facts">
      <dt>Services</dt><dd><span data-nofilter>{formatCount(counts.services)}</span> with a version your environments know</dd>
      <dt>Versions</dt><dd><span data-nofilter>{formatCount(counts.signed)}</span> signed, <span data-nofilter>{formatCount(counts.unsigned)}</span> not signed ({sharePct(counts.unsigned, counts.versions)}){#if counts.unknown}, <span data-nofilter>{formatCount(counts.unknown)}</span> not known here{/if}</dd>
      <dt>Not signed</dt><dd>An entry Stage holds it and Flux never verified a signature on it, so who built it cannot be traced</dd>
      {#if counts.unknown}<dt>Not known here</dt><dd>Only a Stage downstream or one fed by another cluster holds it, so the Stages you can list cannot tell</dd>{/if}
    </dl>
  {/snippet}
  {#snippet caption()}{counts.unsignedServices ? `${countOf(counts.unsignedServices, 'service')} of ${formatCount(counts.services)} ${counts.unsignedServices === 1 ? 'has' : 'have'} a version that is not signed, ${countOf(counts.unsigned, 'version')} in all, so who built ${counts.unsigned === 1 ? 'it' : 'them'} cannot be traced.` : unknownSvcs ? `No version is known to be unsigned; the signature of ${countOf(counts.unknown, 'version')} of ${countOf(unknownSvcs, 'service')} is not known here.` : `Every version of the ${countOf(counts.services, 'service')} is signed.`}{' '}{#if counts.unsigned}<span class="fig-ref">The dashed lower path skips the seal; those versions still reach the registry, unsigned.</span>{/if}{/snippet}
</Figure>
