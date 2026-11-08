<script>
  // Figure (SPEC 3): a <figure> whose question heads it and whose <figcaption> answers it
  // in words. It owns the one layout pass (C3): the width class (S1), the lanes' chip
  // folding, identity elision and the wires, drawn at 1 SVG unit = 1 CSS px between the
  // anchors the HTML grid placed. Marks never carry words; the stage, aside and foot are
  // HTML.
  //
  // It also owns the narration (SPEC 6). The rendered DOM is the final frame: a
  // narration only adds `narr:` animations that fill backwards from hidden, so a figure
  // that does not play is complete from frame 1. It autoplays only when the caller says
  // the reader asked (`autoplay`), once per session per answer key, in view, with the tab
  // visible, and never under reduced motion or html[data-narrate="final"]. Reduced motion
  // gets a step-through (Previous / Next) whose last step is the same DOM.
  import { onMount, onDestroy, tick, untrack } from 'svelte';
  import { widthClass, CHIP_CAP } from '../../lib/figure.ts';
  import { layoutLanes, wirePaths } from '../../lib/figureLayout.ts';
  import { fitIdents } from '../../lib/elide.ts';
  import { play, resolve, claimAutoplay, finalMode, sessionKey } from '../../lib/narrate.ts';
  import { prefersReducedMotion } from '../../lib/motion.ts';
  import Wire from './Wire.svelte';

  /** @type {{ id: string, question: string, kind?: 'data' | 'explain', alt?: string, wires?: import('../../lib/figure.ts').WireDef[], steps?: import('../../lib/narrate.ts').Step[], autoplay?: boolean, autoKey?: unknown, testid?: string, stage: import('svelte').Snippet, aside?: import('svelte').Snippet | null, foot?: import('svelte').Snippet | null, caption: import('svelte').Snippet }} */
  let { id, question, kind = 'data', alt = '', wires = [], steps = [], autoplay = false, autoKey = null, testid = undefined, stage, aside = null, foot = null, caption } = $props();

  /** @type {HTMLElement} */ let el;
  /** @type {HTMLElement} */ let stageEl;
  /** @type {SVGGElement} */ let svgEl;
  let w = $state(/** @type {import('../../lib/figure.ts').WidthClass} */ ('wide'));
  let paths = $state(/** @type {import('../../lib/figureLayout.ts').DrawnWire[]} */ ([]));
  let narration = $state('done');
  const reduced = prefersReducedMotion();
  // The step-through starts at its last step: the final frame.
  let k = $state(untrack(() => (reduced ? steps.length : 0)));
  /** @type {Animation[]} */ let anims = [];
  /** @type {ResizeObserver | undefined} */ let ro;
  /** @type {IntersectionObserver | undefined} */ let io;
  let first = true;

  async function layout() {
    if (!el?.isConnected) return;
    w = widthClass(el.clientWidth);
    await tick();
    if (!stageEl?.isConnected) return;
    layoutLanes(stageEl, CHIP_CAP[w]);
    fitIdents(el); // the stage's names, and the aside's and foot's
    paths = wirePaths(stageEl, wires, w);
    if (first) { first = false; await tick(); if (stageEl?.isConnected) start(); }
  }

  function ring(on) {
    for (const r of stageEl.querySelectorAll('[data-ring]')) r.classList.toggle('ring-on', on);
  }

  function start() {
    if (!steps.length) { ring(true); return; }
    if (reduced) { setStep(steps.length); return; }
    if (!autoplay || autoKey == null || finalMode() || !claimAutoplay(sessionKey(autoKey))) { ring(true); return; }
    // Pending shows the final frame until the figure is in view: nothing waits at opacity 0.
    narration = 'pending';
    ring(true);
    if (typeof IntersectionObserver === 'undefined') { narration = 'done'; return; }
    io = new IntersectionObserver((es) => {
      if (es.some((e) => e.isIntersecting) && document.visibilityState === 'visible' && narration === 'pending') { io?.disconnect(); run(); }
    }, { threshold: 0.35 });
    io.observe(el);
  }

  function run() {
    if (finalMode()) return;
    for (const a of anims) a.cancel();
    ring(false);
    anims = play(stageEl, svgEl, steps, id);
    if (!anims.length) { done(); return; }
    narration = 'playing';
    const mine = anims;
    Promise.all(mine.map((a) => a.finished)).then(() => { if (anims === mine) done(); }, () => {});
  }

  function done() { narration = 'done'; ring(true); }
  function skip() { for (const a of anims) a.finish(); done(); }

  // Reduced motion: discrete steps at duration 0; step n is the final frame.
  function setStep(/** @type {number} */ to) {
    k = Math.max(1, Math.min(steps.length, to));
    steps.forEach((s, i) => {
      for (const sel of s.show ?? []) for (const t of resolve(stageEl, svgEl, sel)) t.classList.toggle('nar-hid', i >= k);
    });
    ring(k === steps.length);
  }

  /** @param {Event} e */
  function interrupt(e) {
    if (narration === 'playing' && !(/** @type {Element} */ (e.target)).closest?.('.fig-ctl')) skip();
  }
  function onVisibility() { if (document.visibilityState !== 'visible' && narration === 'playing') skip(); }

  onMount(() => {
    if (typeof ResizeObserver !== 'undefined') { ro = new ResizeObserver(() => layout()); ro.observe(el); }
    document.addEventListener('visibilitychange', onVisibility);
    for (const t of ['pointerdown', 'keydown']) el.addEventListener(t, interrupt);
    el.addEventListener('wheel', interrupt, { passive: true });
    document.fonts?.ready.then(() => layout());
    layout();
  });
  onDestroy(() => {
    ro?.disconnect();
    io?.disconnect();
    for (const a of anims) a.finish();
    if (typeof document !== 'undefined') document.removeEventListener('visibilitychange', onVisibility);
  });
</script>

<figure class="fig fig--{kind} iso-backdrop" id="fig-{id}" data-fig data-w={w} data-narration={narration} data-testid={testid}
  aria-labelledby="fig-{id}-q" bind:this={el}>
  <div class="fig-head">
    <span class="fig-kind">{kind === 'explain' ? 'How this works' : 'Data figure'}</span>
    <p class="fig-q" id="fig-{id}-q">{question}</p>
    {#if steps.length}
      <div class="fig-ctl">
        {#if reduced}
          <button type="button" class="btn btn-sm" disabled={k <= 1} onclick={() => setStep(k - 1)}>Previous</button>
          <span class="t-meta" data-nofilter>Step {k} of {steps.length}</span>
          <button type="button" class="btn btn-sm" disabled={k >= steps.length} onclick={() => setStep(k + 1)}>Next</button>
        {:else}
          <button type="button" class="btn btn-sm" onclick={run}>Replay</button>
          <button type="button" class="btn btn-sm" disabled={narration !== 'playing'} onclick={skip}>Skip to final frame</button>
        {/if}
      </div>
    {/if}
  </div>
  <div class="fig-body" class:has-aside={!!aside}>
    <div class="fig-stage" bind:this={stageEl}>
      <svg class="fig-wires" role="img" aria-label={alt} focusable="false">
        <g bind:this={svgEl}>
          {#each paths as p (p.id)}<Wire w={p} />{/each}
        </g>
      </svg>
      {@render stage()}
    </div>
    {#if aside}<div class="fig-aside">{@render aside()}</div>{/if}
  </div>
  {#if steps.length}
    <ol class="visually-hidden" aria-label="What the drawing shows, in order">
      {#each steps as s, i (i)}<li aria-current={reduced && i === k - 1 ? 'step' : undefined}>{s.cap}</li>{/each}
    </ol>
    {#if reduced && k}<p class="step-now t-meta">{steps[k - 1].cap}</p>{/if}
  {/if}
  {#if foot}<div class="fig-foot">{@render foot()}</div>{/if}
  <figcaption>{@render caption()}</figcaption>
</figure>
