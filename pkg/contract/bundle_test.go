package contract

import (
	"testing"
	"testing/fstest"
)

func TestBundleRaw(t *testing.T) {
	tests := []struct {
		name    string
		bundle  Bundle
		want    string
		wantErr bool
	}{
		{
			name:   "prefers RawYAML",
			bundle: Bundle{RawYAML: []byte("from-raw"), FS: fstest.MapFS{"pacto.yaml": {Data: []byte("from-fs")}}},
			want:   "from-raw",
		},
		{
			name:   "falls back to the filesystem",
			bundle: Bundle{FS: fstest.MapFS{"pacto.yaml": {Data: []byte("from-fs")}}},
			want:   "from-fs",
		},
		{
			name:    "empty RawYAML with no filesystem is an error",
			bundle:  Bundle{},
			wantErr: true,
		},
		{
			name:    "missing document in the filesystem is an error",
			bundle:  Bundle{FS: fstest.MapFS{}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.bundle.Raw()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Raw() error = nil, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Raw() error = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("Raw() = %q, want %q", got, tt.want)
			}
		})
	}
}
