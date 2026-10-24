package delivery

import (
	"fmt"
	"slices"
	"strings"
)

// UpstreamFromRecords rebuilds what a Stage in another cluster verified from
// its stream's verified records, the way the promoter keeps it on a local
// Stage: an operation that Succeeded and Verified adds its version to
// Verified, and a promote adds it to Offered too when pacto.published passed
// there; a Failed operation of any type adds its version to FailedDigests and
// takes it out of Verified. A rollback raised on a Fail verdict, the one a
// failed post-release re-check raises, marks the version it leaves failed the
// same way, and so does the freeze such a re-check raises when it cannot roll
// back: that freeze names the failed version it stays on. unreadable names the
// records that failed verification. Any of those, or a gap or fork inside the
// stream, offers nothing: a downstream never promotes on history it cannot
// check, so the result is nil with the reason.
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
		s.foldUpstream(r.Statement.Predicate)
	}
	return s, ""
}

// foldUpstream applies one verified record to the remote view
// UpstreamFromRecords builds.
func (s *StageStatus) foldUpstream(p RecordPredicate) {
	if p.Kind != RecordOperation || p.Type == nil || p.Intent.Params == nil || p.Outcome == nil {
		return
	}
	if failedCause(p) {
		s.failUpstream(p.Intent.Params.From)
	}
	to := p.Intent.Params.To
	if to == "" { // a freeze or unfreeze moves no version
		return
	}
	switch p.Outcome.Result {
	case Succeeded:
		if p.Outcome.Verification != Verified {
			return
		}
		s.Verified = AddDigest(s.Verified, to, MaxVerified)
		if p.Type.ID == TypePromote && slices.ContainsFunc(p.Verdicts, func(v Verdict) bool { return v.Gate == GatePublished && v.Result == Pass }) {
			s.Offered = AddDigest(s.Offered, to, MaxOffered)
		}
	case Failed:
		s.failUpstream(to)
	}
}

// failedCause says whether p is a rollback or a freeze raised on a Fail
// verdict, which leaves the version it moves from, or stays on, failed. A
// freeze a person raised with no failed check behind it fails nothing.
func failedCause(p RecordPredicate) bool {
	c := p.Intent.Cause
	return (p.Type.ID == TypeRollback || p.Type.ID == TypeFreeze) && c != nil && c.Verdict != nil && c.Verdict.Result == Fail && p.Intent.Params.From != ""
}

// failUpstream marks d failed in the remote view.
func (s *StageStatus) failUpstream(d string) {
	s.FailedDigests = AddDigest(s.FailedDigests, d, MaxFailedDigests)
	s.Verified = RemoveDigest(s.Verified, d)
}

// upstreamName names a Stage's upstream for messages: a local Stage by
// namespace/name, a remote one by its stream id.
func upstreamName(c Candidates) string {
	if c.UpstreamRecords != nil {
		return fmt.Sprintf("%s (records in %s)", c.UpstreamRecords.Stream, c.UpstreamRecords.Repository)
	}
	return c.UpstreamStage.Namespace + "/" + c.UpstreamStage.Name
}
