package story

import "slices"

// updaters are the namespaces where each person may update workloads, as
// edit RoleBindings grant it on the kind demo: alice and carol in test and
// every production namespace, dana in test only. Agents and the promoter
// update nothing by hand.
var updaters = map[string][]string{
	Alice.Name: {"test", "prod", "prod-eu", "prod-us"},
	Carol.Name: {"test", "prod", "prod-eu", "prod-us"},
	Dana.Name:  {"test"},
}

// MayUpdate says whether p may update workloads in namespace ns: what a
// SelfSubjectAccessReview answers for p there.
func MayUpdate(p Persona, ns string) bool {
	return slices.Contains(updaters[p.Name], ns)
}
