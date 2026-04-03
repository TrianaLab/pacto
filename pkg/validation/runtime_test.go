package validation_test

import (
	"testing"

	"github.com/trianalab/pacto/pkg/contract"
	"github.com/trianalab/pacto/pkg/validation"
)

func intPtr(v int) *int { return &v }

func TestValidateRuntime_PortMatch(t *testing.T) {
	c := &contract.Contract{
		Interfaces: []contract.Interface{
			{Name: "api", Type: "http", Port: intPtr(8080)},
		},
	}

	ctx := validation.RuntimeContext{
		Ports: []int{8080},
	}

	result := validation.ValidateRuntime(c, ctx)
	if !result.IsValid() {
		t.Errorf("expected valid, got errors: %v", result.Errors)
	}
}

func TestValidateRuntime_PortMissing(t *testing.T) {
	c := &contract.Contract{
		Interfaces: []contract.Interface{
			{Name: "api", Type: "http", Port: intPtr(8080)},
		},
	}

	ctx := validation.RuntimeContext{
		Ports: []int{9090},
	}

	result := validation.ValidateRuntime(c, ctx)
	if result.IsValid() {
		t.Fatal("expected error for missing port")
	}
	if len(result.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(result.Errors))
	}
	if result.Errors[0].Code != "PORT_NOT_OBSERVED" {
		t.Errorf("expected PORT_NOT_OBSERVED, got %s", result.Errors[0].Code)
	}
}

func TestValidateRuntime_EmptyPortsSkipsCheck(t *testing.T) {
	c := &contract.Contract{
		Interfaces: []contract.Interface{
			{Name: "api", Type: "http", Port: intPtr(8080)},
		},
	}

	ctx := validation.RuntimeContext{}

	result := validation.ValidateRuntime(c, ctx)
	if !result.IsValid() {
		t.Errorf("expected valid when no ports observed, got errors: %v", result.Errors)
	}
}

func TestValidateRuntime_ConfigPresent(t *testing.T) {
	c := &contract.Contract{
		Configurations: []contract.ConfigurationSource{
			{
				Name: "default",
				Values: map[string]interface{}{
					"DB_HOST": "localhost",
				},
			},
		},
	}

	ctx := validation.RuntimeContext{
		EnvVars: map[string]string{
			"DB_HOST": "prod-db.example.com",
		},
	}

	result := validation.ValidateRuntime(c, ctx)
	if !result.IsValid() {
		t.Errorf("expected valid, got errors: %v", result.Errors)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("expected no warnings, got %v", result.Warnings)
	}
}

func TestValidateRuntime_ConfigMissing(t *testing.T) {
	c := &contract.Contract{
		Configurations: []contract.ConfigurationSource{
			{
				Name: "default",
				Values: map[string]interface{}{
					"DB_HOST": "localhost",
				},
			},
		},
	}

	ctx := validation.RuntimeContext{
		EnvVars: map[string]string{
			"OTHER_VAR": "value",
		},
	}

	result := validation.ValidateRuntime(c, ctx)
	if len(result.Warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(result.Warnings))
	}
	if result.Warnings[0].Code != "CONFIG_NOT_OBSERVED" {
		t.Errorf("expected CONFIG_NOT_OBSERVED, got %s", result.Warnings[0].Code)
	}
}

func TestValidateRuntime_NilConfigSkipsCheck(t *testing.T) {
	c := &contract.Contract{}
	ctx := validation.RuntimeContext{
		EnvVars: map[string]string{"FOO": "bar"},
	}

	result := validation.ValidateRuntime(c, ctx)
	if !result.IsValid() {
		t.Errorf("expected valid, got errors: %v", result.Errors)
	}
}

func TestValidateRuntime_MultiplePorts(t *testing.T) {
	c := &contract.Contract{
		Interfaces: []contract.Interface{
			{Name: "api", Type: "http", Port: intPtr(8080)},
			{Name: "grpc", Type: "grpc", Port: intPtr(9090)},
			{Name: "events", Type: "event"},
		},
	}

	ctx := validation.RuntimeContext{
		Ports: []int{8080, 9090},
	}

	result := validation.ValidateRuntime(c, ctx)
	if !result.IsValid() {
		t.Errorf("expected valid, got errors: %v", result.Errors)
	}
}

func TestValidateRuntime_ConfigValuesWithEmptyEnvVars(t *testing.T) {
	c := &contract.Contract{
		Configurations: []contract.ConfigurationSource{
			{
				Name: "default",
				Values: map[string]interface{}{
					"DB_HOST": "localhost",
				},
			},
		},
	}

	ctx := validation.RuntimeContext{}

	result := validation.ValidateRuntime(c, ctx)
	if !result.IsValid() {
		t.Errorf("expected valid when no env vars observed, got errors: %v", result.Errors)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("expected no warnings when env vars not provided, got %v", result.Warnings)
	}
}

func TestValidateRuntime_MultiConfigs(t *testing.T) {
	c := &contract.Contract{
		Configurations: []contract.ConfigurationSource{
			{
				Name:   "app",
				Values: map[string]interface{}{"APP_PORT": "8080"},
			},
			{
				Name:   "db",
				Values: map[string]interface{}{"DB_HOST": "localhost"},
			},
		},
	}

	ctx := validation.RuntimeContext{
		EnvVars: map[string]string{
			"APP_PORT": "8080",
		},
	}

	result := validation.ValidateRuntime(c, ctx)
	if !result.IsValid() {
		t.Errorf("expected valid, got errors: %v", result.Errors)
	}
	// DB_HOST not in env -> should produce warning
	if len(result.Warnings) != 1 {
		t.Fatalf("expected 1 warning for missing DB_HOST, got %d", len(result.Warnings))
	}
	if result.Warnings[0].Code != "CONFIG_NOT_OBSERVED" {
		t.Errorf("expected CONFIG_NOT_OBSERVED, got %s", result.Warnings[0].Code)
	}
}

func TestValidateRuntime_PartialPortMatch(t *testing.T) {
	c := &contract.Contract{
		Interfaces: []contract.Interface{
			{Name: "api", Type: "http", Port: intPtr(8080)},
			{Name: "grpc", Type: "grpc", Port: intPtr(9090)},
		},
	}

	ctx := validation.RuntimeContext{
		Ports: []int{8080},
	}

	result := validation.ValidateRuntime(c, ctx)
	if result.IsValid() {
		t.Fatal("expected error for missing grpc port")
	}
	if len(result.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(result.Errors))
	}
}
