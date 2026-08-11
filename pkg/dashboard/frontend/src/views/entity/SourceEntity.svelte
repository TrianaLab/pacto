<script>
  import { formatDate } from '../../lib/dateFormat.ts';
  import { sourceHealthSentence } from '../../lib/entityLabels.ts';
  import { fleetSourcesUrl } from '../../lib/router.ts';
  import PreviewSection from '../../components/PreviewSection.svelte';
  import EntityRefList from '../../components/EntityRefList.svelte';
  import LimitationsList from '../../components/LimitationsList.svelte';

  // The data source page: a product inspector, in the order the questions get asked.
  //
  //   1  what is this source            the header, plus the kind and role below it
  //   2  is IT healthy, and current     a sentence about THIS source, then the times
  //   3  when did it last succeed
  //   4  when was it last observed
  //   5  how much raw knowledge         records it sent
  //   6  what does the product owe it   product entities attributable to it
  //   7  if degraded, what failed       the sanitized source error
  //   8  what is still unknown          its limitations
  //
  // It used to be four facts on one line and two previews. Everything was on screen and
  // nothing was answered: the two counts sat side by side with no hint that they measure
  // different things, the health question was left to a badge in the page header, and
  // the whole page rendered too few titled sections for the shared contents navigator to
  // appear — which parked the body in the 200px rail column and squeezed a page of prose
  // into a gutter.
  //
  // No new visual language: the sections, previews, fact rows and tones are the ones
  // every other entity page uses.
  let { detail } = $props();
  const d = $derived(detail.source ?? {});
  // RECORDS the source sent, and the product ENTITIES attributable to it, are two
  // different measurements of the same source and they legitimately disagree -- a
  // service is derived from revisions and sent by nobody, and a revision two sources
  // both reported is attributable to both. Both totals come from the backend over the
  // complete population; neither is counted off the bounded preview below.
  const records = $derived((d.revisionCount ?? 0) + (d.targetCount ?? 0));
  const contributed = $derived(d.contributed ?? {});
  const plural = (n, one, many) => `${n} ${n === 1 ? one : many}`;
</script>

<div class="source-entity">
  <section class="se-what" id="sec-this-data-source" data-toc="This data source" aria-labelledby="se-what-h">
    <div class="se-head">
      <h2 id="se-what-h" class="t-section-title">This data source</h2>
      <a class="se-viewall" href={fleetSourcesUrl()}>All data sources</a>
    </div>
    <p class="se-lead t-body-2">{sourceHealthSentence(d.health)}</p>

    <dl class="se-facts">
      {#if d.kind}
        <div class="se-fact"><dt class="se-k">Kind</dt><dd class="se-v">{d.kind}</dd></div>
      {/if}
      <div class="se-fact">
        <dt class="se-k">Last successful sync</dt>
        <!-- A source that has NEVER synced and one whose sync time we simply do not
             carry are both "no date", and neither may be drawn as a dash a reader fills
             in as "just now". -->
        <dd class="se-v">{d.lastSuccessfulSync ? formatDate(d.lastSuccessfulSync) : 'Never recorded'}</dd>
      </div>
      <div class="se-fact">
        <dt class="se-k">Last observed</dt>
        <dd class="se-v">{d.observedAt ? formatDate(d.observedAt) : 'Never recorded'}</dd>
      </div>
      <div class="se-fact">
        <dt class="se-k">Source records</dt>
        <dd class="se-v">
          {plural(d.revisionCount ?? 0, 'contract revision', 'contract revisions')}
          · {plural(d.targetCount ?? 0, 'operational target', 'operational targets')}
        </dd>
      </div>
    </dl>
  </section>

  <!-- Level 2, so it is a peer section in the outline and the contents navigator lists
       it. `help` carries the one thing that makes the two numbers on this page make
       sense; without it a reader subtracts one from the other. -->
  <PreviewSection
    title="Product entities contributed"
    total={d.entities?.total ?? 0}
    count={d.entities?.count ?? 0}
    truncated={d.entities?.truncated}
    help="What the product can attribute to this source. It is not the record count above: services are derived from the revisions a source sends rather than sent by anyone, and an entity two sources both reported is attributable to both. So the two numbers are counted differently and are expected to differ."
    empty="Nothing in the product is attributable to this data source."
  >
    <p class="se-breakdown t-body-2">
      {plural(contributed.services ?? 0, 'service', 'services')}
      · {plural(contributed.revisions ?? 0, 'contract revision', 'contract revisions')}
      · {plural(contributed.targets ?? 0, 'operational target', 'operational targets')}
      <span class="se-vs">from {plural(records, 'record', 'records')} it sent</span>
    </p>
    <EntityRefList items={d.entities?.items ?? []} />
  </PreviewSection>

  {#if d.error}
    <!-- The header badge already says THAT the source is degraded and the sentence at the
         top says what that costs; this section is WHY. The human message leads and the
         machine code follows as a small chip, so the first thing read is not a raw enum.
         Never collapsible: a failure a reader has to open is a failure they will miss. -->
    <section class="se-failure" id="sec-reported-failure" data-toc="Reported failure" aria-labelledby="se-fail-h">
      <h2 id="se-fail-h" class="t-section-title">Reported failure</h2>
      <div class="se-error" role="status">
        <span class="se-error-msg">{d.error.message || 'This data source reported an error.'}</span>
        {#if d.error.code}<code class="se-error-code">{d.error.code}</code>{/if}
      </div>
    </section>
  {/if}

  {#if (d.limitations?.count ?? 0) > 0}
    <PreviewSection title="Limitations" tone="warn" collapsible open={false} summary="What Pacto could not determine" total={d.limitations?.total ?? 0} count={d.limitations?.count ?? 0} truncated={d.limitations?.truncated}>
      <LimitationsList items={d.limitations?.items ?? []} />
    </PreviewSection>
  {/if}
</div>

<style>
  .source-entity { display: flex; flex-direction: column; gap: var(--sp-4); }
  /* The same card the owner page's operational summary uses: the lead block of an
     entity page looks the same whatever kind of entity it is. */
  .se-what, .se-failure {
    border: 1px solid var(--c-border); border-radius: var(--radius-md);
    padding: var(--sp-4); background: var(--c-surface);
    display: flex; flex-direction: column; gap: var(--sp-3);
  }
  .se-head { display: flex; align-items: baseline; justify-content: space-between; gap: var(--sp-3); flex-wrap: wrap; }
  .se-head h2, .se-failure h2 { margin: 0; }
  .se-viewall { color: var(--c-accent); text-decoration: none; font-size: var(--text-sm); }
  .se-viewall:hover, .se-viewall:focus-visible { text-decoration: underline; }
  .se-lead { margin: 0; max-width: 80ch; }
  /* A definition list, because that is what these are: the term names the fact and the
     value answers it, and a screen reader reads the pair rather than two loose spans.
     They wrap into columns rather than sitting on one line -- four facts on one row is
     how the last version lost its Records label off the right edge on a phone. */
  .se-facts { margin: 0; display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 220px), 1fr)); gap: var(--sp-3) var(--sp-5); }
  .se-fact { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
  .se-k { font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.04em; color: var(--c-text-3); }
  .se-v { margin: 0; color: var(--c-text); font-size: var(--text-sm); overflow-wrap: anywhere; }
  .se-breakdown { margin: 0 0 var(--sp-3); }
  /* The comparison is the point of the line, so it is on the line -- quietly, because
     the breakdown is the answer and the record count is the thing it is not. */
  .se-vs { color: var(--c-text-3); }
  .se-error {
    display: flex; gap: var(--sp-2); flex-wrap: wrap; align-items: baseline;
    padding: var(--sp-3); border-radius: var(--radius-md); font-size: var(--text-sm);
    background: var(--c-err-bg); border: 1px solid color-mix(in srgb, var(--c-err) 30%, transparent);
  }
  .se-error-msg { color: var(--c-text); }
  .se-error-code {
    font-family: var(--font-mono, monospace); font-size: var(--text-xs); color: var(--c-text-3);
    background: var(--c-surface-inset); border: 1px solid var(--c-border);
    padding: 1px 6px; border-radius: var(--radius-xs); flex-shrink: 0;
  }
</style>
