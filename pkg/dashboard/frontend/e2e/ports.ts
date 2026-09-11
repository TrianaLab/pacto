/**
 * Ports the browser suite serves on.
 *
 * Imported by BOTH playwright.config.ts (which starts the servers) and the specs
 * (which navigate to them), so a port change cannot leave a spec pointing at a
 * server nobody started. 4322 is the docs-site suite's and is deliberately skipped.
 */

/** The built WASM demo — a Fleet host, and the baseURL for almost every spec. */
export const APP_PORT = Number(process.env.PW_PORT || 4321);

/**
 * The offline `pacto doc --format html` export — the only host of the legacy
 * non-Fleet views. It gets its own origin because the embedded UI it reuses
 * references its assets absolutely, so it cannot be mounted under a subpath of
 * the demo.
 */
export const DOC_PORT = Number(process.env.PW_DOC_PORT || 4323);

/** DOC_ORIGIN is the absolute origin of the offline export server. */
export const DOC_ORIGIN = `http://127.0.0.1:${DOC_PORT}`;
