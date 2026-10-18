/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package kubesink

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/pkg/runtimefacts"
)

var t0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func snapshot() runtimefacts.Snapshot {
	return runtimefacts.Snapshot{
		NodeName: "node-a", AgentVersion: "v1", PublishIntervalSeconds: 30, LastPublished: t0,
		Collection: runtimefacts.Collection{State: runtimefacts.StateRunning, DroppedEvents: 3},
		Workloads: []runtimefacts.Workload{
			{Identity: runtimefacts.Identity{Kind: runtimefacts.KindPod, ID: "uid-a"}, BaselineAt: t0,
				Listeners: []runtimefacts.Listener{{Port: 8080, Family: "IPv4", Address: "0.0.0.0", Process: "web"}}},
			{Identity: runtimefacts.Identity{Kind: runtimefacts.KindUnit, ID: "web.service", Name: "web.service"}},
		},
	}
}

// applied is one apply the fake client saw: subresource "" or "status".
type applied struct {
	sub string
	obj map[string]any
}

// fakeClient answers status applies with statusErrs in order (nil once
// exhausted) and the object apply with createErr, recording every apply. A
// Lease apply is recorded with sub "lease" and answered with leaseErr.
func fakeClient(t *testing.T, createErr error, statusErrs ...error) (*dynamicfake.FakeDynamicClient, *[]applied) {
	t.Helper()
	return fakeClientLease(t, nil, createErr, statusErrs...)
}

func fakeClientLease(t *testing.T, leaseErr, createErr error, statusErrs ...error) (*dynamicfake.FakeDynamicClient, *[]applied) {
	t.Helper()
	c := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{GVR: "NodeRuntimeObservationList", LeaseGVR: "LeaseList"})
	var seen []applied
	c.PrependReactor("patch", "leases", func(a k8stesting.Action) (bool, runtime.Object, error) {
		pa := a.(k8stesting.PatchAction)
		var obj map[string]any
		if err := json.Unmarshal(pa.GetPatch(), &obj); err != nil {
			t.Fatal(err)
		}
		if pa.GetNamespace() != "pacto-system" || pa.GetName() != "node-a" {
			t.Fatalf("lease apply to %s/%s", pa.GetNamespace(), pa.GetName())
		}
		seen = append(seen, applied{sub: "lease", obj: obj})
		return true, &unstructured.Unstructured{Object: obj}, leaseErr
	})
	c.PrependReactor("patch", "noderuntimeobservations", func(a k8stesting.Action) (bool, runtime.Object, error) {
		pa := a.(k8stesting.PatchAction)
		var obj map[string]any
		if err := json.Unmarshal(pa.GetPatch(), &obj); err != nil {
			t.Fatal(err)
		}
		if pa.GetNamespace() != "pacto-system" || pa.GetName() != "node-a" {
			t.Fatalf("apply to %s/%s", pa.GetNamespace(), pa.GetName())
		}
		seen = append(seen, applied{sub: pa.GetSubresource(), obj: obj})
		var err error
		if pa.GetSubresource() == "status" {
			if len(statusErrs) > 0 {
				err, statusErrs = statusErrs[0], statusErrs[1:]
			}
		} else {
			err = createErr
		}
		return true, &unstructured.Unstructured{Object: obj}, err
	})
	return c, &seen
}

func TestPublish_Existing(t *testing.T) {
	c, seen := fakeClient(t, nil)
	s := &Sink{Client: c, Namespace: "pacto-system", Name: "node-a"}
	if err := s.Publish(context.Background(), snapshot()); err != nil {
		t.Fatal(err)
	}
	if len(*seen) != 2 || (*seen)[0].sub != "status" || (*seen)[1].sub != "lease" {
		t.Fatalf("applies = %+v", *seen)
	}
	got, _ := json.Marshal((*seen)[0].obj["status"])
	want := `{"agentVersion":"v1","collection":{"droppedEvents":3,"state":"Running"},"lastPublished":"2026-10-18T12:00:00Z",` +
		`"nodeName":"node-a","pods":[{"baselineAt":"2026-10-18T12:00:00Z","listeners":[{"address":"0.0.0.0","family":"IPv4",` +
		`"port":8080,"process":"web"}],"uid":"uid-a"}],"publishIntervalSeconds":30}`
	if string(got) != want {
		t.Fatalf("status:\n got  %s\n want %s", got, want)
	}
	if (*seen)[0].obj["kind"] != "NodeRuntimeObservation" || (*seen)[0].obj["apiVersion"] != "pacto.trianalab.io/v1alpha1" {
		t.Fatalf("object = %+v", (*seen)[0].obj)
	}
}

func TestPublish_CreatesWithLabels(t *testing.T) {
	notFound := apierrors.NewNotFound(GVR.GroupResource(), "node-a")
	c, seen := fakeClient(t, nil, notFound)
	s := &Sink{Client: c, Namespace: "pacto-system", Name: "node-a"}
	if err := s.Publish(context.Background(), snapshot()); err != nil {
		t.Fatal(err)
	}
	if len(*seen) != 4 || (*seen)[1].sub != "" || (*seen)[2].sub != "status" || (*seen)[3].sub != "lease" {
		t.Fatalf("applies = %+v", *seen)
	}
	labels := (*seen)[1].obj["metadata"].(map[string]any)["labels"].(map[string]any)
	for k, v := range Labels() {
		if labels[k] != v {
			t.Errorf("label %s = %v, want %s", k, labels[k], v)
		}
	}
	if _, ok := (*seen)[1].obj["status"]; ok {
		t.Fatal("the create apply carries no status")
	}
}

func TestPublish_Errors(t *testing.T) {
	notFound := apierrors.NewNotFound(GVR.GroupResource(), "node-a")
	boom := errors.New("boom")
	for name, tc := range map[string]struct {
		createErr  error
		statusErrs []error
	}{
		"status apply fails":  {nil, []error{boom}},
		"create fails":        {boom, []error{notFound}},
		"status after create": {nil, []error{notFound, boom}},
	} {
		// A failed status write never renews the Lease: the heartbeat would vouch for a stale status.
		t.Run(name, func(t *testing.T) {
			c, seen := fakeClient(t, tc.createErr, tc.statusErrs...)
			s := &Sink{Client: c, Namespace: "pacto-system", Name: "node-a"}
			if err := s.Publish(context.Background(), snapshot()); !errors.Is(err, boom) {
				t.Fatalf("err = %v, want boom", err)
			}
			for _, a := range *seen {
				if a.sub == "lease" {
					t.Fatal("the Lease was renewed after a failed status write")
				}
			}
		})
	}
}

// The Lease is the heartbeat: held by the node, three publish intervals long,
// renewed at the snapshot's time, labelled like the observation.
func TestPublish_LeaseShape(t *testing.T) {
	c, seen := fakeClient(t, nil)
	s := &Sink{Client: c, Namespace: "pacto-system", Name: "node-a"}
	if err := s.Publish(context.Background(), snapshot()); err != nil {
		t.Fatal(err)
	}
	lease := (*seen)[1].obj
	if lease["kind"] != "Lease" || lease["apiVersion"] != "coordination.k8s.io/v1" {
		t.Fatalf("lease = %+v", lease)
	}
	spec, _ := json.Marshal(lease["spec"])
	if want := `{"holderIdentity":"node-a","leaseDurationSeconds":90,"renewTime":"2026-10-18T12:00:00.000000Z"}`; string(spec) != want {
		t.Fatalf("spec:\n got  %s\n want %s", spec, want)
	}
	labels := lease["metadata"].(map[string]any)["labels"].(map[string]any)
	for k, v := range Labels() {
		if labels[k] != v {
			t.Errorf("label %s = %v, want %s", k, labels[k], v)
		}
	}
}

// An unchanged status (lastPublished aside) is skipped and only the Lease is
// renewed, until RefreshEvery publishes have passed; a change is written at once.
func TestPublish_SkipsUnchangedStatus(t *testing.T) {
	c, seen := fakeClient(t, nil)
	s := &Sink{Client: c, Namespace: "pacto-system", Name: "node-a"}
	statuses := func() int {
		n := 0
		for _, a := range *seen {
			if a.sub == "status" {
				n++
			}
		}
		return n
	}
	snap := snapshot()
	for i := range RefreshEvery {
		snap.LastPublished = t0.Add(time.Duration(i) * 30 * time.Second)
		if err := s.Publish(context.Background(), snap); err != nil {
			t.Fatal(err)
		}
	}
	if got := statuses(); got != 1 {
		t.Fatalf("status writes over %d unchanged publishes = %d, want 1", RefreshEvery, got)
	}
	if err := s.Publish(context.Background(), snap); err != nil {
		t.Fatal(err)
	}
	if got := statuses(); got != 2 {
		t.Fatalf("the refresh is due: status writes = %d, want 2", got)
	}
	snap.Collection.DroppedEvents++
	if err := s.Publish(context.Background(), snap); err != nil {
		t.Fatal(err)
	}
	if got := statuses(); got != 3 {
		t.Fatalf("a changed status is written at once: status writes = %d, want 3", got)
	}
	if last := (*seen)[len(*seen)-1]; last.sub != "lease" {
		t.Fatalf("every publish renews the Lease, last apply = %+v", last)
	}
}

func TestPublish_LeaseError(t *testing.T) {
	boom := errors.New("boom")
	c, _ := fakeClientLease(t, boom, nil)
	s := &Sink{Client: c, Namespace: "pacto-system", Name: "node-a"}
	if err := s.Publish(context.Background(), snapshot()); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}
