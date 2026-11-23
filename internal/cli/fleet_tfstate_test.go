package cli_test

import (
	"strings"
	"testing"
)

func TestFleetTFStateFlag(t *testing.T) {
	out, _, err := execFleet(t, "fleet", "get", "external:aws/rds/123456789012/eu-west-1/orders-db", "--local", "",
		"--tf-state", "../../pkg/managedres/testdata/state-v4.json", "--output-format", "json")
	if err != nil {
		t.Fatalf("get: %v\n%s", err, out)
	}
	for _, want := range []string{`"aws/rds/123456789012/eu-west-1/orders-db"`, `"aws_db_instance.orders"`, `"state-v4.json"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output misses %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, "hunter2") {
		t.Errorf("output holds a state secret:\n%s", out)
	}
}
