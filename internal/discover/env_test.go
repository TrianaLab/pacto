package discover

import "testing"

// TestSensitiveLiteral: a $(NAME) reference holds no value of its own, so a
// value made only of references, or a URL whose password is one, is not a
// sensitive literal; a malformed credential URL still is.
func TestSensitiveLiteral(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		want        bool
	}{
		{"DB_PASSWORD", "hunter2", true},
		{"DB_PASSWORD", "$(OTHER_PASSWORD)", false},
		{"DB_PASSWORD", "", false},
		{"DATABASE_URL", "postgres://app:$(DB_PASSWORD)@db:5432/x", false},
		{"DATABASE_URL", "postgres://$(DB_USER):hunter2@db:5432/x", true},
		{"DATABASE_URL", "postgres://app:pa%ss@db:5432/x", true},
		{"DATABASE_URL", "app:pw@db:5432/x", true},
		{"LOG_LEVEL", "info", false},
	} {
		if got := SensitiveLiteral(tc.name, tc.value); got != tc.want {
			t.Errorf("SensitiveLiteral(%q, %q) = %v, want %v", tc.name, tc.value, got, tc.want)
		}
	}
}

func TestTypeOf(t *testing.T) {
	for v, want := range map[string]string{"8080": "integer", "true": "boolean", "false": "boolean", "info": "string", "": "string"} {
		if got := TypeOf(v); got != want {
			t.Errorf("TypeOf(%q) = %q, want %q", v, got, want)
		}
	}
}
