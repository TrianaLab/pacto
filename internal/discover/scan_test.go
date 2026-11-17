package discover

import "testing"

func TestImageRepo(t *testing.T) {
	for in, want := range map[string]string{"python:3.13": "docker.io/library/python", "acme/app": "docker.io/acme/app", "localhost:5000/app:1": "localhost:5000/app",
		"reg.io/a/b@sha256:abc": "reg.io/a/b"} {
		if got, _ := ImageRepo(in); got != want {
			t.Errorf("ImageRepo(%q) = %q, want %q", in, got, want)
		}
	}
}
