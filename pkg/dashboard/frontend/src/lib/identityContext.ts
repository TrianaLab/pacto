/**
 * The DISAMBIGUATING CONTEXT of an entity reference: the few bits that tell a
 * same-named entity in another domain or scope apart from this one.
 *
 * This lives on its own because two components need exactly the same answer. The
 * inline `EntityIdentity` renders it beside a label in a list row; the page header
 * renders it as an eyebrow above a page title. If they derived it separately they
 * would drift, and a page would disambiguate itself differently from the row the
 * user clicked to reach it -- which reads as two different entities.
 */

export interface EntityRefLike {
  key?: string;
  label?: string;
  kind?: string;
  domain?: string;
  scope?: string;
  parentService?: string;
  secondary?: string;
}

/**
 * spelledOut reports whether `bit` already appears in `label` as a WHOLE segment.
 *
 * A context bit the label already spells out is not disambiguation, it is the same
 * word twice: rows read "payments-service 1.2.0 - payments-service - 45cc..." and
 * "prod/k8s/orders-service - orders-service - prod". Segment-bounded (start, end or
 * a separator) so a target whose own name genuinely differs from its service still
 * says which service it belongs to.
 */
export function spelledOut(label: string, bit: string): boolean {
  const i = label.indexOf(bit);
  if (i < 0) return false;
  const sep = (c: string | undefined) => c === undefined || '/ @:·,'.includes(c);
  return sep(label[i - 1]) && sep(label[i + bit.length]);
}

/** primaryLabel is the human name of an entity, with an honest fallback. */
export function primaryLabel(ref: EntityRefLike | null | undefined): string {
  return ref?.label || ref?.key || '(unknown)';
}

/**
 * identityContext returns the disambiguating bits, in priority order, with anything
 * the label already states removed. Same-named services in two domains differ by
 * domain; targets differ by scope plus parent service.
 */
export function identityContext(ref: EntityRefLike | null | undefined): string[] {
  const r = ref || {};
  const label = primaryLabel(r);
  return [
    { raw: r.domain, text: `domain ${r.domain}` },
    { raw: r.parentService && r.parentService !== r.key ? r.parentService : '', text: r.parentService },
    { raw: r.scope, text: r.scope },
    // secondary is the copyable-ish extra (a digest or scope); show it only when it
    // is not already the key and not already surfaced as scope.
    { raw: r.secondary && r.secondary !== r.scope ? r.secondary : '', text: r.secondary },
  ]
    .filter((b) => b.raw && !spelledOut(label, b.raw))
    .map((b) => b.text as string);
}
