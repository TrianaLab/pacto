package delivery

// AutoPromoteHold is why the promoter does not raise an automatic promote on
// a Stage, or "" when it does (unless an operation is already in flight, which
// the promoter checks against the cluster). The promoter and the "why not
// yet" answer share it, so the reason shown is the promoter's own.
func AutoPromoteHold(spec StageSpec, status StageStatus) string {
	level := CurrentLevel(spec, status)
	switch {
	case level == LevelObserve:
		return "the Stage observes: GitOps owns the applyRef and Pacto only plans what it would do"
	case !level.Automatic():
		return "the Stage's level is " + string(level) + ": a person proposes each promote"
	case status.Frozen:
		return "the Stage is frozen: " + status.FrozenReason
	case status.AutoHold:
		return "the Stage is on auto-hold: a person rolled back or promoted back to a version it verified, so the promoter " +
			"does not promote on its own until someone unfreezes it"
	case status.Candidate == "":
		return "the Stage has no eligible candidate"
	}
	return ""
}
