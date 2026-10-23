// Command storybeats prints examples/story's Beats as one JSON object keyed by
// beat id, so the Kubernetes demo (delivery-demo.sh) checks the cluster against
// the very table the story test proves, and fails on drift instead of keeping
// a second copy of the expectations.
package main

import (
	"encoding/json"
	"os"

	"github.com/trianalab/pacto/v4/examples/story"
)

func main() {
	byID := map[string]story.Beat{}
	for _, b := range story.Beats {
		byID[b.ID] = b
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(byID); err != nil {
		os.Exit(1)
	}
}
