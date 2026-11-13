package delivery

import "testing"

func TestAutoPromoteHold(t *testing.T) {
	auto := StageStatus{Level: LevelAutonomous, Candidate: "sha256:b"}
	with := func(f func(*StageStatus)) StageStatus { s := auto; f(&s); return s }
	for _, c := range []struct {
		name   string
		spec   StageSpec
		status StageStatus
		want   string
	}{
		{"observe", StageSpec{}, StageStatus{Candidate: "sha256:b"},
			"the Stage observes: GitOps owns the applyRef and Pacto only plans what it would do"},
		{"advise", StageSpec{}, with(func(s *StageStatus) { s.Level = LevelAdvise }),
			"the Stage's level is advise: a person proposes each promote"},
		{"start level advise", StageSpec{StartLevel: LevelAdvise}, StageStatus{Candidate: "sha256:b"},
			"the Stage's level is advise: a person proposes each promote"},
		{"frozen", StageSpec{}, with(func(s *StageStatus) { s.Frozen, s.FrozenReason = true, "rollback refused" }),
			"the Stage is frozen: rollback refused"},
		{"auto-hold", StageSpec{}, with(func(s *StageStatus) { s.AutoHold = true }),
			"the Stage is on auto-hold: a person rolled back or promoted back to a version it verified, so the promoter " +
				"does not promote on its own until someone unfreezes it"},
		{"no candidate", StageSpec{}, with(func(s *StageStatus) { s.Candidate = "" }), "the Stage has no eligible candidate"},
		{"auto-nonbreaking with a candidate", StageSpec{}, with(func(s *StageStatus) { s.Level = LevelAutoNonBreaking }), ""},
		{"autonomous with a candidate", StageSpec{}, auto, ""},
	} {
		if got := AutoPromoteHold(c.spec, c.status); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
