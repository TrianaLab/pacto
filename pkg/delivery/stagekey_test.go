package delivery

import "testing"

func TestStageKeyHomeBytesUnchanged(t *testing.T) {
	if k := StageKey(ObjectMeta{Namespace: "prod"}, "payments"); k != "prod/payments" {
		t.Fatalf("home key = %q", k)
	}
	if k := StageKey(ObjectMeta{Namespace: "prod", Cluster: "eu"}, "payments"); k != "eu/prod/payments" {
		t.Fatalf("remote key = %q", k)
	}
}

// Two Stages of the same name in two clusters stay two, and each follows
// the upstream in its own cluster.
func TestStagesOfTwoClustersKeepTheirUpstreams(t *testing.T) {
	at := func(cluster, ns, name, env, upstream string) StageObject {
		st := StageObject{Metadata: ObjectMeta{Cluster: cluster, Namespace: ns, Name: name}, Spec: StageSpec{Environment: env}}
		if upstream != "" {
			st.Spec.Candidates.UpstreamStage = &StageRef{Name: upstream}
		}
		return st
	}
	stages := []StageObject{at("", "p", "pay", "prod", "pay-dev"), at("", "p", "pay-dev", "dev", ""),
		at("eu", "p", "pay", "prod-eu", "pay-dev"), at("eu", "p", "pay-dev", "dev-eu", "")}
	byStage := StagesByName(stages)
	if len(byStage) != 4 {
		t.Fatalf("by stage = %v", byStage)
	}
	if u := UpstreamListed(&stages[2], byStage, func(string) bool { return true }, ""); u.Environment != "dev-eu" {
		t.Fatalf("eu upstream = %+v", u)
	}
	_, upstream, _, _ := PromotionOrder(stages)
	if up := upstream["prod-eu"]; len(up) != 1 || up[0] != "dev-eu" {
		t.Fatalf("upstream = %v", upstream)
	}
}
