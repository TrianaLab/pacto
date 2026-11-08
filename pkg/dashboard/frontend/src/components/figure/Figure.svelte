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
  import { play, resolve, claimAutoplay, finalMode, sessionKey, tokenOffsets } from '../../lib/narrate.ts';
  import { prefersReducedMotion } from '../../lib/motion.ts';
  import Wire from './Wire.svelte';

  // A rail walk (`walk`) takes one step per station reached (A1b) and may carry a token
  // that travels them (`token`). `rowMin` is the width under which a row of steps turns
  // vertical (S1 r3, data-minw): each step keeps at least 150px. `compact` offers the
  // L3 (r5) Compact toggle of a list view's figure: it folds the drawing away, keeps
  // the aside and caption, and is remembered per figure.
  /** @type {{ id: string, question: string, kind?: 'data' | 'explain', alt?: string, wires?: import('../../lib/figure.ts').WireDef[], steps?: import('../../lib/narrate.ts').Step[], walk?: boolean, token?: import('../../lib/narrate.ts').TokenWalk | null, rowMin?: number, autoplay?: boolean, autoKey?: unknown, compact?: boolean, testid?: string, stage: import('svelte').Snippet, aside?: import('svelte').Snippet | null, foot?: import('svelte').Snippet | null, caption: import('svelte').Snippet }} */
  let { id, question, kind = 'data', alt = '', wires = [], steps = [], walk = false, token = null, rowMin = 0, autoplay = false, autoKey = null, compact = false, testid = undefined, stage, aside = null, foot = null, caption } = $props();

  const COMPACT = 'pacto-fig-compact';
  /** @returns {string[]} */
  function compactIds() {
    try { return JSON.parse(localStorage.getItem(COMPACT) || '[]'); } catch { return []; }
  }
  let folded = $state(untrack(() => compact && compactIds().includes(id)));
  async function toggleCompact() {
    folded = !folded;
    if (folded) { io?.disconnect(); skip(); }
    // localStorage may be unavailable (private mode); the figure still folds.
    try { localStorage.setItem(COMPACT, JSON.stringify([...compactIds().filter((x) => x !== id), ...(folded ? [id] : [])])); } catch { /* not remembered */ }
    await tick();
    layout();
  }

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
    w = el.clientWidth < rowMin ? 'col' : widthClass(el.clientWidth);
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
    // A folded figure plays nothing, and keeps its once-per-session narration for later.
    if (!steps.length || folded) { ring(true); return; }
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
    anims = play(stageEl, svgEl, steps, id, { walk, token });
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
    const tok = token && stageEl.querySelector(token.sel);
    const slots = token ? token.slots.map((sel) => stageEl.querySelector(sel)) : [];
    if (tok instanceof HTMLElement || tok instanceof SVGElement) {
      // At its last step the token is where it is rendered: no style left behind, so the
      // final step is the final frame's DOM (G2).
      const at = slots.every(Boolean) ? tokenOffsets(slots.map((x) => x.getBoundingClientRect()))[k - 1] : 'none';
      if (at && at !== 'none') tok.style.transform = at;
      else { tok.style.removeProperty('transform'); if (!tok.getAttribute('style')) tok.removeAttribute('style'); }
    }
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

<figure class="fig fig--{kind} iso-backdrop" id="fig-{id}" data-fig data-w={w} data-narration={narration} data-compact={folded || undefined} data-testid={testid}
  aria-labelledby="fig-{id}-q" bind:this={el}>
  <div class="fig-head">
    <span class="fig-kind">{kind === 'explain' ? 'How this works' : 'Data figure'}</span>
    <p class="fig-q" id="fig-{id}-q">{question}</p>
    {#if steps.length || compact}
      <div class="fig-ctl">
        {#if compact}<button type="button" class="btn btn-sm" aria-pressed={folded} onclick={toggleCompact}>Compact</button>{/if}
        {#if !steps.length || folded}
          <!-- a folded figure has no narration to drive -->
        {:else if reduced}
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
  {#if steps.length && !folded}
    <ol class="visually-hidden" aria-label="What the drawing shows, in order">
      {#each steps as s, i (i)}<li aria-current={reduced && i === k - 1 ? 'step' : undefined}>{s.cap}</li>{/each}
    </ol>
    {#if reduced && k}<p class="step-now t-meta">{steps[k - 1].cap}</p>{/if}
  {/if}
  {#if foot}<div class="fig-foot">{@render foot()}</div>{/if}
  <figcaption>{@render caption()}</figcaption>
</figure>
