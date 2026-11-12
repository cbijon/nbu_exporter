package utils

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fjacquet/nbu_exporter/internal/models"
)

const reloadOneSite = `server:
  host: "0.0.0.0"
  port: "9440"
  uri: "/metrics"
  scrapingInterval: "1h"
  logName: "stdout"
nbuservers:
  - site: "primary"
    scheme: "https"
    uri: "/netbackup"
    host: "${RLD_NBU1_HOST}"
    port: "1556"
    apiKey: "${RLD_NBU1_KEY}"
    insecureSkipVerify: "${RLD_NBU1_SKIP:-false}"
`

const reloadSecondSite = `  - site: "secondary"
    scheme: "https"
    uri: "/netbackup"
    host: "${RLD_NBU2_HOST}"
    port: "1556"
    apiKey: "${RLD_NBU2_KEY}"
    insecureSkipVerify: "${RLD_NBU2_SKIP:-false}"
`

func loadForReload(t *testing.T, path string) *models.SafeConfig {
	t.Helper()
	var cfg models.Config
	if err := ReadFile(&cfg, path); err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	sc := models.NewSafeConfig(&cfg)
	sc.Resolver = ResolveSecrets
	return sc
}

// A reload of an nbuservers: config with ${VAR} / ${VAR:-default} references must
// resolve them like startup does (it used to fail on the literal ${...} host), and
// an unchanged site list must not be reported as a server change.
func TestReloadResolvesNbuServersEnv(t *testing.T) {
	t.Setenv("RLD_NBU1_HOST", "master1.example.com")
	t.Setenv("RLD_NBU1_KEY", "k1")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(reloadOneSite), 0o600); err != nil {
		t.Fatal(err)
	}
	sc := loadForReload(t, path)

	changed, err := sc.ReloadConfig(path)
	if err != nil {
		t.Fatalf("reload of an unchanged nbuservers config failed: %v", err)
	}
	if changed {
		t.Error("unchanged site list reported as a server change")
	}
	if got := sc.Get().NbuServers[0].Host; got != "master1.example.com" {
		t.Errorf("host not resolved on reload: %q", got)
	}
}

// Adding a second master is a server change (the collection loop needs a restart),
// even though the primary site is untouched.
func TestReloadDetectsAddedSite(t *testing.T) {
	t.Setenv("RLD_NBU1_HOST", "master1.example.com")
	t.Setenv("RLD_NBU1_KEY", "k1")
	t.Setenv("RLD_NBU2_HOST", "master2.example.com")
	t.Setenv("RLD_NBU2_KEY", "k2")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(reloadOneSite), 0o600); err != nil {
		t.Fatal(err)
	}
	sc := loadForReload(t, path)

	if err := os.WriteFile(path, []byte(reloadOneSite+reloadSecondSite), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := sc.ReloadConfig(path)
	if err != nil {
		t.Fatalf("reload with a second site failed: %v", err)
	}
	if !changed {
		t.Error("adding a site must be reported as a server change")
	}
	if n := len(sc.Get().NbuServers); n != 2 {
		t.Errorf("expected 2 sites after reload, got %d", n)
	}
}

// A second site whose variables are not set is rejected and the running config kept.
func TestReloadRejectsUnsetSecondSiteVars(t *testing.T) {
	t.Setenv("RLD_NBU1_HOST", "master1.example.com")
	t.Setenv("RLD_NBU1_KEY", "k1")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(reloadOneSite), 0o600); err != nil {
		t.Fatal(err)
	}
	sc := loadForReload(t, path)
	if err := os.WriteFile(path, []byte(reloadOneSite+reloadSecondSite), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := sc.ReloadConfig(path); err == nil {
		t.Fatal("expected an error for unset RLD_NBU2_* variables")
	}
	if n := len(sc.Get().NbuServers); n != 1 {
		t.Errorf("running config must be kept on a failed reload, got %d sites", n)
	}
}
