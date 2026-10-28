/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package component

import (
	"maps"
	"testing"
)

func TestLabels(t *testing.T) {
	if got := Labels("dashboard", "pacto-dashboard"); !maps.Equal(got, map[string]string{
		"app.kubernetes.io/managed-by": "pacto-operator", "app.kubernetes.io/component": "dashboard", "app.kubernetes.io/name": "pacto-dashboard"}) {
		t.Errorf("Labels = %v", got)
	}
	if got := SelectorLabels("dashboard", "pacto-dashboard"); !maps.Equal(got, map[string]string{
		"app.kubernetes.io/component": "dashboard", "app.kubernetes.io/name": "pacto-dashboard"}) {
		t.Errorf("SelectorLabels = %v", got)
	}
	if got := Owned("dashboard"); !maps.Equal(got, map[string]string{
		"app.kubernetes.io/managed-by": "pacto-operator", "app.kubernetes.io/component": "dashboard"}) {
		t.Errorf("Owned = %v", got)
	}
}

func TestRestrictedContainerSecurityContext(t *testing.T) {
	sc := RestrictedContainerSecurityContext()
	if !*sc.ReadOnlyRootFilesystem || *sc.AllowPrivilegeEscalation || len(sc.Capabilities.Drop) != 1 || sc.Capabilities.Drop[0] != "ALL" {
		t.Errorf("security context = %+v", sc)
	}
}
