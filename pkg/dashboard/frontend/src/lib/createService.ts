/**
 * The words, links and request body of the Create service panel (515-S6): which
 * refusal sits at which field, the bounds of an offering field, the offering values a
 * person typed as the typed request, and where a signing login comes back to. Pure,
 * so the panel only draws.
 */

import { fleetEntityUrl } from './router.ts';
import { pagedHash } from './paging.ts';

/** A refused input of a create, at its field (the server's CreateServiceRefusal). */
export type CreateRefusal = { code: string; message: string; field: string; closest?: unknown; allowed?: unknown[] | null; cli?: string; mcp?: string };
/** A field of an offering's closed configuration schema, as the consumable read gives it. */
export type Parameter = {
  configuration?: string; path: string; type?: string; tier: string; required?: boolean; enum?: unknown[] | null; enumTotal?: number;
  minimum?: number; maximum?: number; exclusiveMinimum?: number; exclusiveMaximum?: number; minLength?: number; maxLength?: number; default?: unknown; sensitive?: boolean;
};
/** An offering a create may set values on. */
export type OfferingView = { offering: string; version?: string; requested?: boolean; classes?: string[] | null; parameters?: Parameter[] | null };
/** What a person typed: by offering, then field path, the raw input. */
export type Typed = Record<string, Record<string, string>>;

/** createReturn is the golden path page with its Create service panel open, where a
 *  signing login started for intent comes back. */
export function createReturn(pathKey: string, intent: string): string {
  return pagedHash(fleetEntityUrl('service', pathKey), { create: '1', signed: intent });
}

/** createSignUrl starts the signing login with the plan's one-time ticket. */
export function createSignUrl(ticket: string, pathKey: string, intent: string): string {
  return `/auth/sign?ticket=${encodeURIComponent(ticket)}&return=${encodeURIComponent('/' + createReturn(pathKey, intent))}`;
}

// The contract schema's service.name and owner.team patterns; a namespace is a DNS label.
const NAME = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/;
const TEAM = /^[a-zA-Z0-9._/-]+$/;

/** createFieldError is the first essential field that is wrong, and why; field ''
 *  when the plan may be asked for. */
export function createFieldError(name: string, owner: string, namespace: string): { field: '' | 'name' | 'owner' | 'namespace'; message: string } {
  if (!name) return { field: 'name', message: 'Name the new service' };
  if (name.length > 63 || !NAME.test(name)) return { field: 'name', message: 'A service name uses lower-case letters, digits and dashes, at most 63' };
  if (owner && (owner.length > 253 || !TEAM.test(owner))) return { field: 'owner', message: 'A team uses letters, digits, dots, dashes, underscores and slashes' };
  if (!namespace) return { field: 'namespace', message: 'Name the namespace you create the service in' };
  if (namespace.length > 63 || !NAME.test(namespace)) return { field: 'namespace', message: 'A namespace uses lower-case letters, digits and dashes, at most 63' };
  return { field: '', message: '' };
}

const fmt = (v: unknown) => (typeof v === 'string' ? v : JSON.stringify(v));

/** boundsText is what a field allows, short: its values, its range or its type. */
export function boundsText(p: Parameter): string {
  if (p.enum?.length) return p.enum.map(fmt).join(' | ') + ((p.enumTotal ?? 0) > p.enum.length ? ` (+${(p.enumTotal ?? 0) - p.enum.length} more)` : '');
  const lo = p.minimum ?? p.exclusiveMinimum;
  const hi = p.maximum ?? p.exclusiveMaximum;
  if (lo !== undefined && hi !== undefined) return `${lo}-${hi}`;
  if (lo !== undefined) return `at least ${lo}`;
  if (hi !== undefined) return `at most ${hi}`;
  if (p.maxLength !== undefined) return `text, at most ${p.maxLength}`;
  return p.type || 'any value';
}

/** fieldKey names one input of the panel: an offering field, set once (env '') or
 *  for one environment; path '' is the offering itself. */
export function fieldKey(offering: string, path: string, env = ''): string {
  return `${offering}\u0000${path}\u0000${env}`;
}

/** The environment input of the per-environment fold. */
export const ENV_FIELD = fieldKey('', '', '*');

// mcpOffering is the offering a refusal's pacto_create_service fragment sets, or ''.
function mcpOffering(mcp: string | undefined): string {
  try {
    const o = JSON.parse(mcp || '{}')?.offerings?.[0]?.offering;
    return typeof o === 'string' ? o : '';
  } catch {
    return '';
  }
}

// ownerOf is the offering a field key path belongs to: the one the fragment names,
// else the only offering declaring that path.
function ownerOf(r: CreateRefusal, key: string, offerings: readonly OfferingView[]): string {
  const named = mcpOffering(r.mcp);
  if (named) return named;
  const holders = offerings.filter((o) => (o.parameters ?? []).some((p) => p.path === key));
  return holders.length === 1 ? holders[0].offering : '';
}

/** placeRefusals puts each refusal at the input it is about (by fieldKey), and the
 *  ones no input shows in rest (an offering the caller may not consume has no input),
 *  so nothing refused is ever dropped. */
export function placeRefusals(refusals: readonly CreateRefusal[] | null | undefined, offerings: readonly OfferingView[]): { at: Record<string, CreateRefusal[]>; rest: CreateRefusal[] } {
  const at: Record<string, CreateRefusal[]> = {};
  const rest: CreateRefusal[] = [];
  const put = (k: string, r: CreateRefusal) => { (at[k] ??= []).push(r); };
  for (const r of refusals ?? []) {
    const f = r.field ?? '';
    let m: RegExpExecArray | null;
    if ((m = /^offerings\[(.+)\]$/.exec(f)) && offerings.some((o) => o.offering === m![1])) put(fieldKey(m[1], ''), r);
    else if ((m = /^configurations\[[^\]]+\]\.values\.(.+)$/.exec(f)) && ownerOf(r, m[1], offerings)) put(fieldKey(ownerOf(r, m[1], offerings), m[1]), r);
    else if ((m = /^environments\[([^\]]+)\]\.[^.]+\.(.+)$/.exec(f)) && ownerOf(r, m[2], offerings)) put(fieldKey(ownerOf(r, m[2], offerings), m[2], '*'), r);
    else if (/^environments\[[^\]]+\]$/.test(f)) put(ENV_FIELD, r);
    else rest.push(r);
  }
  return { at, rest };
}

/** closestText says the closest valid value a refusal names, and the others an enum
 *  or class allows; '' when it names none. */
export function closestText(r: CreateRefusal): string {
  if (r.closest === undefined || r.closest === null) return '';
  const others = (r.allowed ?? []).filter((a) => fmt(a) !== fmt(r.closest));
  return `closest ${fmt(r.closest)}${others.length ? ` (also ${others.map(fmt).join(', ')})` : ''}`;
}

// typed is a raw input as the value its field takes: a number field's number, a
// boolean field's boolean, an enum's own value (whatever its JSON type), the rest as
// typed. A value that does not parse stays the text, so the server refuses it at its field.
function typed(p: Parameter | undefined, raw: string): unknown {
  if (p?.type === 'boolean' && (raw === 'true' || raw === 'false')) return raw === 'true';
  const hit = p?.enum?.find((v) => fmt(v) === raw);
  if (hit !== undefined) return hit;
  if ((p?.type === 'integer' || p?.type === 'number') && raw.trim() !== '' && Number.isFinite(Number(raw))) return Number(raw);
  return raw;
}

// setPath sets v at the dotted path inside obj, as nested objects.
function setPath(obj: Record<string, unknown>, dotted: string, v: unknown) {
  const parts = dotted.split('.');
  let o = obj;
  for (const k of parts.slice(0, -1)) o = (o[k] ??= {}) as Record<string, unknown>;
  o[parts[parts.length - 1]] = v;
}

/** offeringPicks is the request's offerings: the values typed once and, for env, the
 *  per-environment ones; an offering with nothing typed is left out. */
export function offeringPicks(views: readonly OfferingView[], once: Typed, env: string, perEnv: Typed) {
  const out: { offering: string; values?: Record<string, unknown>; environments?: Record<string, Record<string, unknown>> }[] = [];
  const names = [...new Set([...views.map((v) => v.offering), ...Object.keys(once), ...Object.keys(perEnv)])];
  for (const name of names) {
    const params = new Map((views.find((v) => v.offering === name)?.parameters ?? []).map((p) => [p.path, p]));
    const fill = (src: Record<string, string> | undefined) => {
      const o: Record<string, unknown> = {};
      for (const [path, raw] of Object.entries(src ?? {})) if (raw !== '') setPath(o, path, typed(params.get(path), raw));
      return Object.keys(o).length ? o : undefined;
    };
    const values = fill(once[name]);
    const envValues = env ? fill(perEnv[name]) : undefined;
    if (!values && !envValues) continue;
    out.push({ offering: name, ...(values ? { values } : {}), ...(envValues ? { environments: { [env]: envValues } } : {}) });
  }
  return out;
}

/** signatureText is the golden path's signature step in words: an unchecked one is
 *  Unknown, never verified. */
export function signatureText(step: { status?: string; detail?: string } | null | undefined): string {
  if (step?.status === 'done') return 'signed';
  if (step?.status === 'failed') return 'signature refused';
  return 'signature Unknown';
}
