/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package kubesink is the Kubernetes sink of `pacto agent`: it writes each
// snapshot as the status of the node's NodeRuntimeObservation through the
// dynamic client, so the core module never imports the operator's API types.
// A snapshot's pod projection is that status field for field
// (runtimefacts.Snapshot's wire form; the operator pins it with a parity test).
//
// The heartbeat is a coordination.k8s.io Lease named after the node, renewed
// every publish. The status itself is written only when its content changed
// (lastPublished aside) or every RefreshEvery publishes, so a quiet node costs
// one small Lease write per interval instead of its whole observation.
package kubesink

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/trianalab/pacto/v4/pkg/runtimefacts"
)

const (
	// FieldOwner is the server-side apply field manager of the node agent.
	FieldOwner = "pacto-node-agent"

	// Labels on every NodeRuntimeObservation, so the operator can select and
	// prune them. The operator's nodeagent package carries the same values.
	LabelManagedBy = "app.kubernetes.io/managed-by"
	LabelComponent = "app.kubernetes.io/component"
	LabelName      = "app.kubernetes.io/name"
	ManagedByValue = "pacto-operator"
	ComponentValue = "node-agent"
	NameValue      = "pacto-node-agent"
)

// RefreshEvery is how many publishes an unchanged status may be skipped for
// before it is written again anyway, so an object lost or edited behind the
// agent's back comes back within a bounded time.
const RefreshEvery = 10

// leaseIntervals is the Lease duration in publish intervals: the same bound the
// operator applies to a node's heartbeat by default.
const leaseIntervals = 3

// GVR is the NodeRuntimeObservation resource.
var GVR = schema.GroupVersionResource{Group: "pacto.trianalab.io", Version: "v1alpha1", Resource: "noderuntimeobservations"}

// LeaseGVR is the heartbeat resource.
var LeaseGVR = schema.GroupVersionResource{Group: "coordination.k8s.io", Version: "v1", Resource: "leases"}

// Labels returns the labels the agent applies to its object.
func Labels() map[string]string {
	return map[string]string{
		LabelManagedBy: ManagedByValue,
		LabelComponent: ComponentValue,
		LabelName:      NameValue,
	}
}

// Sink writes one node's NodeRuntimeObservation with server-side apply. The
// agent is the only writer of the object, so it forces ownership.
type Sink struct {
	Client    dynamic.Interface
	Namespace string
	// Name is the node name.
	Name string

	// last is the content of the last status written (lastPublished cleared),
	// skipped the publishes since.
	last    []byte
	skipped int
}

func (s *Sink) object() *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(GVR.GroupVersion().String())
	u.SetKind("NodeRuntimeObservation")
	u.SetNamespace(s.Namespace)
	u.SetName(s.Name)
	return u
}

// Publish writes the snapshot's pods as the status when they changed or are
// due a refresh, then renews the node's Lease. The Lease is renewed only after
// the status is known to be current, so a fresh heartbeat never vouches for a
// status the agent failed to write. Other workload kinds have no place in the
// resource and are left out.
func (s *Sink) Publish(ctx context.Context, snap runtimefacts.Snapshot) error {
	var pods []runtimefacts.Workload
	for _, w := range snap.Workloads {
		if w.Kind == runtimefacts.KindPod {
			pods = append(pods, w)
		}
	}
	snap.Workloads = pods
	content := snap
	content.LastPublished = time.Time{}
	// Plain fields always encode and decode; the errors are unreachable.
	key, _ := json.Marshal(content)
	if s.last == nil || !bytes.Equal(key, s.last) || s.skipped+1 >= RefreshEvery {
		if err := s.writeStatus(ctx, snap); err != nil {
			return err
		}
		s.last, s.skipped = key, 0
	} else {
		s.skipped++
	}
	return s.renew(ctx, snap)
}

// writeStatus applies the status, creating the object first when it does not
// exist (the status subresource cannot create it).
func (s *Sink) writeStatus(ctx context.Context, snap runtimefacts.Snapshot) error {
	b, _ := json.Marshal(snap)
	var status map[string]any
	_ = json.Unmarshal(b, &status)
	st := s.object()
	st.Object["status"] = status

	err := s.applyStatus(ctx, st)
	if !apierrors.IsNotFound(err) {
		return err
	}
	obj := s.object()
	obj.SetLabels(Labels())
	if _, err := s.Client.Resource(GVR).Namespace(s.Namespace).Apply(ctx, s.Name, obj,
		metav1.ApplyOptions{FieldManager: FieldOwner, Force: true}); err != nil {
		return fmt.Errorf("create %s/%s: %w", s.Namespace, s.Name, err)
	}
	return s.applyStatus(ctx, st)
}

// renew applies the node's Lease: held by the node, valid for three publish
// intervals, renewed at the snapshot's time.
func (s *Sink) renew(ctx context.Context, snap runtimefacts.Snapshot) error {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(LeaseGVR.GroupVersion().String())
	u.SetKind("Lease")
	u.SetNamespace(s.Namespace)
	u.SetName(s.Name)
	u.SetLabels(Labels())
	u.Object["spec"] = map[string]any{
		"holderIdentity":       s.Name,
		"leaseDurationSeconds": int64(leaseIntervals * snap.PublishIntervalSeconds),
		"renewTime":            snap.LastPublished.UTC().Format(metav1.RFC3339Micro),
	}
	if _, err := s.Client.Resource(LeaseGVR).Namespace(s.Namespace).Apply(ctx, s.Name, u,
		metav1.ApplyOptions{FieldManager: FieldOwner, Force: true}); err != nil {
		return fmt.Errorf("renew lease %s/%s: %w", s.Namespace, s.Name, err)
	}
	return nil
}

func (s *Sink) applyStatus(ctx context.Context, u *unstructured.Unstructured) error {
	_, err := s.Client.Resource(GVR).Namespace(s.Namespace).ApplyStatus(ctx, s.Name, u,
		metav1.ApplyOptions{FieldManager: FieldOwner, Force: true})
	if err != nil {
		return fmt.Errorf("apply status %s/%s: %w", s.Namespace, s.Name, err)
	}
	return nil
}
