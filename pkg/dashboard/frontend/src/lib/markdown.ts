/**
 * Render in-bundle Markdown to sanitized HTML for display in the dashboard.
 *
 * Bundle docs can originate from arbitrary OCI registries, so the output is run
 * through DOMPurify (no script execution, no event-handler attributes) before it
 * is injected with {@html}. The dashboard also holds sessions and the Approve
 * button, so a doc may not restyle the page or draw a form either: <style>,
 * style attributes and form controls are refused, and a fake "sign in again"
 * box cannot be built. The server's Content-Security-Policy backs this up. Anchors are forced to open in a new tab with a safe
 * rel, since rendered content may link anywhere.
 */
import { marked } from 'marked';
import DOMPurify from 'dompurify';

DOMPurify.addHook('afterSanitizeAttributes', (node) => {
  if (node.nodeName === 'A') {
    node.setAttribute('target', '_blank');
    node.setAttribute('rel', 'noopener noreferrer');
  }
});

const PURIFY = {
  FORBID_TAGS: ['style', 'form', 'input', 'button', 'textarea', 'select', 'option', 'iframe', 'link', 'meta', 'base'],
  FORBID_ATTR: ['style', 'formaction'],
};

export function renderMarkdown(src: string): string {
  const raw = marked.parse(src ?? '', { async: false }) as string;
  return DOMPurify.sanitize(raw, PURIFY);
}

/**
 * Mermaid's settings for bundle diagrams. A diagram can override settings with
 * an `%%{init: ...}%%` line, and themeCSS lands in a <style> that applies to the
 * whole page, so everything that styles or loosens rendering is pinned here.
 * `secure` replaces Mermaid's own list, so its defaults are repeated.
 */
export const MERMAID_CONFIG = {
  startOnLoad: false,
  securityLevel: 'strict' as const, // no JS, no click handlers
  // Render labels as native SVG <text> rather than HTML in <foreignObject>:
  // the sanitized-SVG output keeps <text>/<tspan>, but would strip foreignObject
  // HTML, leaving empty shapes. htmlLabels:false also keeps <br/> as line breaks.
  htmlLabels: false,
  flowchart: { htmlLabels: false },
  theme: 'neutral' as const,
  fontFamily: 'inherit',
  secure: [
    'secure', 'securityLevel', 'startOnLoad', 'maxTextSize', 'suppressErrorRendering', 'maxEdges',
    'themeCSS', 'themeVariables', 'theme', 'fontFamily', 'altFontFamily', 'htmlLabels', 'flowchart', 'dompurifyConfig',
  ],
};
