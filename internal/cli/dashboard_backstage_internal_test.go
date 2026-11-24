package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
)

// TestDashboardBackstageFlagAndEnv pins --backstage-url on the dashboard: the
// flag and PACTO_DASHBOARD_BACKSTAGE_URL reach the fleet options with the token
// from PACTO_BACKSTAGE_TOKEN, the banner names the source, a Backstage catalog
// alone answers nothing, and a token over plain http off-host fails at startup.
func TestDashboardBackstageFlagAndEnv(t *testing.T) {
	t.Setenv("PACTO_DASHBOARD_BACKSTAGE_URL", "https://backstage.example.com")
	v := viper.New()
	v.SetEnvPrefix("PACTO")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	v.AutomaticEnv()
	cmd := newDashboardCommand(app.NewService(nil, nil), v, "test", defaultDeps())
	if cmd.Flag("backstage-url") == nil {
		t.Fatal("pacto dashboard does not declare --backstage-url")
	}
	if got := v.GetString("dashboard.backstage-url"); got != "https://backstage.example.com" {
		t.Errorf("dashboard.backstage-url = %q", got)
	}

	fopts := dashboardFleetOptions("", nil, nil, nil, dashboardSources{backstageURL: "https://b.example.com", backstageToken: "tok"})
	if fopts.BackstageURL != "https://b.example.com" || fopts.BackstageToken != "tok" {
		t.Errorf("BackstageURL, token set = %q, %v", fopts.BackstageURL, fopts.BackstageToken != "")
	}
	got := configuredSources(app.FleetOptions{MiraURLs: []string{"m"}, BackstageURL: "b", Detected: "d"})
	if !slices.Equal(got, []string{"mira", "backstage", "detection"}) {
		t.Errorf("configuredSources = %v", got)
	}
	for _, names := range [][]string{{"backstage"}, {"backstage", "detection"}} {
		if !answersNothing(names) {
			t.Errorf("answersNothing(%v) = false", names)
		}
	}
	if answersNothing([]string{"local", "backstage"}) {
		t.Error("a local source with Backstage answers something")
	}

	isolateHost(t)
	t.Setenv("PACTO_DASHBOARD_BACKSTAGE_URL", "")
	t.Setenv(backstageTokenEnv, "reader-token-value")
	_, err := runDashboard(t, app.NewService(nil, nil), viper.New(), t.TempDir(), "--port", "0", "--backstage-url", "http://backstage.acme.internal")
	if err == nil || !strings.Contains(err.Error(), "use https or run inside the cluster") || strings.Contains(err.Error(), "reader-token-value") {
		t.Fatalf("err = %v, want the CheckBackstageConfig refusal", err)
	}
}
