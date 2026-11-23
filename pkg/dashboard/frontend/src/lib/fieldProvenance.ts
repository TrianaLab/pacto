/**
 * The words of a declared service's field provenance (513-S5): one replacement as
 * pacto fleet get prints it, a field row's label and its mark's tip. Kept out of
 * entityLabels.ts so they load with the service page, never in the first download.
 */

import { formatDateTime, formatRelative } from './dateFormat.ts';
import { serviceKeyName, type ReplacementRef } from './entityLabels.ts';
import { shortDigest } from './format.ts';

/** replacementLine is one replacement as fleet.Replacement.Line words, its age read
 *  against asOf: the same line pacto fleet get prints. */
export function replacementLine(r: ReplacementRef, asOf?: string): string {
  const from = serviceKeyName(r.from);
  if (r.link === 'inferred-from') return `Declared by ${r.by || 'an unknown signer'} from inferred ${from} (revision ${r.revision || 'unknown'})`;
  const age = formatRelative(r.at, Date.parse(asOf ?? '') || Date.now());
  if (r.link === 'observed') {
    const how = r.signal === 'annotation' ? 'its pacto.trianalab.io/contract annotation' : `its running image digest sha256:${shortDigest(r.value, 12)}…`;
    return `${from} is linked to ${r.to || 'this service'} by ${how} (observed${age ? `, ${age}` : ''})`;
  }
  return `Replaces ${from}${age ? ` ${age}` : ''} (linked by Pacto target ${r.target || 'of this service'})`;
}

type FieldRef = { field: string; provenance: string; by?: string; source?: string; asOf?: string; completeness?: string; proposed?: string };

/** fieldLabel is the short label of a field row, as FieldProvenance.Line names it. */
export function fieldLabel(field: string): string {
  const dep = /^dependencies\[(.+)\]\.compatibility$/.exec(field);
  if (dep) return `${dep[1]} range`;
  return field === 'service.owner' ? 'owner' : field;
}

/** fieldProvenanceText is the tip of one field row's mark: where its value comes from,
 *  who accepted it, as of when and how complete the read was. A proposed range is
 *  never declared, and says so. */
export function fieldProvenanceText(f: FieldRef): string {
  const asOf = formatDateTime(f.asOf);
  const at = asOf ? ` as of ${asOf}` : '';
  const partial = f.completeness ? `, ${f.completeness}` : '';
  if (f.proposed) {
    const kind = f.source?.startsWith('cluster:') ? f.source.slice('cluster:'.length).split(' ')[0] : '';
    const from = kind === 'runtime' ? 'runtime calls' : kind || f.source || 'an unknown source';
    return `Proposed ${f.proposed} from ${from}${at}${partial}; never declared`;
  }
  const source = f.source ?? '';
  switch (f.provenance) {
    case 'accepted':
      return `Accepted by ${f.by || 'an unknown signer'} in ${source.replace(/^pacto\.yaml of /, '') || 'its revision'}${at}`;
    case 'attested':
      return `Attested by the repository${source ? `: read from ${source}` : ''}${at}`;
    case 'inferred':
      return `Inferred from ${source || 'an unknown source'}${at}${partial}`;
  }
  if (source === 'Pacto target') return `Declared by the contract a running Pacto target reports${at}`;
  return `Declared${source ? ` in ${source}` : ''}${asOf ? `, read ${asOf}` : ''}`;
}
