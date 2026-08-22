/**
 * Product IA entry-point guard: the public "Live Demo" CTAs must launch the
 * Fleet-capable WASM demo at its canonical product entry (/demo/#/fleet), not the bare
 * /demo/ that falls through to the legacy landing. The demo bootstrap (examples/demo/
 * boot.js) also canonicalizes a no-hash entry, so this is defense-in-depth on the docs
 * side. Paths resolve from this file's location, so the check is cwd-independent.
 */
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../../../../..');

describe('public Live Demo links use the canonical Fleet entry', () => {
  for (const rel of ['README.md', 'docs/examples/dashboard-demo.md']) {
    it(`${rel} links the demo to /demo/#/fleet (the product Operational Overview)`, () => {
      const txt = readFileSync(resolve(repoRoot, rel), 'utf8');
      // The governed demo CTA carries the fleet entry hash...
      expect(txt).toMatch(/demo\/#\/fleet/);
      // ...and no longer offers a bare /demo/ CTA that falls through to the legacy home.
      expect(txt).not.toMatch(/demo\/"[^#]/); // e.g. href="../../demo/" with no hash
      expect(txt).not.toMatch(/latest\/demo\/\)/); // e.g. (https://.../latest/demo/) with no hash
    });
  }
});
