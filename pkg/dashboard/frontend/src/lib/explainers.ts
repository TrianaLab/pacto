/**
 * The explainers' models (SPEC 2.2, "How this works"): drawings of a mechanism, never
 * of a fleet. They use the neutral and accent tones only, so a diagram never reads as a
 * status, and carry no data.
 */
import type { WireDef, WireStyle } from './figure.ts';
import type { Step } from './narrate.ts';

export interface ExplainStation { key: string; label: string; sub: string; tone: 'neutral' | 'accent'; shape: 'circle' | 'square' | 'diamond'; reached: true }

/**
 * knowsModel is "How does Pacto know?" on Overview: sources report, Pacto joins the
 * reports into one snapshot, each claim keeps its evidence and the verdict is decided
 * from evidence. A rail walk, one step per station (A1b), that plays only when the
 * reader opens it.
 */
export function knowsModel() {
  const stations: ExplainStation[] = [
    { key: 'sources', label: 'Sources', sub: 'Registries, clusters and evidence servers report what exists and what runs', tone: 'neutral', shape: 'square', reached: true },
    { key: 'snapshot', label: 'Snapshot', sub: 'Pacto joins the reports into one picture at one time', tone: 'accent', shape: 'square', reached: true },
    { key: 'evidence', label: 'Evidence', sub: 'Each claim keeps where it came from and when', tone: 'neutral', shape: 'circle', reached: true },
    { key: 'verdict', label: 'Verdict', sub: 'Compliance is decided from evidence. Missing evidence is Unknown, never Compliant', tone: 'accent', shape: 'diamond', reached: true },
  ];
  const wires: WireDef[] = stations.slice(1).map((_, k) => ({
    id: `wf${k}`, from: `f${k}`, fs: 'r', to: `f${k + 1}`, ts: 'l', col: { fs: 'b', ts: 't' }, style: 'obs', tone: 'accent', head: true,
  }));
  const steps: Step[] = stations.map((s, k) => ({
    cap: `${s.label}: ${s.sub}.`, dur: 120, hold: 30,
    show: [`[data-m="f${k}"]`, ...(k ? [`w:wf${k - 1}`] : [])], sig: k ? [`wf${k - 1}`] : [],
  }));
  return { stations, wires, steps, alt: 'Sources feed a snapshot; the snapshot holds evidence; verdicts are decided from evidence.' };
}

/** The four wire styles of SPEC 2.1, in the graph legend's order. */
export const EDGE_STYLES: { style: WireStyle; label: string; sub: string }[] = [
  { style: 'obs', label: 'Observed', sub: 'a target reported the call' },
  { style: 'proj', label: 'Projected', sub: 'the contract declares it; nothing reported it yet' },
  { style: 'stale', label: 'Stale', sub: 'last reported too long ago' },
  { style: 'broken', label: 'Missing', sub: 'its source is unavailable' },
];

/**
 * edgeModel is "How does an edge read in the graph?" above the graph canvas (SPEC 17,
 * r4): four consumers wired into one service, one per wire style. Static.
 */
export function edgeModel() {
  const wires: WireDef[] = EDGE_STYLES.map((e, k) => ({ id: `wg${k}`, from: `g${k}`, fs: 'r', to: 'gh', ts: 'l', when: ['wide', 'mid', 'narrow'], style: e.style, tone: 'neutral' }));
  return {
    rows: EDGE_STYLES.map((e, k) => ({ ...e, a: `g${k}` })),
    wires,
    alt: 'Four edges into one service: observed (solid), projected (dashed), stale (dotted) and missing (broken with a cross).',
  };
}
