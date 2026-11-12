package delivery

import (
	"cmp"
	"fmt"
	"reflect"
	"testing"
)

// stage is a Stage of service in environment ns.
func stage(ns, name, service string, st StageStatus) StageObject {
	return StageObject{Metadata: ObjectMeta{Namespace: ns, Name: name},
		Spec: StageSpec{Service: ServiceRef{Name: service}, Environment: ns}, Status: st}
}

// fed is a Stage in env whose candidates come from the Stage upNS/upName.
func fed(env, name, upNS, upName string) StageObject {
	st := stage(env, name, name, StageStatus{})
	if upName != "" {
		st.Spec.Candidates.UpstreamStage = &StageRef{Name: upName, Namespace: upNS}
	}
	return st
}

func TestPromotionOrderFollowsDeclaredUpstreams(t *testing.T) {
	cases := []struct {
		name      string
		stages    []StageObject
		order     string
		upstream  map[string][]string
		cycle     string
		unordered string
	}{
		{
			// Names alone would put prod before staging's "s" and qa first.
			name:     "a chain in no name order",
			stages:   []StageObject{fed("prod", "a", "staging", "a"), fed("qa", "a", "", ""), fed("staging", "a", "qa", "a")},
			order:    "[qa staging prod]",
			upstream: map[string][]string{"qa": {}, "staging": {"qa"}, "prod": {"qa", "staging"}},
			cycle:    "map[]",
		},
		{
			name: "a fork, and an upstream in the Stage's own namespace",
			stages: []StageObject{fed("dev", "a", "", ""), fed("prod-us", "a", "dev", "a"), fed("prod-eu", "a", "dev", "a"),
				{Metadata: ObjectMeta{Namespace: "dev", Name: "b"}, Spec: StageSpec{Environment: "dev-2",
					Candidates: Candidates{UpstreamStage: &StageRef{Name: "a"}}}}},
			order:    "[dev dev-2 prod-eu prod-us]",
			upstream: map[string][]string{"dev": {}, "dev-2": {"dev"}, "prod-eu": {"dev"}, "prod-us": {"dev"}},
			cycle:    "map[]",
		},
		{
			// c is free to go first; the cycle a <-> b is placed by name after
			// it and reported; 0, fed by b, comes after the cycle, not by name.
			name: "a dangling upstream orders nothing and a cycle is reported",
			stages: []StageObject{fed("b", "x", "a", "x"), fed("a", "x", "b", "x"), fed("c", "x", "nowhere", "x"),
				fed("0", "x", "b", "x")},
			order:    "[c a b 0]",
			upstream: map[string][]string{"0": {"a", "b"}, "a": {"b"}, "b": {"a"}, "c": {}},
			cycle:    "map[a:true b:true]",
		},
		{
			// Each service's path is acyclic; only together do they order eu
			// and us both ways. That is no cycle: it is placed by name.
			name:      "two services in opposite orders",
			stages:    []StageObject{fed("eu", "a", "", ""), fed("us", "a", "eu", "a"), fed("us", "b", "", ""), fed("eu", "b", "us", "b")},
			order:     "[eu us]",
			upstream:  map[string][]string{"eu": {"us"}, "us": {"eu"}},
			cycle:     "map[]",
			unordered: "map[eu:true us:true]",
		},
		{
			name:     "a Stage that names itself is on a cycle",
			stages:   []StageObject{fed("prod", "s", "prod", "s")},
			order:    "[prod]",
			upstream: map[string][]string{"prod": {}},
			cycle:    "map[prod:true]",
		},
	}
	for _, c := range cases {
		order, up, cycle, unordered := PromotionOrder(c.stages)
		if fmt.Sprint(unordered) != cmp.Or(c.unordered, "map[]") {
			t.Errorf("%s: unordered %v, want %s", c.name, unordered, c.unordered)
		}
		for k, v := range up {
			if len(v) == 0 {
				up[k] = []string{}
			}
		}
		if fmt.Sprint(order) != c.order || !reflect.DeepEqual(up, c.upstream) || fmt.Sprint(cycle) != c.cycle {
			t.Errorf("%s: order %v upstream %v cycle %v, want %s %v %s", c.name, order, up, cycle, c.order, c.upstream, c.cycle)
		}
	}
}
