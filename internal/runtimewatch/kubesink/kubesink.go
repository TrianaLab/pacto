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
package kubesink

import (
	"context"
	"encoding/json"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/trianalab/pacto/v3/pkg/runtimefacts"
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

// GVR is the NodeRuntimeObservation resource.
var GVR = schema.GroupVersionResource{Group: "pacto.trianalab.io", Version: "v1alpha1", Resource: "noderuntimeobservations"}

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
}

func (s *Sink) object() *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(GVR.GroupVersion().String())
	u.SetKind("NodeRuntimeObservation")
	u.SetNamespace(s.Namespace)
	u.SetName(s.Name)
	return u
}

// Publish applies the snapshot's pods as the status, creating the object first
// when it does not exist (the status subresource cannot create it). Other
// workload kinds have no place in the resource and are left out.
func (s *Sink) Publish(ctx context.Context, snap runtimefacts.Snapshot) error {
	var pods []runtimefacts.Workload
	for _, w := range snap.Workloads {
		if w.Kind == runtimefacts.KindPod {
			pods = append(pods, w)
		}
	}
	snap.Workloads = pods
	// Plain fields always encode and decode; the errors are unreachable.
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

func (s *Sink) applyStatus(ctx context.Context, u *unstructured.Unstructured) error {
	_, err := s.Client.Resource(GVR).Namespace(s.Namespace).ApplyStatus(ctx, s.Name, u,
		metav1.ApplyOptions{FieldManager: FieldOwner, Force: true})
	if err != nil {
		return fmt.Errorf("apply status %s/%s: %w", s.Namespace, s.Name, err)
	}
	return nil
}
