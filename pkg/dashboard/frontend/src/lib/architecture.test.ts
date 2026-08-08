import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync } from 'node:fs';
import { join, relative } from 'node:path';

/**
 * Architecture guard (ADR-6, requirement, item 15): the generated OpenAPI SDK is
 * the ONLY way the dashboard talks to the backend. This test fails if new code
 * bypasses it - a raw fetch of a backend route, or a hand-built `/api/...` URL -
 * outside the single transport seam and the facade. It keeps the wire contract
 * from drifting back into scattered handwritten clients.
 *
 * Allowed exceptions, by design:
 *  - lib/transport.ts   the single fetch seam behind the generated client;
 *  - lib/api.ts         the facade, which passes typed operation paths to the
 *                       generated client (type-checked against the contract, not
 *                       URL construction);
 *  - lib/generated/**   the generated SDK itself.
 */

const SRC = join(process.cwd(), 'src'); // vitest runs from the frontend package root

const FETCH_ALLOW = ['lib/transport.ts'];
const API_PATH_ALLOW = ['lib/transport.ts', 'lib/api.ts'];

function sourceFiles(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = join(dir, entry.name);
    if (entry.isDirectory()) {
      if (entry.name === 'generated' || entry.name === 'node_modules') continue;
      out.push(...sourceFiles(full));
      continue;
    }
    if (!/\.(ts|svelte)$/.test(entry.name)) continue;
    // Test and compile-time-test files legitimately reference paths/fetch mocks.
    if (/\.(test|typetest)\.ts$/.test(entry.name)) continue;
    out.push(full);
  }
  return out;
}

const files = sourceFiles(SRC).map((f) => ({ rel: relative(SRC, f), body: readFileSync(f, 'utf8') }));

describe('frontend backend-access architecture', () => {
  it('scans a non-trivial set of source files', () => {
    expect(files.length).toBeGreaterThan(20);
  });

  it('performs a raw backend fetch only in the transport seam', () => {
    const offenders = files
      .filter((f) => !FETCH_ALLOW.includes(f.rel))
      .filter((f) => /(?<![.\w])fetch\s*\(/.test(f.body))
      .map((f) => f.rel);
    expect(offenders, `raw fetch() outside the transport seam: ${offenders.join(', ')}`).toEqual([]);
  });

  it('references /api/ backend paths only in the facade and transport', () => {
    const offenders = files
      .filter((f) => !API_PATH_ALLOW.includes(f.rel))
      .filter((f) => /['"`]\/api\//.test(f.body))
      .map((f) => f.rel);
    expect(offenders, `hand-built /api/ URL outside the generated-SDK facade: ${offenders.join(', ')}`).toEqual([]);
  });
});
