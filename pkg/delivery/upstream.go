package delivery

import (
	"fmt"
	"slices"
	"strings"
)

// UpstreamFromRecords rebuilds what a Stage in another cluster verified from
// its stream's verified records, the way the promoter keeps it on a local
// Stage: a promote that Succeeded and Verified adds its version to Verified,
// and to Offered when pacto.published passed there; a Failed one adds it to
// FailedDigests and takes it out of Verified. unreadable names the records
// that failed verification. Any of those, or a stream with a problem (a gap,
// a fork, a missing seq), offers nothing: a downstream never promotes on
// history it cannot check, so the result is nil with the reason.
func UpstreamFromRecords(recs []StoredRecord, unreadable []string) (*StageStatus, string) {
	if len(unreadable) > 0 {
		return nil, "records that could not be verified: " + strings.Join(unreadable, "; ")
	}
	f := FoldStream(recs)
	if len(f.Problems) > 0 {
		return nil, "the stream is not whole: " + strings.Join(f.Problems, "; ")
	}
	s := &StageStatus{}
	for _, r := range f.Records {
		p := r.Statement.Predicate
		if p.Kind != RecordOperation || p.Type == nil || p.Type.ID != TypePromote || p.Intent.Params == nil || p.Outcome == nil {
			continue
		}
		to := p.Intent.Params.To
		switch p.Outcome.Result {
		case Succeeded:
			if p.Outcome.Verification != Verified {
				continue
			}
			s.Verified = AddDigest(s.Verified, to, MaxVerified)
			if slices.ContainsFunc(p.Verdicts, func(v Verdict) bool { return v.Gate == GatePublished && v.Result == Pass }) {
				s.Offered = AddDigest(s.Offered, to, MaxOffered)
			}
		case Failed:
			s.FailedDigests = AddDigest(s.FailedDigests, to, MaxFailedDigests)
			s.Verified = RemoveDigest(s.Verified, to)
		}
	}
	return s, ""
}

// upstreamName names a Stage's upstream for messages: a local Stage by
// namespace/name, a remote one by its stream id.
func upstreamName(c Candidates) string {
	if c.UpstreamRecords != nil {
		return fmt.Sprintf("%s (records in %s)", c.UpstreamRecords.Stream, c.UpstreamRecords.Repository)
	}
	return c.UpstreamStage.Namespace + "/" + c.UpstreamStage.Name
}
