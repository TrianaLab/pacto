<script lang="ts">
  /**
   * The whole fleet on one canvas, from the server's map answer and the node
   * pages of the groups in view. Zooms semantically (Groups, Services, Names,
   * Details), pans and pinches, fits what is shown and respects reduced motion.
   * The canvas is an image to assistive technology; the HUD breadcrumbs, the
   * level ladder and the live region carry its words.
   *
   * The toolbar groups by domain, owner team or environment, switches the
   * lens (Both, Expected, Observed) and the Only problems mode, and picks an
   * environment to dim what does not run there. The map's state lives in the
   * hash query (groupBy, lens, only, gaps, missing, env): every change is
   * written through onparams, so a view is shareable.
   */
  import { onMount, onDestroy, untrack } from 'svelte';
  import { FleetMapStore, type GroupBy } from '../lib/fleetMap/store.svelte';
  import { LENSES, groupCount, summary, type Lens } from '../lib/fleetMap/lens';
  import { GAP_KINDS, MISSING_SECTIONS, environmentChoices, gapCount, modeFrom, modeParams, toggleGap, type GapKind, type Mode } from '../lib/fleetMap/modes';
  import { attentionCategoryLabel } from '../lib/entityLabels';
  import { countOf, formatCount } from '../lib/format';
  import { setHashParams } from '../lib/hashQuery.svelte';
  import { Grid, circleAt } from '../lib/fleetMap/hit';
  import {
    render, readPalette, placeNodes, placeEdges, drawMinimap, LEGEND, EDGE_DASH,
    type DrawnLabel, type LegendEntry, type MiniTransform, type Palette, type PlacedEdge, type PlacedNode, type Rect,
  } from '../lib/fleetMap/render';
  import {
    Camera, L1, L2, L3, LEVEL_NAMES, NODE_R, clampK, fitK, fitView, gapOf, kForGap, level, toWorld, viewFor, zoomAt,
    type View, type Viewport,
  } from '../lib/fleetMap/view';
  import type { GroupPage } from '../lib/fleetMap/store.svelte';

  type Patch = Record<string, string | null>;
  let {
    variant = 'embedded',
    params = {},
    store = new FleetMapStore(),
    onparams = setHashParams,
  }: { variant?: 'embedded' | 'full'; params?: Record<string, string>; store?: FleetMapStore; onparams?: (patch: Patch) => void } = $props();

  const GROUPINGS: readonly GroupBy[] = ['domain', 'team', 'environment'];
  const GROUP_WORDS: Record<GroupBy, string> = { domain: 'Domain', team: 'Owner team', environment: 'Environment' };
  const LENS_WORDS: Record<Lens, string> = { both: 'Both', expected: 'Expected', observed: 'Observed' };
  const GAP_WORDS: Record<GapKind, [string, readonly number[], string]> = {
    'expected-only': ['Expected, not observed', EDGE_DASH.expectedOnly, '--c-info'],
    'observed-only': ['Observed, not expected', EDGE_DASH.observedOnly, '--c-warn'],
    insufficient: ['Insufficient evidence', EDGE_DASH.insufficient, '--c-neutral'],
  };
  // The view's state: the hash query, overridden by the toolbar until the page passes new params.
  let q = $derived<Record<string, string>>({ ...params });
  const groupBy = $derived<GroupBy>((GROUPINGS as readonly string[]).includes(q.groupBy) ? (q.groupBy as GroupBy) : 'domain');
  const lens = $derived<Lens>((LENSES as readonly string[]).includes(q.lens) ? (q.lens as Lens) : 'both');
  const mode = $derived<Mode>(modeFrom(q));
  // Gaps are differences between declared and observed: they read in Both.
  const shown = $derived<Lens>(mode?.kind === 'gaps' ? 'both' : lens);
  // Grouped by environment, the columns are the environments: the picker steps aside.
  const environment = $derived(groupBy === 'environment' ? '' : q.env ?? '');

  /** set changes the view's state and writes it to the hash query. */
  function set(patch: Patch) {
    const next = { ...q };
    for (const [k, v] of Object.entries(patch)) { if (v === null || v === '') delete next[k]; else next[k] = v; }
    q = next;
    onparams(patch);
  }
  const setMode = (m: Mode) => set(modeParams(m));

  let canvas: HTMLCanvasElement | undefined = $state();
  let mini: HTMLCanvasElement | undefined = $state();
  const camera = new Camera();
  let vp: Viewport = { w: 0, h: 0 };
  let dpr = 1;
  let palette: Palette | null = null;
  let fitted = true;
  let pending = 0;
  let hoverGroup: string | null = null;
  let hoverNode: string | null = null;
  // The group tooltip, pinned where the pointer entered the bubble so the reader can reach its action.
  let tip = $state<{ key: string; x: number; y: number } | null>(null);
  let tipEl: HTMLDivElement | undefined = $state();
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
    // A new answer, a page arriving, a lens, mode or environment switch redraws.
    void store.revision; void answer; void shown; void mode; void environment;
    request();
  });

  // Placed nodes and edges per page, kept until the page grows or the lens changes.
  const placed = new WeakMap<GroupPage, { n: number; lens: Lens; nodes: PlacedNode[]; edges: PlacedEdge[] }>();
  function inView(keys: string[]): { nodes: PlacedNode[]; edges: PlacedEdge[] } {
    const nodes: PlacedNode[] = [], edges: PlacedEdge[] = [];
    for (const key of keys) {
      const g = byKey.get(key);
      const page = store.page(key);
      if (!g || !page) continue;
      let c = placed.get(page);
      if (!c || c.n !== page.nodes.length || c.lens !== shown) {
        const ns = placeNodes(g, page, shown);
        c = { n: page.nodes.length, lens: shown, nodes: ns, edges: placeEdges(page, ns) };
        placed.set(page, c);
      }
      nodes.push(...c.nodes);
      edges.push(...c.edges);
    }
    return { nodes, edges };
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
    const { nodes, edges } = lv >= 1 ? inView(keys) : { nodes: [], edges: [] };
    drawn = nodes;
    grid = null;
    const res = render(ctx, {
      groups, ribbons: answer.ribbons ?? [], nodes, edges, view: v, w: vp.w, h: vp.h, dpr, palette, lens: shown,
      groupBy: answer.groupBy, mode, environment, hoverGroup, hoverNode, reserved: reserved(),
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
    else if (e.key === 'Escape' && mode) setMode(null);
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
    if (g !== hoverGroup) { hoverGroup = g; tip = g ? { key: g, x: Math.max(8, Math.min(x + 12, vp.w - 268)), y: Math.max(8, Math.min(y + 12, vp.h - 180)) } : null; request(); }
    const n = lvl >= 1 ? nodeAt(wx, wy, v.k) : null;
    const nk = n?.node.key ?? null;
    if (nk !== hoverNode) { hoverNode = nk; request(); }
    if (canvas) canvas.style.cursor = g || n ? 'pointer' : '';
  }

  function unhover(e: PointerEvent) {
    if (drag || (tipEl && e.relatedTarget instanceof Node && tipEl.contains(e.relatedTarget))) return;
    if (hoverGroup || hoverNode) { hoverGroup = null; hoverNode = null; request(); }
    tip = null;
  }

  /** zoomToGroup frames one group. */
  function zoomToGroup(g: { x: number; y: number; r: number }) {
    fitted = false;
    go(viewFor(vp, g.x, g.y, Math.max(Math.min((vp.w - 80) / (2 * g.r), (vp.h - 120) / (2 * g.r)), kForGap(L1 * 1.3))));
  }

  /** missingIn dims the complete services and frames the group. */
  function missingIn(key: string) {
    tip = null;
    setMode({ kind: 'missing', section: 'any' });
    const g = byKey.get(key);
    if (g) zoomToGroup(g);
  }

  function click(x: number, y: number) {
    const v = camera.view;
    const [wx, wy] = toWorld(v, x, y);
    const n = nodeAt(wx, wy, v.k);
    if (n) { go(viewFor(vp, n.x, n.y, Math.max(v.k, kForGap(60)))); return; }
    const g = lvl === 0 ? circleAt(groups, wx, wy) : null;
    if (g) { tip = null; zoomToGroup(g); }
  }

  function onWheel(e: WheelEvent) {
    e.preventDefault();
    tip = null;
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

  const choices = $derived(environmentChoices(store.environments));
  const tipGroup = $derived(tip ? byKey.get(tip.key) ?? null : null);
  /** Legend tones as design tokens. */
  const TONE: Partial<Record<keyof Palette, string>> = { text2: '--c-text-2', text3: '--c-text-3', err: '--c-err', warn: '--c-warn', info: '--c-info', neutral: '--c-neutral' };
  const tone = (e: LegendEntry) => `var(${TONE[e.tone] ?? '--c-text-3'})`;
  const dashOf = (d: readonly number[] | undefined) => (d?.length ? d.join(' ') : undefined);

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

<section class="fmw" aria-label="Dependency map">
<div class="fm-tools">
  <div class="fm-seg" role="group" aria-label="Group by">
    <span class="fm-seg-label" aria-hidden="true">Group by</span>
    {#each GROUPINGS as g (g)}
      <button type="button" aria-pressed={groupBy === g} onclick={() => set({ groupBy: g === 'domain' ? null : g })}>{GROUP_WORDS[g]}</button>
    {/each}
  </div>
  <div class="fm-seg" role="group" aria-label="Show">
    <span class="fm-seg-label" aria-hidden="true">Show</span>
    {#each LENSES as l (l)}
      <!-- A lens other than Both leaves gaps mode: gaps read in Both. -->
      <button type="button" aria-pressed={shown === l} onclick={() => set({ lens: l === 'both' ? null : l, ...(mode?.kind === 'gaps' && l !== 'both' ? modeParams(null) : {}) })}>{LENS_WORDS[l]}</button>
    {/each}
  </div>
  <button type="button" class="fm-toggle" aria-pressed={mode?.kind === 'problems'} onclick={() => setMode(mode?.kind === 'problems' ? null : { kind: 'problems' })}>Only problems</button>
  {#if groupBy !== 'environment'}
    <label class="fm-env">
      <span>Environment</span>
      <select value={environment} onfocus={() => store.loadEnvironments()} onpointerenter={() => store.loadEnvironments()}
        onchange={(e) => set({ env: e.currentTarget.value || null })}>
        <option value="">All environments</option>
        {#each choices.filter((c) => !c.preview) as c (c.value)}<option value={c.value}>{c.label}</option>{/each}
        {#if choices.some((c) => c.preview)}
          <optgroup label="Previews">
            {#each choices.filter((c) => c.preview) as c (c.value)}<option value={c.value}>{c.label}</option>{/each}
          </optgroup>
        {/if}
        {#if environment && !choices.some((c) => c.value === environment)}<option value={environment}>{environment}</option>{/if}
      </select>
    </label>
  {/if}
</div>
{#if answer}<p class="fm-summary" data-testid="fleet-map-summary">{summary(answer, shown)}</p>{/if}
{#if mode}
  <div class="fm-modebar" data-testid="fleet-map-mode">
    {#if mode.kind === 'problems'}
      <p><b>Only problems.</b> Services with an error or a warning, and the dependencies that carry one. Everything healthy stays as grey context.</p>
    {:else if mode.kind === 'gaps'}
      <p><b>Gaps only.</b> Everything that matches is dimmed.</p>
      <div class="fm-chips" role="group" aria-label="Kinds of gap">
        {#each GAP_KINDS as k (k)}
          {@const [word, dash, col] = GAP_WORDS[k]}
          <button type="button" class="fm-chip" aria-pressed={mode.gaps.has(k)} onclick={() => mode?.kind === 'gaps' && setMode({ kind: 'gaps', gaps: toggleGap(mode.gaps, k) })}>
            <svg class="fm-sw" viewBox="0 0 22 12" aria-hidden="true"><line x1="1" y1="6" x2="21" y2="6" stroke="var({col})" stroke-width="2" stroke-dasharray={dashOf(dash)} /></svg>
            {word}{answer ? ` ${formatCount(gapCount(answer.match, [k]))}` : ''}
          </button>
        {/each}
      </div>
    {:else}
      <p><b>Missing contract sections.</b> Services that describe {mode.section === 'any' ? 'every section' : `their ${MISSING_SECTIONS[mode.section] ?? mode.section}`} are dimmed.</p>
    {/if}
    <button type="button" class="fm-clear" onclick={() => setMode(null)}>Show everything</button>
  </div>
{/if}
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
    onpointerleave={unhover}
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
  {#if tip && tipGroup}
    <div class="fm-tip" bind:this={tipEl} data-testid="fleet-map-group-tip" role="group" aria-label={tipGroup.label}
      style:left="{tip.x}px" style:top="{tip.y}px"
      onpointerleave={(e) => { if (!(e.relatedTarget instanceof Node && canvas?.contains(e.relatedTarget))) tip = null; }}>
      <b>{tipGroup.label}</b>
      <span>{countOf(groupCount(tipGroup, shown), 'service')}</span>
      <span>{tipGroup.match.percent == null ? 'Pacto observes nothing yet' : `${tipGroup.match.percent}% match · ${tipGroup.match.coverage ?? 0}% observed`}</span>
      <span>{tipGroup.percentComplete == null ? 'Contract completeness not judged' : `${tipGroup.percentComplete}% contract completeness`}</span>
      {#each (tipGroup.attention.worst ?? []).slice(0, 3) as w (w.category)}
        <span class="fm-tip-worst">{attentionCategoryLabel(w.category)} {w.count}</span>
      {/each}
      {#if tipGroup.percentComplete != null && tipGroup.percentComplete < 100}
        <button type="button" onclick={() => missingIn(tipGroup.key)}>What this group is missing</button>
      {/if}
    </div>
  {/if}
  <div class="visually-hidden" aria-live="polite">{live}</div>
</div>
<ul class="fm-legend" aria-label="Legend">
  {#each LEGEND as e (e.key)}
    <li>
      <svg class="fm-sw" viewBox="0 0 22 12" aria-hidden="true">
        {#if e.shape === 'line'}
          <line x1="1" y1="6" x2="21" y2="6" stroke={tone(e)} stroke-width="2" stroke-dasharray={dashOf(e.dash)} />
        {:else if e.shape === 'dot'}
          <circle cx="11" cy="6" r="3.5" fill={tone(e)} />
        {:else}
          <circle cx="11" cy="6" r="3.5" fill="none" stroke={tone(e)} stroke-width="1.25" stroke-dasharray={dashOf(e.dash)} />
          {#if e.shape === 'ring2'}<circle cx="11" cy="6" r="5.5" fill="none" stroke={tone(e)} stroke-width="1" />{/if}
        {/if}
      </svg>
      {#if e.glyph}<span class="fm-glyph" style:color={tone(e)} aria-hidden="true">{e.glyph}</span>{/if}
      {e.label}
    </li>
  {/each}
</ul>
</section>

<style>
  .fmw { display: flex; flex-direction: column; gap: var(--sp-2); min-inline-size: 0; }
  .fm-tools { display: flex; flex-wrap: wrap; align-items: center; gap: var(--sp-2) var(--sp-3); }
  .fm-seg { display: inline-flex; align-items: center; border: 1px solid var(--c-border); border-radius: var(--radius-sm); overflow: hidden; background: var(--c-surface); }
  .fm-seg-label { padding: 0 var(--sp-2); font-size: var(--text-xs); color: var(--c-text-3); text-transform: uppercase; letter-spacing: 0.04em; }
  .fm-seg button, .fm-toggle, .fm-chip, .fm-clear { border: 0; background: none; color: var(--c-text-2); cursor: pointer; padding: 4px 10px; min-block-size: 32px; font-size: var(--text-sm); }
  .fm-seg button + button { border-inline-start: 1px solid var(--c-border); }
  .fm-seg button[aria-pressed='true'], .fm-toggle[aria-pressed='true'], .fm-chip[aria-pressed='true'] { color: var(--c-accent); background: var(--c-accent-bg); font-weight: 600; }
  .fm-toggle, .fm-chip, .fm-clear { border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); }
  .fm-env { display: inline-flex; align-items: center; gap: var(--sp-2); font-size: var(--text-sm); color: var(--c-text-2); }
  .fm-env select { min-block-size: 32px; max-inline-size: 16rem; }
  .fm-summary { margin: 0; font-size: var(--text-sm); color: var(--c-text-2); overflow-wrap: anywhere; }
  .fm-modebar { display: flex; flex-wrap: wrap; align-items: center; gap: var(--sp-2) var(--sp-3); padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface-raised); font-size: var(--text-sm); }
  .fm-modebar p { margin: 0; flex: 1 1 18rem; }
  .fm-chips { display: flex; flex-wrap: wrap; gap: var(--sp-2); }
  .fm-chip { display: inline-flex; align-items: center; gap: var(--sp-1); }
  .fm-sw { inline-size: 22px; block-size: 12px; flex: none; }
  .fm-tip { position: absolute; z-index: 2; display: flex; flex-direction: column; gap: 2px; inline-size: max-content; max-inline-size: 16rem; padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface-raised); box-shadow: var(--shadow-md, 0 4px 16px rgb(0 0 0 / 0.25)); font-size: var(--text-sm); color: var(--c-text-2); overflow-wrap: anywhere; }
  .fm-tip b { color: var(--c-text); }
  .fm-tip-worst { color: var(--c-text-3); }
  .fm-tip button { margin-block-start: var(--sp-1); align-self: flex-start; border: 0; background: none; padding: 0; color: var(--c-accent); cursor: pointer; font-size: inherit; text-align: start; }
  .fm-legend { display: flex; flex-wrap: wrap; gap: var(--sp-1) var(--sp-3); margin: 0; padding: 0; list-style: none; font-size: var(--text-xs); color: var(--c-text-2); }
  .fm-legend li { display: inline-flex; align-items: center; gap: var(--sp-1); }
  .fm-glyph { font-weight: 700; }
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
  @media (pointer: coarse) { .fm-seg button, .fm-toggle, .fm-chip, .fm-clear, .fm-env select, .fm-tip button { min-inline-size: 44px; min-block-size: 44px; } }
</style>
