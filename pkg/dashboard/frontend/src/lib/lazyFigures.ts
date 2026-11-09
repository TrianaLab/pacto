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
