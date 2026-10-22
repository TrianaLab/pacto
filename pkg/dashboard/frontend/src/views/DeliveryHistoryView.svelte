<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { deliveryChangeUrl, deliveryHistoryUrl } from '../lib/router.ts';
  import { typeLabel, shortDigest } from '../lib/delivery.ts';
  import PageHeader from '../components/PageHeader.svelte';
  import DeliveryNav from '../components/DeliveryNav.svelte';
  import DeliveryProblem from '../components/DeliveryProblem.svelte';
  import ListingNote from '../components/ListingNote.svelte';
  import EmptyState from '../components/EmptyState.svelte';

  // What happened on each environment: from its signed records where the
  // recorder runs, otherwise from the changes still in the cluster. Every
  // history says where it came from and whether it is the whole story.
  let { namespace = '', stage = '', refreshTick = 0 } = $props();

  const loader = createProductLoader(() => api.deliveryHistory(namespace, stage));
  const question = $derived(`${namespace}/${stage}`);
  $effect(() => { loader.sync(`${question}@@${refreshTick}`, question); });
  onDestroy(() => loader.destroy());

  const data = $derived(loader.dataTag === question ? loader.data : null);
  const histories = $derived(data?.histories ?? []);

  /** Where a history came from, in words. */
  function sourceLabel(h) {
    const from = h.source !== 'records' ? 'From status' : h.verified ? 'From signed records' : 'From records, not verified';
    return `${from}, ${h.completeness === 'complete' ? 'complete' : 'partial'}`;
  }

  /** What a line is: a change, or a record that stands for one. */
  function changeLabel(en) {
    if (en.kind === 'gap') return 'Gap';
    if (en.kind === 'unrecordable') return 'Unrecordable';
    return typeLabel(en.type);
  }
</script>

<div class="product-page">
  <PageHeader
    title={stage ? `History of ${namespace}/${stage}` : 'History'}
    subtitle="What happened on each environment. Where the recorder runs it is read from signed records; elsewhere from the changes still in the cluster, which is never the whole story."
    asOf={data?.asOf}
    revalidating={!!data && loader.loading}
  />
  <DeliveryNav current="delivery-history" />
  {#if stage}<p class="t-body-2"><a class="hv-link" href={deliveryHistoryUrl()}>Show every environment</a></p>{/if}

  {#if !data && loader.loading}
    <EmptyState loading={true} rows={4} />
  {:else if !data && loader.error}
    <DeliveryProblem error={loader.error} onRetry={() => loader.refresh()} />
  {:else if data}
    <ListingNote listing={data} error={loader.error} onRetry={() => loader.refresh()} />
    {#if histories.length === 0}
      <div class="state-box"><h2>No history yet</h2><p>No environment you can see has a change on record.</p></div>
    {:else}
      {#each histories as h (h.namespace + '/' + h.stage.name)}
        <section class="hv-stage" aria-label={`History of ${h.namespace}/${h.stage.name}`}>
          <h2 class="t-section-title">{h.namespace}/{h.stage.name}</h2>
          <p class="t-body-2">
            <span class="hv-source" class:hv-partial={h.completeness !== 'complete'} data-testid="history-source">{sourceLabel(h)}</span>
            Runs <code title={h.stage.pointer}>{shortDigest(h.stage.pointer) || 'nothing yet'}</code>{#if h.stage.frozen}, paused{/if}. {h.note}
          </p>
          {#if (h.problems ?? []).length}
            <div class="hv-notes" data-testid="history-problems">
              <h3 class="t-body-2">What the records show is wrong</h3>
              <ul>{#each h.problems as p (p)}<li>{p}</li>{/each}</ul>
            </div>
          {/if}
          {#if (h.unreadable ?? []).length}
            <div class="hv-notes" data-testid="history-unreadable">
              <h3 class="t-body-2">What could not be read or verified</h3>
              <ul>{#each h.unreadable as u (u)}<li>{u}</li>{/each}</ul>
            </div>
          {/if}
          {#if (h.entries ?? []).length === 0}
            <p class="t-body-2 hv-none">No change kept for this environment.</p>
          {:else}
            <div class="hv-scroll">
              <table class="hv-table">
                <thead><tr><th scope="col">#</th><th scope="col">Change</th><th scope="col">Version</th><th scope="col">By</th><th scope="col">How it ended</th><th scope="col">When</th><th scope="col">Record</th></tr></thead>
                <tbody>
                  {#each h.entries as en (`${en.seq ?? ''}/${en.record ?? ''}/${en.operation}`)}
                    <tr>
                      <td>{en.seq ?? ''}</td>
                      <td>{#if en.operation}<a href={deliveryChangeUrl(h.namespace, en.operation)}>{changeLabel(en)}</a>{:else}{changeLabel(en)}{/if}</td>
                      <td><code>{shortDigest(en.from) || '–'} → {shortDigest(en.to) || '–'}</code></td>
                      <td>{en.actor || '–'}{#if en.actorClass} ({en.actorClass}){/if}</td>
                      <td>{en.outcome || en.phase}{#if en.verification}, {en.verification}{/if}{#if en.reason}: {en.reason}{/if}</td>
                      <td>{#if en.finished}<time datetime={en.finished}>{en.finished}</time>{/if}</td>
                      <td>{#if en.record}<code title={en.record}>{shortDigest(en.record)}</code>{:else if en.unverifiedRecord}<span title={en.unverifiedRecord}>Recorded, not verified</span>{:else if h.source === 'records'}Not recorded yet{:else}–{/if}</td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          {/if}
        </section>
      {/each}
    {/if}
  {/if}
</div>

<style>
  .hv-link { color: var(--c-accent); text-decoration: underline; }
  .hv-stage { display: flex; flex-direction: column; gap: var(--sp-2); }
  .hv-stage p { margin: 0; }
  .hv-source { font-weight: 600; color: var(--c-text-2); margin-right: var(--sp-2); }
  .hv-source.hv-partial { color: var(--c-text-3); }
  .hv-notes h3 { margin: 0; font-weight: 600; }
  .hv-notes ul { margin: var(--sp-1) 0 0; padding-left: var(--sp-5); overflow-wrap: anywhere; }
  .hv-scroll { overflow-x: auto; }
  .hv-table { border-collapse: collapse; width: 100%; font-size: var(--text-sm); }
  .hv-table th, .hv-table td { border-bottom: 1px solid var(--c-border); padding: var(--sp-2) var(--sp-3); text-align: left; vertical-align: top; }
  .hv-table th { color: var(--c-text-3); font-weight: 600; }
  .hv-table a { color: var(--c-accent); }
  .hv-none { color: var(--c-text-3); }
  .state-box { display: flex; flex-direction: column; align-items: center; gap: var(--sp-2); padding: var(--sp-8) var(--sp-4); text-align: center; color: var(--c-text-3); }
  .state-box h2 { color: var(--c-text-2); }
</style>
