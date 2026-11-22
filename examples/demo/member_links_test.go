package main

import (
	"strconv"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// The demo's hub shows both identity models: eu-prod reviews through the
// shared issuer over a live channel, us-west under the group mapping alice
// approved with its channel down, which raises CLUSTER_ISSUER_DIFFERS. The
// pending ap-dev has no cluster record and so no link.
func TestDemoMemberLinksShowBothModels(t *testing.T) {
	snap := demoFleet(t)
	links := map[string]*fleet.ClusterLink{}
	for _, c := range snap.Clusters {
		links[c.Name] = c.Link
	}
	want := map[string]string{
		"eu-prod": "live " + fleet.IssuerShared + " " + demoIssuer + " ",
		"us-west": "down " + fleet.IssuerGroupMapping + " " + demoIssuer + " " + demoApprover,
		"ap-dev":  "",
	}
	for name, w := range want {
		if got := linkFacts(links[name]); got != w {
			t.Errorf("%s link = %q, want %q", name, got, w)
		}
	}
	l, err := fleet.NewQuery(snap).Attention(fleet.AttentionFilter{Category: "cluster", Limit: fleet.MaxAttentionLimit})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Items) != 1 || l.Items[0].Code != fleet.CodeClusterIssuerDiffers || l.Items[0].Label != "us-west" || !strings.Contains(l.Items[0].NextStep, demoApprover) {
		t.Errorf("cluster attention = %+v, want one CLUSTER_ISSUER_DIFFERS for us-west naming %s", l.Items, demoApprover)
	}
}

// linkFacts is a link's channel and its one issuer's model, issuer and
// approver; "" without a link.
func linkFacts(l *fleet.ClusterLink) string {
	if l == nil {
		return ""
	}
	if len(l.Issuers) != 1 {
		return l.Channel + " with " + strconv.Itoa(len(l.Issuers)) + " issuers"
	}
	m := l.Issuers[0]
	return strings.Join([]string{l.Channel, m.Model, m.Issuer, m.MappedBy}, " ")
}
