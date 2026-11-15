<script>
  import { offerLabel, offerRequest, shortDigest } from '../lib/delivery.ts';
  import ProposePanel from './ProposePanel.svelte';
  import UndoPanel from './UndoPanel.svelte';

  // A Stage's offers as buttons, and one ProposePanel or UndoPanel under the Stage it
  // acts on. Offers only describe: the panel plans, then proposes with the caller's own
  // identity, and admission decides. `onlyAvailable` keeps what the caller may already
  // take (an investigation page offers nothing the caller cannot do), `match(row, offer)`
  // keeps the offers a page is about, `correlation` prefills the panel's ticket or
  // incident id and `labelPrefix` ("Plan") words each button as the whole action.
  // `acting` is bindable so one instance per Stage row still opens one panel at a time.
  let { rows = [], service = '', onlyAvailable = false, match = null, correlation = '', labelPrefix = '', acting = $bindable(null) } = $props();

  let opener = null;
  const keyOf = (r) => `${r.namespace}/${r.stage}`;
  const offersOf = (r) => (r.offers ?? []).filter((o) => (!onlyAvailable || o.available) && (!match || match(r, o)));
  // The version a promote names: the candidate's label, else its short digest.
  const versionOf = (r, o) => (o.action === 'promote' ? offerRequest(r, o).version || shortDigest(o.to || r.candidate) : '');
  // When two Stages deliver the same environment, each label names its Stage, so no
  // two buttons read alike (WCAG 2.4.4).
  const shared = (r) => rows.some((x) => x !== r && x.environment === r.environment);
  const stageOf = (r) => (shared(r) ? keyOf(r) : '');
  const label = (r, o) => (labelPrefix
    ? `${labelPrefix} ${offerLabel(o.action).toLowerCase()}${versionOf(r, o) ? ` of ${versionOf(r, o)}` : ''} in ${r.environment}${stageOf(r) ? ` (${stageOf(r)})` : ''}…`
    : `${offerLabel(o.action)} ${service} in ${r.environment}${stageOf(r) ? ` (${stageOf(r)})` : ''}`);
  function act(e, r, o) {
    opener = e.currentTarget;
    acting = { key: keyOf(r), offer: o, ...offerRequest(r, o) };
  }
  function close() { acting = null; opener?.focus(); }
</script>

{#each rows as r (keyOf(r))}
  {@const offers = offersOf(r)}
  {#if offers.length}
    <div class="sd-offers">
      {#each offers as o (o.action + (o.operation ?? ''))}
        <div class="sd-offer">
          {#if labelPrefix}
            <button type="button" class="btn btn-sm" data-testid="service-delivery-offer" disabled={!o.available} title={label(r, o)}
              onclick={(e) => act(e, r, o)}><span class="so-label">{labelPrefix} {offerLabel(o.action).toLowerCase()}{#if versionOf(r, o)}{' '}of <bdi>{versionOf(r, o)}</bdi>{/if} in <bdi>{r.environment}</bdi>{#if stageOf(r)}{' '}(<bdi>{stageOf(r)}</bdi>){/if}…</span></button>
          {:else}
            <button type="button" class="btn btn-sm" data-testid="service-delivery-offer" disabled={!o.available}
              aria-label={label(r, o)} title={label(r, o)} onclick={(e) => act(e, r, o)}>{offerLabel(o.action)}</button>
          {/if}
          {#if !o.available}<span class="t-meta">{offerLabel(o.action)}: {o.reason}</span>{/if}
        </div>
      {/each}
    </div>
  {/if}
  {#if acting?.key === keyOf(r)}
    {#key acting}
      {#if acting.offer.action === 'undo'}
        <UndoPanel namespace={r.namespace} stage={r.stage} name={acting.offer.operation} {correlation} onClose={close} />
      {:else}
        <ProposePanel request={acting.request} toVersion={acting.version} {correlation} onClose={close} />
      {/if}
    {/key}
  {/if}
{/each}

<style>
  .sd-offers { display: flex; flex-wrap: wrap; gap: var(--sp-1) var(--sp-3); }
  .sd-offer { display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--sp-1); min-inline-size: 0; }
  /* .btn is a flex box: one label span keeps the words of the label in one line box. */
  .sd-offer .btn { max-inline-size: 100%; white-space: normal; text-align: start; }
  .so-label { min-inline-size: 0; overflow-wrap: anywhere; }
  @media (pointer: coarse) { .sd-offer .btn { min-block-size: 44px; min-inline-size: 44px; } }
</style>
