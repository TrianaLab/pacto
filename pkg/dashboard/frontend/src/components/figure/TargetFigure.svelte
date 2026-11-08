<script>
  // The target page's figure (SPEC 7): "Is this running thing compliant, and on what
  // link?". The target on its plinth, wired to the revision it runs in the style of the
  // link (observed solid, inferred dashed, ambiguous dotted, missing broken), and its
  // compliance as a stamp. An ambiguous or unresolved link names no revision.
  import Figure from './Figure.svelte';
  import Plinth from './Plinth.svelte';
  import { targetModel } from '../../lib/entityFigures.ts';
  import { linkStateLabel } from '../../lib/entityLabels.ts';
  import { statusLabel, statusTone } from '../../lib/format.ts';
  import { hashForHref } from '../../lib/router.ts';
  import { elidable } from '../../lib/elide.ts';

  /** @type {{ target: any, status: string, revisionLabel: string }} */
  let { target, status, revisionLabel } = $props();
  const m = $derived(targetModel(target.linkState));
  const named = $derived(target.linkState === 'exact' || target.linkState === 'inferred' ? target.revision : null);
  const STAMP = { ok: 'ok', warn: 'warn', err: 'err' };
  const stamp = $derived(STAMP[statusTone(status)] ?? 'neutral');
</script>

<Figure id="target" testid="target-figure" question="Is this running thing compliant, and on what link?"
  alt={`${statusLabel(status)}. ${m.words}`} wires={m.wires}>
  {#snippet stage()}
    <div class="tgtfig">
      <div class="tgtfig-p"><Plinth count={1} a="plinth" m="plinth" /><span class="t-meta">this target</span></div>
      <div class="tgtfig-r" data-a="rev">
        {#if named}
          <a class="id mono" href={hashForHref(named.href)} title={revisionLabel}><bdi class:fit={elidable(revisionLabel)}>{revisionLabel}</bdi></a>
          <span class="t-meta">{target.linkState === 'exact' ? 'the revision it runs' : 'the revision it most likely runs'}</span>
        {:else}
          <b>No revision named</b>
          <span class="t-meta">{linkStateLabel(target.linkState)}</span>
        {/if}
      </div>
    </div>
  {/snippet}
  {#snippet aside()}
    <dl class="facts">
      <dt>Compliance</dt><dd><span class="stamp t-{stamp}">{statusLabel(status)}</span></dd>
      <dt>Revision match</dt><dd>{m.words}</dd>
    </dl>
  {/snippet}
  {#snippet caption()}This target is {statusLabel(status).toLowerCase()}. {m.words}{' '}<span class="fig-ref">The wire's style is the link: solid observed, dashed inferred, dotted ambiguous, broken missing.</span>{/snippet}
</Figure>
