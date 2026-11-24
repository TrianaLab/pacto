/**
 * Every source kind the tooltips name has a dot colour: the base `.source-dot` paints no
 * background, so a kind without its rule draws an invisible dot.
 */
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const read = (f: string) => readFileSync(join(process.cwd(), f), 'utf8');
const format = read('src/lib/format.ts');
const css = read('src/styles/components.css');

const table = format.slice(format.indexOf('const SOURCE_DESCRIPTIONS'), format.indexOf('};', format.indexOf('const SOURCE_DESCRIPTIONS')));
const kinds = [...table.matchAll(/^\s*'?([\w-]+)'?:\s*'/gm)].map((m) => m[1]);

describe('source dots', () => {
  it('reads the source kinds', () => expect(kinds).toContain('index'));
  it.each(kinds)('paints a background for %s', (kind) => {
    // Catalog kinds (catalog, <idp>-catalog) share one generic attribute rule.
    const selector = kind.includes('catalog') ? '\\.source-dot\\[class\\*="catalog"\\]' : `\\.source-dot-${kind}(?![\\w-])`;
    expect(css).toMatch(new RegExp(`${selector}[^{]*\\{[^}]*background:`));
  });
});
