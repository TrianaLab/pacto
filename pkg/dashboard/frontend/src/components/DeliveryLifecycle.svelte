<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.ts';
  import { lifecycleWords as W, typeLabel, timeLabel, sentence, andList } from '../lib/delivery.ts';
  import { completenessClass, completenessLabel } from '../lib/format.ts';

  // One artifact's way through a service's Stages: whether its signature was checked,
  // then per Stage each change that moved it there or off it, with its time and
  // whether a signed record says so. Read once, when it is first shown (one walk of
  // the service's Stages and their history). `query` is the lifecycle question:
  // service, domain and one of digest, version or contract.
  let { query } = $props();
  let data = $state(null);
  let error = $state(null);
  onMount(() => { api.deliveryLifecycle(query).then((d) => { data = d; }, (e) => { error = e; }); });
  const SIG_TONE = { signed: 'badge-ok', unsigned: 'badge-warn', unknown: 'badge-neutral' };
</script>

<div class="lc" data-testid="delivery-lifecycle">
  {#if error}
    <p class="t-meta lc-warn" role="alert">Pacto could not read its lifecycle: {error.message || String(error)}</p>
  {:else if !data}
    <p class="t-meta" role="status">Reading its lifecycle</p>
  {:else}
    <p class="t-meta lc-line">
      <span class="badge {SIG_TONE[data.signature] ?? 'badge-neutral'}">{W.signature[data.signature] ?? W.signature.unknown}</span>
      {#if data.signedOn?.length}<span>on {andList(data.signedOn, 3)}</span>{:else if data.unsignedOn?.length}<span>on {andList(data.unsignedOn, 3)}</span>{/if}
      <span class="badge {completenessClass(data.completeness)}">{completenessLabel(data.completeness)}</span>
      <span>As of {timeLabel(data.asOf)}</span>
    </p>
    {#if data.unreadable?.length}<p class="t-meta">Pacto could not list {andList(data.unreadable, 3)}, so a Stage may be missing.</p>{/if}
    {#if data.note}<p class="t-meta">{sentence(data.note)}</p>{/if}
    <ul class="lc-stages">
      {#each data.stages ?? [] as st (st.namespace + '/' + st.stage)}
        <li class="lc-stage">
          <span class="lc-env"><bdi>{st.environment}</bdi> <span class="t-meta"><bdi>{st.namespace}/{st.stage}</bdi></span></span>
          <div class="lc-body">
            <span class="t-meta lc-line">
              {#if st.running}<span>running</span>{/if}
              <span>{W.verified[st.verified] ?? W.verified.unknown}</span>
              {#if W.failed[st.failed]}<span>{W.failed[st.failed]}</span>{/if}
            </span>
            {#if st.events?.length}
              <ol class="lc-events">
                {#each st.events as ev, i (i)}
                  <li class="t-meta lc-line">
                    <span>{W.role[ev.role] ?? ev.role}: {typeLabel(ev.type)}</span>
                    {#if ev.applied || ev.finished}<time datetime={ev.applied || ev.finished}>{timeLabel(ev.applied || ev.finished)}</time>{/if}
                    <!-- A line read from an Operation's status has no record: its time and cause are not signed. -->
                    {#if ev.record}<span>record <bdi class="lc-mono">{ev.record}</bdi></span>{:else}<span class="lc-unsigned">from status, not signed</span>{/if}
                  </li>
                {/each}
              </ol>
            {:else}
              <!-- Never "not reached" past a history Pacto could read only in part. -->
              <span class="t-meta">{st.completeness === 'complete' ? 'not reached' : 'not seen in the history read (partial)'}</span>
            {/if}
          </div>
        </li>
      {/each}
    </ul>
  {/if}
</div>

<style>
  /* One row per Stage: its name beside what happened there, stacked below the container breakpoint (SPEC 24). */
  .lc { container-type: inline-size; display: flex; flex-direction: column; gap: var(--sp-1); min-inline-size: 0; }
  .lc p { margin: 0; }
  .lc-line { display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--sp-1) var(--sp-2); margin: 0; overflow-wrap: anywhere; }
  .lc-warn { color: var(--c-warn); }
  .lc-stages, .lc-events { list-style: none; margin: 0; padding: 0; }
  .lc-stage { display: grid; grid-template-columns: minmax(0, 1fr); gap: var(--sp-1) var(--sp-3); padding: var(--sp-1) 0; border-block-end: 1px solid var(--c-border); min-inline-size: 0; }
  @container (min-width: 40rem) { .lc-stage { grid-template-columns: minmax(12ch, 14rem) minmax(0, 1fr); } }
  .lc-env { font-weight: 600; overflow-wrap: anywhere; min-inline-size: 0; }
  .lc-body { display: flex; flex-direction: column; gap: 2px; min-inline-size: 0; }
  .lc-mono { font-family: var(--font-mono); }
  .lc-unsigned { border: 1px solid var(--c-border); border-radius: var(--radius-sm); padding: 0 var(--sp-1); }
</style>
