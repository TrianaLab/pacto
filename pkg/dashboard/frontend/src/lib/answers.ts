// One-line answers for the top of a page. Each page answers its question first, in
// plain words, from the same backend aggregate its charts draw -- never from a preview.

const plural = (n: number, one: string, many: string) => `${n.toLocaleString('en-US')} ${n === 1 ? one : many}`;

/** Joins phrases with commas and a final "and" (or `word`), with no Oxford comma. */
export function joinAnd(parts: string[], word = 'and'): string {
  return parts.length < 2 ? parts.join('') : `${parts.slice(0, -1).join(', ')} ${word} ${parts[parts.length - 1]}`;
}

interface PostureSummary {
  targets?: number;
  revisions?: number;
  revisionsInUse?: number;
  invalidRevisions?: number;
  compliance?: { compliant?: number; nonCompliant?: number; unknown?: number };
}

/**
 * postureAnswer reads a service, owner or fleet posture aggregate and says whether
 * anything is broken. `incomplete` (the snapshot is missing data) withholds the
 * all-good claim. `conflicts` counts revisions that name a different owner: it and an
 * estate where no running target matches a known revision are named in the answer, so
 * the line never says "nothing broken" above either.
 */
export function postureAnswer(sum: PostureSummary = {}, incomplete = false, conflicts = 0): { tone: 'err' | 'warn' | 'ok' | 'neutral'; lead: string; rest: string } {
  const targets = sum.targets ?? 0;
  const nc = sum.compliance?.nonCompliant ?? 0;
  const unknown = sum.compliance?.unknown ?? 0;
  const invalid = sum.invalidRevisions ?? 0;
  const problems = [
    nc ? `${plural(nc, 'running target breaks its', 'running targets break their')} contract` : '',
    invalid ? `${plural(invalid, 'revision is', 'revisions are')} invalid` : '',
  ].filter(Boolean);
  const concerns = [
    targets && unknown ? `${plural(unknown, 'running target has', 'running targets have')} unknown compliance` : '',
    targets && sum.revisionsInUse === 0 && (sum.revisions ?? 0) > 0 ? 'no running target matches a known revision' : '',
    conflicts ? `${plural(conflicts, 'revision names', 'revisions name')} a different owner` : '',
  ].filter(Boolean);
  if (problems.length) return { tone: 'err', lead: 'Needs work.', rest: `${cap(joinAnd([...problems, ...concerns]))}.` };
  if (concerns.length) return { tone: 'warn', lead: 'Needs a look.', rest: `${cap(joinAnd(concerns))}.` };
  if (targets === 0) return { tone: 'neutral', lead: 'Nothing running yet.', rest: 'There is no compliance picture until a target is observed.' };
  // The knowledge banner under the answer says what is missing; saying it here too doubled it.
  if (incomplete) return { tone: 'warn', lead: 'Nothing broken found.', rest: '' };
  if ((sum.compliance?.compliant ?? 0) === targets) {
    return { tone: 'ok', lead: 'Healthy.', rest: targets === 1 ? 'The one running target is compliant.' : `All ${plural(targets, 'running target', 'running targets')} are compliant.` };
  }
  return { tone: 'neutral', lead: 'Nothing broken found.', rest: '' };
}

const cap = (s: string) => (s ? s[0].toUpperCase() + s.slice(1) : s);

interface RevisionFacts {
  valid?: boolean;
  validation?: { total?: number };
  readiness?: { passing?: boolean; score?: number; minScore?: number; expired?: boolean } | null;
}

/** revisionAnswer: is this contract revision valid, and is it ready by its own gate? */
export function revisionAnswer(r: RevisionFacts = {}): { tone: 'err' | 'warn' | 'ok' | 'neutral'; lead: string; rest: string } {
  if (r.valid === false) {
    const n = r.validation?.total ?? 0;
    return { tone: 'err', lead: 'Invalid contract.', rest: n ? `${plural(n, 'validation finding', 'validation findings')} below.` : '' };
  }
  const g = r.readiness;
  if (!g) return { tone: 'neutral', lead: 'Valid.', rest: 'No readiness gate declared.' };
  const score = `Readiness ${g.score ?? 0}, ${g.minScore ?? 0} required${g.expired ? ', assessment expired' : ''}.`;
  return g.passing && !g.expired
    ? { tone: 'ok', lead: 'Valid and ready.', rest: score }
    : { tone: 'warn', lead: 'Valid, not ready.', rest: score };
}

/** A plain sentence for a confirmed-problem attention category, or '' for any other. */
export function problemPhrase(category: string | undefined, n: number): string {
  if (category === 'non-compliant') return `${plural(n, 'running target breaks its', 'running targets break their')} contract`;
  if (category === 'invalid') return `${plural(n, 'revision is', 'revisions are')} invalid`;
  return '';
}
