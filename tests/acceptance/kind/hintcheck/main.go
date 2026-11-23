// Command hintcheck prints detect.FluxInstallHint, the compiled-in proposal
// every Applier row carries, so delivery-idp-flux.sh compares what the cluster
// shows with the constant instead of a copy of its text.
package main

import (
	"fmt"

	"github.com/trianalab/pacto/v4/pkg/detect"
)

func main() { fmt.Println(detect.FluxInstallHint) }
