/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/observer"
)

// observeSettings resolves probing and metrics observation for one target namespace. It reads the
// Namespace only while a mode is auto. Forbidden and NotFound are standing states that resolve off;
// any other read failure resolves off too and is returned, so the collection reads as incomplete.
// ponytail: one uncached GET per reconcile while a mode is auto; a namespace informer (list/watch) if apiserver load ever shows it.
func (r *PactoReconciler) observeSettings(ctx context.Context, namespace string) (probing, metrics observer.Setting, err error) {
	var labels map[string]string
	var readErr error
	if r.Probing == observer.ModeAuto || r.MetricsObservation == observer.ModeAuto {
		ns := &corev1.Namespace{}
		if readErr = r.APIReader.Get(ctx, types.NamespacedName{Name: namespace}, ns); readErr == nil {
			labels = ns.Labels
		}
	}
	probing = observer.Resolve(r.Probing, "enable-probing", "probing", labels, readErr)
	metrics = observer.Resolve(r.MetricsObservation, "enable-metrics-observation", "metrics", labels, readErr)
	if metrics.On && r.MetricsObservation == observer.ModeAuto {
		metrics.Source += "; ServiceMonitor discovery needs read access to monitoring.coreos.com monitors (controller.metricsObservation=on in the chart)"
	}
	if readErr != nil && !apierrors.IsForbidden(readErr) && !apierrors.IsNotFound(readErr) {
		// The message names the fixed reason the settings carry, never the raw error text.
		err = fmt.Errorf("namespace %s: observe label unreadable", namespace)
	}
	return probing, metrics, err
}

// observeMessage is the RuntimeObserved clause that names where each setting came from.
func observeMessage(probing, metrics observer.Setting) string {
	return clause("probing", probing) + "; " + clause("metrics", metrics)
}

func clause(feature string, s observer.Setting) string {
	state := "off"
	if s.On {
		state = "on"
	}
	return feature + " " + state + " (" + s.Source + ")"
}
