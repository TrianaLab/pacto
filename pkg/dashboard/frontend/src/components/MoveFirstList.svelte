<script>
  import { deliveryHistoryUrl } from '../lib/router.ts';
  import { shortDigest } from '../lib/delivery.ts';
  import { formatCount } from '../lib/format.ts';

  // What must move before a held promote can ship, in one line: each consumer
  // whose declared range does not accept the new version, linked to its Stage,
  // and the verified version upstream that can go first, when there is one.
  // Pacto only says it; a person or an agent moves the other service. Built
  // from the plan's fields: the plan's own message repeats them with digests.
  let { order = null, testid = 'move-first', max = 3 } = $props();

  const moveFirst = $derived(order?.moveFirst ?? []);
  const shown = $derived(moveFirst.slice(0, max));
  const rest = $derived(moveFirst.length - shown.length);
  const unknown = $derived(order?.unknown ?? []);
  const stageUrl = (s) => deliveryHistoryUrl({ namespace: s.namespace, stage: s.name });
  const sep = (i, n) => (i === 0 ? '' : i === n - 1 ? ' and ' : ', ');
</script>

{#if moveFirst.length}
  <p class="mf t-body-2" data-testid={testid}>
    <strong>Safer:</strong> update
    {#each shown as m, i (m.service + i)}{sep(i, shown.length + (rest > 0 ? 1 : 0))}<span class="mf-item">{#if m.stage}<a href={stageUrl(m.stage)} aria-label={`${m.service}, Stage ${m.stage.namespace}/${m.stage.name}`} title={m.accepts ? `It accepts only ${m.accepts}` : undefined}>{m.service}</a>{:else}{m.service}{/if}{#if m.candidate}{' '}(<a href={stageUrl(m.candidate.verifiedOn)}>{m.service} {m.candidate.version || shortDigest(m.candidate.digest)}, verified on {m.candidate.verifiedOn.namespace}/{m.candidate.verifiedOn.name}</a>, can go now){:else if m.unknown}{' '}(Pacto cannot tell what moves it: {m.unknown}){:else}{' '}(no verified version can go yet){/if}</span>{/each}{#if rest > 0}{' and '}{formatCount(rest)} more{/if} first.
    {#if unknown.length}<span class="mf-unknown" data-testid="{testid}-unknown">This list may be incomplete: {unknown[0]}{#if unknown.length > 1}{' '}and {formatCount(unknown.length - 1)} more{/if}.</span>{/if}
  </p>
{/if}

<style>
  .mf { margin: 0; overflow-wrap: break-word; }
  .mf-unknown { color: var(--c-text-3); }
  .mf a { color: var(--c-accent); text-decoration: underline; }
</style>
