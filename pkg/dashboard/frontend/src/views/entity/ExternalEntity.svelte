<script>
  import PreviewSection from '../../components/PreviewSection.svelte';
  import EntityLink from '../../components/EntityLink.svelte';
  import ExternalProvenance from '../../components/ExternalProvenance.svelte';
  import { shortDigest } from '../../lib/format.ts';

  // An external system's page: a system no Pacto contract describes, keyed by its
  // canonical identity. Its class and catalog first, the services that declare, call or use it, then
  // where each source recognized it. Pacto never observes an external's health, so the
  // page states none.
  let { detail } = $props();
  const d = $derived(detail.external ?? {});
  const rec = $derived(d.external ?? {});
  const CLASS_WORDS = { 'vendor-api': 'Vendor API', 'managed-cloud': 'Managed cloud', legacy: 'Legacy', unclassified: 'Unclassified', 'other-egress': 'Other egress' };
  const catalogLine = $derived.by(() => {
    const m = rec.catalog;
    if (!m) return 'Joined by name only: consumers that spell it differently do not join.';
    const c = d.catalog;
    const sig = c ? `, signature ${c.signature}${c.reason ? ` (${c.reason})` : ''}` : '';
    return `${m.origin === 'default' ? 'Default' : 'Organization'} catalog ${shortDigest(m.digest, 12)}, entry ${m.entry}${sig}`;
  });
  const consumers = $derived(d.consumers ?? []);
  // Required and optional describe a declaration; a consumer no contract
  // declares says how Pacto knows it instead.
  const consumerUse = (c) => c.provenance === 'observed' ? 'observed only'
    : c.provenance === 'inferred' ? 'inferred only'
    : `${c.required ? 'required' : 'optional'}${c.versions?.length ? `, ${c.versions.join(', ')}` : ''}`;
</script>

<div class="external-entity">
  <section class="xe-summary" id="sec-external-summary" data-toc="Summary" aria-labelledby="xe-summary-h">
    <h2 id="xe-summary-h" class="t-section-title">Summary</h2>
    <p class="t-body-2 xe-line" data-testid="external-class">{CLASS_WORDS[rec.class] ?? rec.class}{rec.folded ? `, folds ${rec.folded} external systems past the cap` : ''}{d.foldedInto ? `, shown under ${d.foldedInto} past the cap` : ''}</p>
    <p class="t-meta xe-line" data-testid="external-catalog" title={rec.catalog?.digest || undefined}>{catalogLine}</p>
  </section>

  <PreviewSection title="Consumers" total={rec.consumersTotal ?? consumers.length} count={consumers.length} empty="No service you can see declares, calls or uses it.">
    <ul class="xe-consumers">
      {#each consumers as c (c.service.key)}
        <li class="xe-consumer">
          <EntityLink ref={c.service} showKind={false} />
          <span class="t-meta" data-testid="external-consumer-use">{consumerUse(c)}</span>
        </li>
      {/each}
    </ul>
  </PreviewSection>

  <ExternalProvenance data={d} />
</div>

<style>
  .external-entity { display: flex; flex-direction: column; gap: var(--sp-4); }
  .xe-summary { border: 1px solid var(--c-border); border-radius: var(--radius-md); padding: var(--sp-4); background: var(--c-surface); display: flex; flex-direction: column; gap: var(--sp-2); }
  .xe-summary h2, .xe-line { margin: 0; overflow-wrap: anywhere; }
  .xe-consumers { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-1); }
  .xe-consumer { display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--sp-1) var(--sp-2); min-inline-size: 0; overflow-wrap: anywhere; }
</style>
