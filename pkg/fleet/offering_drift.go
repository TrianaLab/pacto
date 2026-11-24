package fleet

import (
	"cmp"
	"encoding/json"
	"slices"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// Offering drift states (see [OfferingDrift.State]).
const (
	DriftDrifted = "drifted"
	DriftUnknown = "unknown"
)

// Bundle files an inferred offering cites, part of the public format: the
// source object the producer read. Generic: any producer that reads a
// cluster object may cite one.
const (
	offeringObjectFile = "source/object.yaml"
	// maxOfferingFileBytes bounds one read of a cited file.
	maxOfferingFileBytes = 256 << 10
	// maxOfferingClasses bounds the classes a row lists.
	maxOfferingClasses = 64
)

// OfferingDrift compares a declared offering accepted from an inferred one
// (its record names inferredFrom: offering:<producer>/<service>@<digest>)
// with the producer's newest record and what it last observed. Unknown is
// never in sync: a head that stopped moving (a source object deleted, an API
// no longer served, a run stopped at a bound) proves nothing.
type OfferingDrift struct {
	State          string     `json:"state" enum:"drifted,unknown" doc:"drifted: the producer's newest record differs from the one accepted; unknown: Pacto cannot tell"`
	Producer       string     `json:"producer" doc:"The producer the offering was accepted from"`
	AcceptedDigest string     `json:"acceptedDigest" doc:"The digest of the inferred record that was accepted"`
	NewestRef      string     `json:"newestRef,omitempty" doc:"The repository@digest of the producer's newest record; absent when unknown or hidden"`
	NewestVersion  string     `json:"newestVersion,omitempty" doc:"The version of the producer's newest record"`
	AsOf           *time.Time `json:"asOf,omitempty" doc:"When the producer last observed its source objects"`
	Reason         string     `json:"reason" doc:"Why the offering drifted or why Pacto cannot tell"`
}

// InferredOfferingFacts is what an inferred offering's list row says: the
// producer and offering, the source object it was read from (<kind> <name>
// as the bundle's source/object.yaml names it) and its generation, the
// classes its configuration schema offers, when the producer made it and,
// when a person accepted it, the declared service.
type InferredOfferingFacts struct {
	Producer   string     `json:"producer" doc:"The producer that contributed the inferred offering"`
	Offering   string     `json:"offering" doc:"The offering slug"`
	From       string     `json:"from,omitempty" doc:"The source object, <kind> <name>, as the bundle's source/object.yaml names it"`
	Generation int64      `json:"generation,omitempty" doc:"The source object's generation"`
	Classes    []string   `json:"classes,omitempty" doc:"The classes its configuration schema offers"`
	AsOf       *time.Time `json:"asOf,omitempty" doc:"When the producer made the record, from its signed manifest"`
	AcceptedAs *EntityRef `json:"acceptedAs,omitempty" doc:"The declared offering a person accepted it as, when visible"`
}

// recordHead keeps rev, a contribution just accepted, when it is its
// producer's newest record of its service: drift reads it after
// supersedeContributions may have dropped it.
func recordHead(snap *FleetSnapshot, rev *ContractRevision) {
	key := rev.contributor.Producer + "/" + rev.Service
	if old := snap.contributedHeads[key]; old != nil && !LessRevisionChrono(old, rev) {
		return
	}
	if snap.contributedHeads == nil {
		snap.contributedHeads = map[string]*ContractRevision{}
	}
	snap.contributedHeads[key] = rev
}

// ingestOfferingObservations keeps the observations a source advertised,
// one per producer; a second one of a producer leaves it unknown.
func ingestOfferingObservations(snap *FleetSnapshot, obs []OfferingObservation) {
	for _, o := range obs {
		if snap.offeringObs == nil {
			snap.offeringObs = map[string]OfferingObservation{}
		}
		if _, dup := snap.offeringObs[o.Producer]; dup {
			o.Unread = "more than one installation advertises producer " + o.Producer
		}
		snap.offeringObs[o.Producer] = o
	}
}

// markOfferings sets the facts of every contributed offering and the drift
// of every declared offering accepted from one, after the services are
// aggregated and their declarations accepted.
func markOfferings(snap *FleetSnapshot) {
	for _, s := range snap.Services {
		if s.Contribution != nil {
			if s.Offering != "" {
				s.inferredOffering = inferredOfferingFacts(snap, s)
			}
			continue
		}
		p, svc, d, ok := acceptedFrom(snap, s)
		if !ok {
			continue
		}
		s.offeringFrom, s.offeringDigest = p+"/"+svc, d
		s.OfferingDrift = offeringDrift(snap, p, svc, d)
	}
}

// acceptedFrom is the producer, service and digest the newest revision of s
// whose declaration names an offering inferredFrom was accepted from.
func acceptedFrom(snap *FleetSnapshot, s *ServiceRecord) (producer, service, digest string, ok bool) {
	var newest *ContractRevision
	for _, rk := range s.Revisions {
		r := snap.Revisions[rk]
		if r.Record == nil || r.Record.Declaration == nil {
			continue
		}
		if _, _, _, of := contract.InferredFromOffering(r.Record.Declaration.InferredFrom); of && (newest == nil || LessRevisionChrono(newest, r)) {
			newest = r
		}
	}
	if newest == nil {
		return "", "", "", false
	}
	return contract.InferredFromOffering(newest.Record.Declaration.InferredFrom)
}

// offeringDrift is the drift of an offering accepted from producer p's
// record digest d of service svc: drifted when p's newest record differs (a
// record re-pushed only to renew its age names d as the one it renews, so it
// is the same record), nil only for a fresh, complete observation that lists
// svc and a newest record equal to d, unknown with the reason otherwise. A
// read the producer reports incomplete, or a newest record read only because
// a newer one was refused, proves no drift either way.
func offeringDrift(snap *FleetSnapshot, p, svc, d string) *OfferingDrift {
	out := &OfferingDrift{State: DriftUnknown, Producer: p, AcceptedDigest: d}
	head := snap.contributedHeads[p+"/"+svc]
	if head != nil {
		out.NewestRef, out.NewestVersion = contract.TrimOCIScheme(head.ResolvedRef), head.Version
	}
	obs, observed := snap.offeringObs[p]
	if observed {
		out.AsOf = obs.AsOf
	}
	switch {
	case observed && obs.Unread != "":
		out.Reason = "the producer's records were not all read: " + obs.Unread
	case observed && obs.Partial != "":
		out.Reason = "the producer's last run stopped short: " + obs.Partial
	case head != nil && head.contributor != nil && head.contributor.NewerRefused != "":
		out.Reason = "the producer's newest record " + head.contributor.NewerRefused + " was refused"
	case head != nil && head.Digest != d && (head.contributor == nil || head.contributor.Renews != d):
		out.State, out.Reason = DriftDrifted, "source object changed since accepted"+generationNote(head)
	case !observed:
		out.Reason = "the producer reports no observation"
	case obs.AsOf == nil || snap.GeneratedAt.Sub(*obs.AsOf) > cmp.Or(snap.provisioningWindow, RecentEvidenceWindow):
		out.Reason = "observation is stale"
	case !slices.Contains(obs.Observed, svc):
		out.Reason = "source object no longer observed"
	case head == nil:
		out.Reason = "no record of the source object was read"
	default:
		return nil
	}
	return out
}

// generationNote is " (now generation N)" when head's bundle names its
// source object's generation, else "".
func generationNote(head *ContractRevision) string {
	if _, gen := offeringSource(head); gen > 0 {
		return " (now generation " + strconv.FormatInt(gen, 10) + ")"
	}
	return ""
}

// inferredOfferingFacts are the list facts of s, a contributed offering,
// read from its newest record.
func inferredOfferingFacts(snap *FleetSnapshot, s *ServiceRecord) *InferredOfferingFacts {
	newest := newestRevision(snap, s)
	from, gen := offeringSource(newest)
	return &InferredOfferingFacts{Producer: s.Contribution.Producer, Offering: s.Offering, From: from, Generation: gen,
		Classes: offeringClasses(newest), AsOf: s.Contribution.AsOf}
}

// SourceObject is the cluster object a contribution cites in its bundle's
// source/object.yaml: what a producer read it from.
type SourceObject struct {
	APIVersion string
	Kind       string
	Namespace  string
	Name       string
	Generation int64
}

// ContributionSource is the source object the newest record of s, a
// contributed service, cites; nil with why when its bundle holds none, is not
// retained or does not parse.
func ContributionSource(snap *FleetSnapshot, s *ServiceRecord) (*SourceObject, string) {
	return sourceObject(newestRevision(snap, s))
}

// sourceObject is the source object rev's bundle cites, nil with why when
// it holds none or it does not parse.
func sourceObject(rev *ContractRevision) (*SourceObject, string) {
	if rev == nil {
		return nil, "no record of the service was read"
	}
	data, why := readBundleFile(rev, offeringObjectFile, maxOfferingFileBytes)
	if why != "" {
		return nil, why
	}
	var obj struct {
		APIVersion string `yaml:"apiVersion"`
		Kind       string `yaml:"kind"`
		Metadata   struct {
			Namespace  string `yaml:"namespace"`
			Name       string `yaml:"name"`
			Generation int64  `yaml:"generation"`
		} `yaml:"metadata"`
	}
	if yaml.Unmarshal(data, &obj) != nil || obj.Kind == "" || obj.Metadata.Name == "" {
		return nil, offeringObjectFile + " names no kind and name"
	}
	return &SourceObject{APIVersion: obj.APIVersion, Kind: obj.Kind, Namespace: obj.Metadata.Namespace, Name: obj.Metadata.Name, Generation: obj.Metadata.Generation}, ""
}

// offeringSource is "<kind> <name>" and the generation of the source object
// rev's bundle cites, empty when it holds none or it does not parse.
func offeringSource(rev *ContractRevision) (string, int64) {
	o, _ := sourceObject(rev)
	if o == nil {
		return "", 0
	}
	return capLen(o.Kind+" "+o.Name, maxRuntimeValueLen), o.Generation
}

// offeringClasses are the class enum values of the first configuration
// schema of rev that offers one, sorted and bounded.
func offeringClasses(rev *ContractRevision) []string {
	for _, c := range rev.Contract.Configurations {
		data, why := readBundleFile(rev, c.Schema, maxOfferingFileBytes)
		var doc struct {
			Properties struct {
				Class struct {
					Enum []any `json:"enum"`
				} `json:"class"`
			} `json:"properties"`
		}
		if c.Schema == "" || why != "" || json.Unmarshal(data, &doc) != nil || len(doc.Properties.Class.Enum) == 0 {
			continue
		}
		var out []string
		for _, v := range doc.Properties.Class.Enum {
			if str, ok := v.(string); ok && len(out) < maxOfferingClasses {
				out = append(out, capLen(str, maxRuntimeValueLen))
			}
		}
		slices.Sort(out)
		return out
	}
	return nil
}

// acceptedOfferings maps each producer/service a visible declared offering
// was accepted from to that offering's service.
func (q *Query) acceptedOfferings() map[string]*ServiceRecord {
	out := map[string]*ServiceRecord{}
	for _, s := range q.snap.Services {
		if s.offeringFrom != "" && (out[s.offeringFrom] == nil || s.Key < out[s.offeringFrom].Key) {
			out[s.offeringFrom] = s
		}
	}
	return out
}

// inferredOfferingRef is s's list row with its inferred offering facts and
// the declared offering it was accepted as, when visible.
func (q *Query) inferredOfferingRef(s *ServiceRecord, accepted map[string]*ServiceRecord) EntityRef {
	ref := q.serviceListRef(s, OriginCount{})
	facts := *s.inferredOffering
	facts.Classes = slices.Clone(facts.Classes)
	if a := accepted[facts.Producer+"/"+s.Name]; a != nil {
		r := serviceEntityRef(a)
		facts.AcceptedAs = &r
	}
	ref.InferredOffering = &facts
	return ref
}
