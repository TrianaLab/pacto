package delivery_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// streamsJoin is a valid join block of snapCluster to edit.
func streamsJoin(t *testing.T) *delivery.SnapshotJoin {
	t.Helper()
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	key, id := joinKey(t, pub)
	return &delivery.SnapshotJoin{Name: "eu-prod", NameSource: fleet.ClusterNameFromNodeLabel, KeyID: id, PublicKey: key}
}

// checkJoinCase checks j inside a snapshot: reject "" wants it accepted,
// else a join error naming reject.
func checkJoinCase(t *testing.T, j *delivery.SnapshotJoin, reject string) {
	t.Helper()
	err := joinSnapshot(j).Check()
	if reject == "" {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), reject) || !strings.HasPrefix(err.Error(), "join: ") {
		t.Fatalf("want a join error naming %q, got %v", reject, err)
	}
}

func TestCheckJoinRecorderKey(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	pemKey, _ := delivery.PublicKeyPEM(pub)
	for name, c := range map[string]struct {
		key    delivery.TrustedKey
		reject string
	}{
		"PEM Ed25519":     {delivery.TrustedKey{Name: "recorder", PublicKey: pemKey, Streams: []string{"pins/*/*"}}, ""},
		"keyless":         {delivery.TrustedKey{Identity: "https://ci.example/recorder", Issuer: "https://dex.example", TrustedRoot: "{}"}, ""},
		"garbage PEM":     {delivery.TrustedKey{PublicKey: "not a key"}, "recorder key"},
		"neither":         {delivery.TrustedKey{Name: "recorder"}, "recorder key"},
		"keyless no root": {delivery.TrustedKey{Identity: "x", Issuer: "https://dex.example"}, "recorder key"},
	} {
		t.Run(name, func(t *testing.T) {
			j := streamsJoin(t)
			j.RecorderKey = &c.key
			checkJoinCase(t, j, c.reject)
		})
	}
}

// TestCheckJoinWithoutRecorderKey: a wave-5 join block, with no recorder
// key, streams or requests, still checks.
func TestCheckJoinWithoutRecorderKey(t *testing.T) {
	checkJoinCase(t, streamsJoin(t), "")
}

func TestCheckJoinStreams(t *testing.T) {
	stream := func(id string) delivery.JoinStream {
		return delivery.JoinStream{Service: delivery.ServiceRef{Name: "billing"}, Environment: "production", Stream: id}
	}
	many := func(n int) []delivery.JoinStream {
		out := make([]delivery.JoinStream, n)
		for i := range out {
			out[i] = stream(fmt.Sprintf("%s/billing/s-%d", snapCluster, i))
		}
		return out
	}
	for name, c := range map[string]struct {
		streams []delivery.JoinStream
		repo    string
		reject  string
	}{
		"one stream":           {[]delivery.JoinStream{stream(snapCluster + "/billing/billing-production")}, "zot.test/acme/pacto-streams", ""},
		"200 streams":          {many(200), "r", ""},
		"201 streams":          {many(201), "r", "over the 200 bound"},
		"no repository":        {many(1), "", "no streams repository"},
		"another uid":          {[]delivery.JoinStream{stream("other-uid/billing/s")}, "r", "is not " + snapCluster},
		"pacto-targets":        {[]delivery.JoinStream{stream(snapCluster + "/pacto-targets/requests")}, "r", "outside pacto-targets"},
		"two parts":            {[]delivery.JoinStream{stream(snapCluster + "/billing")}, "r", "is not"},
		"bad name":             {[]delivery.JoinStream{stream(snapCluster + "/billing/Bad_Name")}, "r", "is not"},
		"bad environment":      {[]delivery.JoinStream{{Service: delivery.ServiceRef{Name: "billing"}, Environment: "Prod", Stream: snapCluster + "/billing/s"}}, "r", "DNS label"},
		"no service":           {[]delivery.JoinStream{{Environment: "prod", Stream: snapCluster + "/billing/s"}}, "r", "needs a service"},
		"cut and none written": {nil, "", ""},
	} {
		t.Run(name, func(t *testing.T) {
			j := streamsJoin(t)
			j.Streams, j.StreamsRepository, j.StreamsCut = c.streams, c.repo, c.streams == nil
			checkJoinCase(t, j, c.reject)
		})
	}
}

func TestCheckJoinRequests(t *testing.T) {
	for name, c := range map[string]struct {
		req    delivery.JoinRequests
		reject string
	}{
		"takes requests":        {delivery.JoinRequests{Issuers: []string{"https://dex.example"}, Environments: []string{"production"}}, ""},
		"issuer no URL":         {delivery.JoinRequests{Issuers: []string{"dex"}}, "requests issuer"},
		"bad environment":       {delivery.JoinRequests{Environments: []string{"Prod"}}, "requests environment"},
		"issuers at bound":      {delivery.JoinRequests{Issuers: slices.Repeat([]string{"https://dex.example"}, delivery.MaxJoinRequestEntries)}, ""},
		"too many issuers":      {delivery.JoinRequests{Issuers: slices.Repeat([]string{"https://dex.example"}, delivery.MaxJoinRequestEntries+1)}, "over the 64 bound"},
		"too many environments": {delivery.JoinRequests{Environments: slices.Repeat([]string{"production"}, delivery.MaxJoinRequestEntries+1)}, "over the 64 bound"},
	} {
		t.Run(name, func(t *testing.T) {
			j := streamsJoin(t)
			j.Requests = &c.req
			checkJoinCase(t, j, c.reject)
		})
	}
}

// TestJoinStateCarriesStreams: a self-signed join block's recorder key,
// streams and requests reach the state; one its own key did not sign
// carries none of them.
func TestJoinStateCarriesStreams(t *testing.T) {
	j := streamsJoin(t)
	j.RecorderKey = &delivery.TrustedKey{Name: "recorder", PublicKey: "pem"}
	j.StreamsRepository, j.StreamsCut = "zot.test/acme/pacto-streams", true
	j.Streams = []delivery.JoinStream{{Service: delivery.ServiceRef{Name: "billing"}, Environment: "production", Stream: "m/billing/s"}}
	j.Requests = &delivery.JoinRequests{Environments: []string{"production"}}
	for _, self := range []bool{true, false} {
		st, _ := delivery.JoinStates("h", []delivery.JoinCandidate{{UID: "m", Join: j, SelfSigned: self}}, nil)
		got := st[0]
		carried := got.RecorderKey == j.RecorderKey && got.StreamsRepository == j.StreamsRepository && got.StreamsCut &&
			len(got.Streams) == 1 && got.Requests == j.Requests
		none := got.RecorderKey == nil && got.StreamsRepository == "" && !got.StreamsCut && got.Streams == nil && got.Requests == nil
		if (self && !carried) || (!self && !none) {
			t.Fatalf("self-signed %v: %+v", self, got)
		}
	}
}
