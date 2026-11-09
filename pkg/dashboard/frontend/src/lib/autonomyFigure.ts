/**
 * The Delivery Autonomy figure's pure model (SPEC 7): "How much does delivery run on
 * its own?". One card per level, every card the same size (S9: size never encodes the
 * level); under each, how many Stages sit there as a count and a bar on one shared
 * scale. The aside says who approved the promotions in the Stages' windows: a person,
 * nobody (automatic) or not recorded, which is never counted as automatic.
 */
import type { DeliveryAutonomy } from './api.ts';
import { countOf, formatCount } from './format.ts';
import { levelLabel } from './delivery.ts';

type Level = NonNullable<DeliveryAutonomy['byLevel']>[number];

/** What happens to one change on each level, in one sentence. */
/** The fleet default: a Stage with no spec.startLevel starts on observe (delivery.CurrentLevel). */
const DEFAULT_LEVEL = 'observe';

const SAYS: Record<string, string> = {
  observe: 'GitOps promotes every change; Pacto plans it and moves nothing.',
  advise: 'Pacto proposes; a person promotes every change.',
  'auto-nonbreaking': 'Non-breaking changes promote alone (top); breaking ones wait for a person (bottom).',
  autonomous: 'Every change the gate clears promotes alone.',
};

export function autonomyModel(byLevel: Level[], promotions: { count: number; of: number; untyped: number }) {
  const cards = byLevel.filter((l) => l.level).map((l) => ({ level: l.level, label: levelLabel(l.level), says: SAYS[l.level] ?? '', n: l.stages, def: l.level === DEFAULT_LEVEL }));
  const max = Math.max(1, ...cards.map((c) => c.n));
  const none = byLevel.find((l) => !l.level)?.stages ?? 0;
  const total = byLevel.reduce((n, l) => n + l.stages, 0);
  const approvals = { person: Math.max(0, promotions.of - promotions.count), auto: promotions.count, unrecorded: promotions.untyped };
  const caption = [
    `${countOf(total, 'Stage')}: ${cards.map((c) => `${formatCount(c.n)} ${c.label.toLowerCase()}`).join(', ')}${none ? ` and ${formatCount(none)} that report no level` : ''}.`,
    promotions.of ? `${formatCount(approvals.auto)} of the ${countOf(promotions.of, 'promotion')} in the Stages' last 20 operations ran with no person.` : 'No promotion succeeded in the Stages\' last 20 operations.',
    promotions.untyped ? `${countOf(promotions.untyped, 'operation')} ${promotions.untyped === 1 ? 'carries' : 'carry'} no type, so who approved ${promotions.untyped === 1 ? 'it' : 'them'} is not recorded; that is never counted as automatic.` : '',
  ].filter(Boolean).join(' ');
  return {
    cards, max, none, total, approvals, caption,
    figRef: 'The drawings are the same size on purpose; the bars give the number of Stages. The outlined card is where every Stage starts.',
    alt: `${cards.map((c) => `${c.label} ${c.n}`).join(', ')}${none ? `, no level ${none}` : ''}. Approvals: ${approvals.person} by a person, ${approvals.auto} automatic, ${approvals.unrecorded} not recorded.`,
  };
}
