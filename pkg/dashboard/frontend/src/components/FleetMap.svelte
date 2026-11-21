<script lang="ts">
  /**
   * The whole fleet on one canvas, from the server's map answer and the node
   * pages of the groups in view. Zooms semantically (Groups, Services, Names,
   * Details), pans and pinches, fits what is shown and respects reduced motion.
   * The canvas is an image to assistive technology; the HUD breadcrumbs, the
   * level ladder and the live region carry its words.
   */
  import { onMount, onDestroy, untrack } from 'svelte';
  import { FleetMapStore, type GroupBy } from '../lib/fleetMap/store.svelte';
  import { LENSES, type Lens } from '../lib/fleetMap/lens';
  import { Grid, circleAt } from '../lib/fleetMap/hit';
  import {
    render, readPalette, placeNodes, drawMinimap,
    type DrawnLabel, type MiniTransform, type Palette, type PlacedNode, type Rect,
  } from '../lib/fleetMap/render';
  import {
    Camera, L1, L2, L3, LEVEL_NAMES, NODE_R, clampK, fitK, fitView, gapOf, kForGap, level, toWorld, viewFor, zoomAt,
    type View, type Viewport,
  } from '../lib/fleetMap/view';
  import type { GroupPage } from '../lib/fleetMap/store.svelte';

  let {
    variant = 'embedded',
    params = {},
    store = new FleetMapStore(),
  }: { variant?: 'embedded' | 'full'; params?: Record<string, string>; store?: FleetMapStore } = $props();

  const GROUPINGS: readonly GroupBy[] = ['domain', 'team', 'environment'];
  const groupBy = $derived<GroupBy>((GROUPINGS as readonly string[]).includes(params.groupBy) ? (params.groupBy as GroupBy) : 'domain');
  const lens = $derived<Lens>((LENSES as readonly string[]).includes(params.lens) ? (params.lens as Lens) : 'both');
  const environment = $derived(params.env ?? '');

  let canvas: HTMLCanvasElement | undefined = $state();
  let mini: HTMLCanvasElement | undefined = $state();
  const camera = new Camera();
  let vp: Viewport = { w: 0, h: 0 };
  let dpr = 1;
  let palette: Palette | null = null;
  let fitted = true;
  let pending = 0;
  let hoverGroup: string | null = null;
  let lastLabels: DrawnLabel[] = [];
  const frames: number[] = [];
  let miniT: MiniTransform | null = null;
  // The nodes last drawn; their hit grid is built on the first hover after a frame.
  let drawn: PlacedNode[] = [];
  let grid: Grid<PlacedNode> | null = null;
  const nodeAt = (wx: number, wy: number, k: number) =>
    (grid ??= new Grid(drawn)).nearest(wx, wy, Math.max(NODE_R * 2.2, 7 / k), (p) => p.vis > 0);

  // What the HUD shows, updated after each frame.
  let lvl = $state(0);
  let centre = $state<string | null>(null);
  let showMini = $state(false);
  let atFit = $state(true);

  const answer = $derived(store.answer);
  const groups = $derived(answer?.groups ?? []);
  const byKey = $derived(new Map(groups.map((g) => [g.key, g])));
  const fitZoom = () => (answer ? fitK(answer.bounds, vp) : 1);

  $effect(() => {
    const g = groupBy, env = environment;
    untrack(() => { void store.load(g, env).then(() => { fit(false); }); });
  });
  $effect(() => {
    // A new answer, a page arriving or a lens switch redraws.
    void store.revision; void answer; void lens;
    request();
  });

  // Placed nodes per page, kept until the page grows or the lens changes.
  const placed = new WeakMap<GroupPage, { n: number; lens: Lens; nodes: PlacedNode[] }>();
  function nodesInView(keys: string[]): PlacedNode[] {
    const out: PlacedNode[] = [];
    for (const key of keys) {
      const g = byKey.get(key);
      const page = store.page(key);
      if (!g || !page) continue;
      let c = placed.get(page);
      if (!c || c.n !== page.nodes.length || c.lens !== lens) { c = { n: page.nodes.length, lens, nodes: placeNodes(g, page, lens) }; placed.set(page, c); }
      out.push(...c.nodes);
    }
    return out;
  }

  function groupsInView(v: View): string[] {
    const [x0, y0] = toWorld(v, 0, 0), [x1, y1] = toWorld(v, vp.w, vp.h);
    return groups.filter((g) => g.x + g.r > x0 && g.x - g.r < x1 && g.y + g.r > y0 && g.y - g.r < y1).map((g) => g.key);
  }

  function centreGroup(v: View): string | null {
    if (v.k < fitZoom() * 1.6) return null;
    const [cx, cy] = toWorld(v, vp.w / 2, vp.h / 2);
    return circleAt(groups, cx, cy)?.label ?? null;
  }

  /** The screen corners the HUD covers. */
  function reserved(): Rect[] {
    const r: Rect[] = [[0, 0, 320, 96], [vp.w - 64, vp.h - 172, vp.w, vp.h]];
    if (showMini) r.push([0, vp.h - 140, 190, vp.h]);
    return r;
  }

  function measure(): boolean {
    if (!canvas) return false;
    const r = canvas.getBoundingClientRect();
    if (!r.width || !r.height) return false;
    if (Math.abs(r.width - vp.w) > 0.5 || Math.abs(r.height - vp.h) > 0.5) {
      const old = vp;
      dpr = Math.min(2, window.devicePixelRatio || 1);
      vp = { w: r.width, h: r.height };
      canvas.width = Math.round(vp.w * dpr);
      canvas.height = Math.round(vp.h * dpr);
      // A fitted view stays fitted when the canvas changes size.
      if (fitted || !old.w) fit(false);
      else camera.set({ ...camera.view, tx: camera.view.tx + (vp.w - old.w) / 2, ty: camera.view.ty + (vp.h - old.h) / 2 });
    }
    return true;
  }

  /** fit frames the whole map in the measured canvas. */
  export function fit(animate = true): void {
    if (!answer || !measure()) return;
    camera.go(fitView(answer.bounds, vp), animate);
    fitted = true;
    request();
  }

  function go(v: View, animate = true) {
    camera.go({ ...v, k: clampK(v.k, fitZoom()) }, animate);
    request();
  }

  function zoomBy(sx: number, sy: number, f: number) {
    fitted = false;
    camera.set(zoomAt(camera.view, sx, sy, f, fitZoom()));
    request();
  }

  function pan(dx: number, dy: number) {
    fitted = false;
    camera.set({ ...camera.view, tx: camera.view.tx + dx, ty: camera.view.ty + dy });
    request();
  }

  function request() {
    if (pending || typeof requestAnimationFrame === 'undefined') return;
    pending = requestAnimationFrame(frame);
  }

  function frame(now: number) {
    pending = 0;
    const moving = camera.step(now);
    draw();
    if (moving) request();
  }

  /** draw renders one frame now. */
  function draw() {
    const ctx = canvas?.getContext('2d');
    if (!ctx || !answer || !measure()) return;
    palette ??= readPalette(canvas);
    const t0 = performance.now();
    const v = camera.view;
    const lv = level(gapOf(v.k));
    const keys = groupsInView(v);
    store.request(lv, keys);
    const nodes = lv >= 1 ? nodesInView(keys) : [];
    drawn = nodes;
    grid = null;
    const res = render(ctx, {
      groups, ribbons: answer.ribbons ?? [], nodes, view: v, w: vp.w, h: vp.h, dpr, palette, lens,
      groupBy: answer.groupBy, hoverGroup, reserved: reserved(),
    });
    lastLabels = res.labels;
    frames.push(performance.now() - t0);
    if (frames.length > 240) frames.shift();
    lvl = lv;
    centre = centreGroup(v);
    atFit = fitted;
    showMini = v.k > fitZoom() * 1.6;
    const mctx = showMini ? mini?.getContext('2d') : null;
    if (mctx && mini) miniT = drawMinimap(mctx, groups, answer.bounds, v, vp.w, vp.h, mini.width, mini.height, palette);
  }

  const point = (e: MouseEvent): [number, number] => {
    const r = canvas!.getBoundingClientRect();
    return [e.clientX - r.left, e.clientY - r.top];
  };

  function onKey(e: KeyboardEvent) {
    const cx = vp.w / 2, cy = vp.h / 2, step = 80;
    if (e.key === '+' || e.key === '=') zoomBy(cx, cy, 1.4);
    else if (e.key === '-' || e.key === '_') zoomBy(cx, cy, 1 / 1.4);
    else if (e.key === '0') fit();
    else if (e.key === 'ArrowLeft') pan(step, 0);
    else if (e.key === 'ArrowRight') pan(-step, 0);
    else if (e.key === 'ArrowUp') pan(0, step);
    else if (e.key === 'ArrowDown') pan(0, -step);
    else return;
    e.preventDefault();
  }

  const ptrs = new Map<number, [number, number]>();
  let drag: { x: number; y: number; tx: number; ty: number; moved: boolean } | null = null;
  let pinch: { d: number; k: number } | null = null;
  let dragging = $state(false);

  function onPointerDown(e: PointerEvent) {
    canvas?.setPointerCapture?.(e.pointerId);
    ptrs.set(e.pointerId, point(e));
    if (ptrs.size === 1) { const [x, y] = point(e); drag = { x, y, tx: camera.view.tx, ty: camera.view.ty, moved: false }; }
    if (ptrs.size === 2) {
      const [p, q] = [...ptrs.values()];
      pinch = { d: Math.hypot(p[0] - q[0], p[1] - q[1]) || 1, k: camera.view.k };
      drag = null;
    }
  }

  function onPointerMove(e: PointerEvent) {
    const [x, y] = point(e);
    if (ptrs.has(e.pointerId)) ptrs.set(e.pointerId, [x, y]);
    if (pinch && ptrs.size === 2) {
      const [p, q] = [...ptrs.values()];
      const d = Math.hypot(p[0] - q[0], p[1] - q[1]);
      zoomBy((p[0] + q[0]) / 2, (p[1] + q[1]) / 2, (pinch.k * d) / pinch.d / camera.view.k);
      return;
    }
    if (drag) {
      const dx = x - drag.x, dy = y - drag.y;
      if (!drag.moved && Math.hypot(dx, dy) > 4) { drag.moved = true; dragging = true; }
      if (drag.moved) { fitted = false; camera.set({ k: camera.view.k, tx: drag.tx + dx, ty: drag.ty + dy }); request(); return; }
    }
    hover(x, y);
  }

  function onPointerEnd(e: PointerEvent) {
    ptrs.delete(e.pointerId);
    if (ptrs.size < 2) pinch = null;
    if (drag && !drag.moved && e.type === 'pointerup') click(...point(e));
    drag = null;
    dragging = false;
  }

  function hover(x: number, y: number) {
    const v = camera.view;
    const [wx, wy] = toWorld(v, x, y);
    const g = lvl === 0 ? circleAt(groups, wx, wy)?.key ?? null : null;
    if (g !== hoverGroup) { hoverGroup = g; request(); }
    const n = nodeAt(wx, wy, v.k);
    if (canvas) canvas.style.cursor = g || n ? 'pointer' : '';
  }

  function click(x: number, y: number) {
    const v = camera.view;
    const [wx, wy] = toWorld(v, x, y);
    const n = nodeAt(wx, wy, v.k);
    if (n) { go(viewFor(vp, n.x, n.y, Math.max(v.k, kForGap(60)))); return; }
    const g = lvl === 0 ? circleAt(groups, wx, wy) : null;
    if (g) { fitted = false; go(viewFor(vp, g.x, g.y, Math.max(Math.min((vp.w - 80) / (2 * g.r), (vp.h - 120) / (2 * g.r)), kForGap(L1 * 1.3)))); }
  }

  function onWheel(e: WheelEvent) {
    e.preventDefault();
    zoomBy(...point(e), Math.exp(-e.deltaY * (e.ctrlKey ? 0.01 : 0.0018)));
  }

  function toLevel(l: number) {
    fitted = false;
    const target = [L1 * 0.6, L1 * 1.4, L2 * 1.4, L3 * 1.2][l];
    const [cx, cy] = toWorld(camera.view, vp.w / 2, vp.h / 2);
    go(viewFor(vp, cx, cy, l === 0 ? Math.min(fitZoom(), kForGap(target)) : kForGap(target)));
  }

  function onMini(e: PointerEvent) {
    if (!miniT || !mini) return;
    const r = mini.getBoundingClientRect();
    go(viewFor(vp, (e.clientX - r.left - miniT.ox) / miniT.s, (e.clientY - r.top - miniT.oy) / miniT.s, camera.view.k));
  }

  const live = $derived(`Level ${lvl}, ${LEVEL_NAMES[lvl]}${centre ? `, ${centre}` : ''}`);

  /** Test hooks: frame times, the labels last drawn and a promise for the end of motion. */
  type Hooks = { bench: () => { frames: number[]; p95: number }; labels: () => DrawnLabel[]; idle: () => Promise<void>; view: () => View & { level: number } };
  const hooks: Hooks = {
    bench: () => {
      const s = [...frames].sort((a, b) => a - b);
      return { frames: [...frames], p95: s.length ? s[Math.min(s.length - 1, Math.floor(s.length * 0.95))] : 0 };
    },
    labels: () => lastLabels.map((l) => ({ ...l })),
    idle: () => new Promise((resolve) => {
      const check = () => (camera.moving || pending || store.loading ? setTimeout(check, 16) : resolve());
      check();
    }),
    view: () => ({ ...camera.view, level: level(gapOf(camera.view.k)) }),
  };

  let observer: ResizeObserver | undefined;
  onMount(() => {
    (window as unknown as { __fleetMap?: Hooks }).__fleetMap = hooks;
    observer = new ResizeObserver(() => { if (measure()) request(); });
    if (canvas) observer.observe(canvas);
    const theme = new MutationObserver(() => { palette = null; request(); });
    theme.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
    return () => theme.disconnect();
  });
  onDestroy(() => {
    observer?.disconnect();
    if (pending && typeof cancelAnimationFrame !== 'undefined') cancelAnimationFrame(pending);
    const w = window as unknown as { __fleetMap?: Hooks };
    if (w.__fleetMap === hooks) delete w.__fleetMap;
  });
</script>

<div class="fm" class:fm-full={variant === 'full'} data-testid="fleet-map">
  <!-- svelte-ignore a11y_no_interactive_element_to_noninteractive_role (the map is an image to assistive technology; it is focusable only for its zoom and pan keys, and its words are the HUD, the ladder and the live region) -->
  <canvas
    bind:this={canvas}
    class:dragging
    tabindex="0"
    role="img"
    aria-roledescription="dependency map"
    aria-label="Dependency map of every service. Plus and minus zoom, arrow keys pan, 0 fits everything."
    onkeydown={onKey}
    onpointerdown={onPointerDown}
    onpointermove={onPointerMove}
    onpointerup={onPointerEnd}
    onpointercancel={onPointerEnd}
    onpointerleave={() => { if (!drag && hoverGroup) { hoverGroup = null; request(); } }}
    onwheel={onWheel}
  ></canvas>
  <div class="fm-hud">
    <nav class="fm-crumbs" aria-label="Where you are">
      {#if atFit && !centre}
        <bdi>All services</bdi>
      {:else}
        <button type="button" onclick={() => fit()}>All services</button>
      {/if}
      {#if centre}<span class="fm-sep" aria-hidden="true">›</span><bdi>{centre}</bdi>{/if}
    </nav>
    <div class="fm-ladder" role="group" aria-label="Level of detail">
      {#each LEVEL_NAMES as name, i (name)}
        <button type="button" aria-current={lvl === i ? 'step' : undefined} onclick={() => toLevel(i)}>{name}</button>
      {/each}
    </div>
  </div>
  <canvas bind:this={mini} class="fm-mini" width="168" height="120" hidden={!showMini} aria-hidden="true" onpointerdown={onMini}></canvas>
  <div class="fm-zoom">
    <button type="button" aria-label="Zoom in" onclick={() => zoomBy(vp.w / 2, vp.h / 2, 1.5)}>+</button>
    <button type="button" aria-label="Zoom out" onclick={() => zoomBy(vp.w / 2, vp.h / 2, 1 / 1.5)}>&minus;</button>
    <button type="button" aria-label="Fit everything" class="fm-fit" onclick={() => fit()}>fit</button>
  </div>
  {#if store.error}
    <p class="fm-error" role="alert">The map could not load: {store.error}</p>
  {/if}
  <div class="visually-hidden" aria-live="polite">{live}</div>
</div>

<style>
  .fm { position: relative; block-size: clamp(420px, calc(100dvh - 300px), 1100px); background: var(--c-surface); border: 1px solid var(--c-border); border-radius: var(--radius-sm); overflow: hidden; }
  .fm.fm-full { block-size: clamp(460px, calc(100dvh - 200px), 1400px); }
  canvas:not(.fm-mini) { display: block; inline-size: 100%; block-size: 100%; touch-action: none; cursor: grab; }
  canvas.dragging { cursor: grabbing; }
  canvas:focus-visible { outline: 2px solid var(--c-accent); outline-offset: -2px; }
  .fm-hud { position: absolute; inset-block-start: var(--sp-3); inset-inline-start: var(--sp-3); display: flex; flex-direction: column; gap: var(--sp-2); pointer-events: none; max-inline-size: calc(100% - 5rem); }
  .fm-hud > * { pointer-events: auto; }
  .fm-crumbs { display: flex; flex-wrap: wrap; align-items: center; gap: 2px var(--sp-2); font-size: var(--text-sm); color: var(--c-text-2); background: color-mix(in srgb, var(--c-surface) 88%, transparent); padding: 2px var(--sp-2); border-radius: var(--radius-xs); align-self: flex-start; }
  .fm-crumbs button { border: 0; background: none; padding: 0; color: var(--c-accent); cursor: pointer; font-size: inherit; }
  .fm-sep { color: var(--c-text-3); }
  .fm-crumbs bdi { overflow-wrap: anywhere; }
  .fm-ladder { display: inline-flex; align-self: flex-start; border: 1px solid var(--c-border); border-radius: var(--radius-pill); overflow: hidden; background: var(--c-surface); font-size: var(--text-xs); }
  .fm-ladder button { border: 0; background: none; padding: 2px 10px; color: var(--c-text-3); cursor: pointer; min-block-size: 26px; }
  .fm-ladder button[aria-current='step'] { color: var(--c-accent); background: var(--c-accent-bg); font-weight: 600; }
  .fm-zoom { position: absolute; inset-block-end: var(--sp-3); inset-inline-end: var(--sp-3); display: flex; flex-direction: column; border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); overflow: hidden; }
  .fm-zoom button { inline-size: 36px; block-size: 36px; border: 0; background: none; color: var(--c-text-2); cursor: pointer; font-size: var(--text-lg); line-height: 1; }
  .fm-zoom button + button { border-block-start: 1px solid var(--c-border); }
  .fm-zoom button:hover { background: var(--c-surface-hover); }
  .fm-zoom .fm-fit { font-size: var(--text-xs); }
  .fm-mini { position: absolute; inset-block-end: var(--sp-3); inset-inline-start: var(--sp-3); inline-size: 10.5rem; block-size: 7.5rem; border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); cursor: pointer; }
  .fm-mini[hidden] { display: none; }
  .fm-error { position: absolute; inset-block-start: 50%; inset-inline: var(--sp-4); margin: 0; text-align: center; color: var(--c-err); }
  @media (pointer: coarse) { .fm-zoom button, .fm-ladder button, .fm-crumbs button { min-inline-size: 44px; min-block-size: 44px; } }
</style>
