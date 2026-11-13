/**
 * How the dashboard words provider-reported provisioning status. Unknown is never Ready,
 * and only a signed Ready reads positive: an unsigned one is info-toned, draws no check
 * and says "(unsigned)" inline, the rule the CLI follows.
 */
import type { components } from './generated/schema.ts';
import { formatDateTime, formatRelative } from './dateFormat.ts';

export type ProvisioningStatus = components['schemas']['ProductProvisioningStatus'];

/**
 * The badge tone of a provisioning phase: only a signed Ready is ok and only Failed is an
 * error. `verified` defaults to true only for phase buckets that carry no status.
 */
export function phaseTone(phase: string | undefined, verified = true): 'ok' | 'err' | 'info' | 'neutral' {
  switch (phase) {
    case 'Ready': return verified ? 'ok' : 'info';
    case 'Failed': return 'err';
    case 'Requested': case 'Provisioning': case 'Deleting': return 'info';
    default: return 'neutral';
  }
}

/** The glyph of a provisioning phase (S6): drawn as geometry beside its badge. The check is for a signed Ready only. */
export function phaseGlyph(phase: string | undefined, verified = true): 'ok' | 'err' | 'progress' | 'none' | 'unknown' {
  switch (phase) {
    case 'Ready': return verified ? 'ok' : 'none';
    case 'Failed': return 'err';
    case 'Requested': case 'Provisioning': return 'progress';
    case 'Deleting': return 'none';
    default: return 'unknown';
  }
}

/**
 * The short badge text: the environment and entry it is about, then the phase, and
 * " (unsigned)" whenever a source reported it without a signature (every phase alike,
 * as the CLI's provisioningText words it).
 */
export function phaseBadgeLabel(st: ProvisioningStatus): string {
  const where = [st.environment, st.entry].filter(Boolean).join(' ');
  const phase = st.source && !st.verified ? `${st.phase} (unsigned)` : st.phase;
  return where ? `${where}: ${phase}` : phase;
}

/** One line that says what a status means and who said it. */
export function phaseLabel(st: ProvisioningStatus, now: number = Date.now()): string {
  if (st.phase === 'Unknown') {
    if (st.conflict?.length) return `Unknown: reporters disagree (${st.conflict.join(', ')})`;
    if (st.stale && st.reportedPhase) return `Unknown: last reported ${st.reportedPhase}, stale`;
    return `Unknown: ${st.reason || 'no provider has reported'}`;
  }
  const when = formatRelative(st.asOf, now);
  if (!st.verified) return `${st.phase} (unsigned target-state file, ${when})`;
  return `${st.phase} (reported by ${st.producer || 'an unnamed producer'} through ${st.source}, ${when})`;
}

/** The full detail a badge title carries; the row shows only the phase. */
export function provisioningTitle(st: ProvisioningStatus, now: number = Date.now()): string {
  return [
    phaseLabel(st, now),
    st.provider ? `Adapter: ${st.provider}` : '',
    st.externalRef ? `External reference: ${st.externalRef}` : '',
    st.sourceKind ? `Source: ${st.sourceKind}` : '',
    st.asOf ? `As of: ${formatDateTime(st.asOf)}` : '',
    st.revision ? `Consumer revision: ${st.revision.label}` : '',
  ].filter(Boolean).join('\n');
}

/** The words of a provisioning phase bucket, as the phase filter and the phase track name it. */
export const PHASE_WORDS: Record<string, string> = {
  requested: 'Requested', provisioning: 'Provisioning', ready: 'Ready', failed: 'Failed', deleting: 'Deleting', unknown: 'Not reported',
};
