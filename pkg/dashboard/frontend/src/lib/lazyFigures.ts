/**
 * The fleet figures load on first use (LazyView), so the figure kit stays out of the
 * first download of the eager list views. Each import is cached.
 */
const once = <T>(f: () => Promise<T>) => { let p: Promise<T> | undefined; return () => (p ??= f()); };
// @ts-expect-error — Svelte component has no declaration file
export const loadTrustFigure = once(() => import('../components/figure/TrustFigure.svelte'));
// @ts-expect-error — Svelte component has no declaration file
export const loadPostureFigure = once(() => import('../components/figure/PostureFigure.svelte'));
// @ts-expect-error — Svelte component has no declaration file
export const loadRankFigure = once(() => import('../components/figure/RankFigure.svelte'));
// @ts-expect-error — Svelte component has no declaration file
export const loadKnowsExplainer = once(() => import('../components/figure/explain/KnowsExplainer.svelte'));
// @ts-expect-error — Svelte component has no declaration file
export const loadOriginFigure = once(() => import('../components/figure/OriginFigure.svelte'));
// @ts-expect-error — Svelte component has no declaration file
export const loadCreationExplainer = once(() => import('../components/figure/explain/CreationExplainer.svelte'));
// @ts-expect-error — Svelte component has no declaration file
export const loadOfferingsFigure = once(() => import('../components/figure/OfferingsFigure.svelte'));
// The Cluster filter shows only with two or more clusters, so the list views' first load skips it.
// @ts-expect-error — Svelte component has no declaration file
export const loadClusterFilter = once(() => import('../components/ClusterFilter.svelte'));
// The fleet map's canvas engine, and the two figures over it, load with the Overview's map block.
export const loadFleetMap = once(() => import('../components/FleetMap.svelte'));
// @ts-expect-error — Svelte component has no declaration file
export const loadFleetFigures = once(() => import('../components/FleetFigures.svelte'));
