package app

// BuildVersion is the pacto CLI version, recorded into pacto.lock provenance.
// It is set from main via SetBuildVersion (Task 9); defaults to "dev".
var BuildVersion = "dev"

// SetBuildVersion records the running CLI version for lock provenance.
func SetBuildVersion(v string) { BuildVersion = v }
