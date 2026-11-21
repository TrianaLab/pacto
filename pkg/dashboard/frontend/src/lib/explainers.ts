/**
 * The explainers' models (SPEC 2.2, "How this works"): drawings of a mechanism, never
 * of a fleet. They use the neutral and accent tones only, so a diagram never reads as a
 * status, and carry no data.
 */
import type { WireDef } from './figure.ts';
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
