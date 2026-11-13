/**
 * The Offerings list figure (SPEC 7, B3): "Are the requests for platform offerings
 * ready?". Every request of every matched offering on the one phase track the offering
 * page draws, from the backend's fleet tally, never from the loaded page, with the
 * offerings that have most failed consumers named and the rest counted.
 */
import { phaseTrack, type PhaseTally } from './entityFigures.ts';
import { countOf } from './format.ts';

export interface OfferingsAggregate {
  offeringCount?: number;
  offeringPhases?: PhaseTally;
  offeringPhaseConsumers?: PhaseTally;
  failedByOffering?: { type: string; services: number }[] | null;
  failedOfferingsBeyond?: number;
}

export function offeringsPhaseModel(agg: OfferingsAggregate | null | undefined) {
  const offerings = agg?.offeringCount ?? 0;
  const track = phaseTrack(agg?.offeringPhases, agg?.offeringPhaseConsumers);
  const failed = (agg?.failedByOffering ?? []).map((c) => ({ name: c.type, n: c.services }));
  const moreFailed = agg?.failedOfferingsBeyond ?? 0;
  const summary = track.requests
    ? `Of ${countOf(track.requests, 'request')} across ${countOf(offerings, 'offering')}, ${track.rest}`
    : `No consumer has asked a provider of ${offerings === 1 ? 'this offering' : `these ${countOf(offerings, 'offering')}`} for a resource yet.`;
  return {
    offerings, requests: track.requests, stations: track.stations, wires: track.wires, failed, moreFailed,
    // The failed list counts offerings, the Failed station requests (T3).
    failedLists: failed.length + moreFailed,
    summary,
  };
}
