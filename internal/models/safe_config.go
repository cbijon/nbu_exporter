// Package models defines the core data structures for the NBU exporter application.
package models

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

// envRefRe matches ${VAR} references; duplicated here to break the import cycle
// between models and utils (utils already imports models).
var envRefRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandEnv replaces ${VAR} references with env var values; fail-fast on unset vars.
func expandEnv(s string) (string, error) {
	var missing []string
	out := envRefRe.ReplaceAllStringFunc(s, func(match string) string {
		name := envRefRe.FindStringSubmatch(match)[1]
		val, ok := os.LookupEnv(name)
		if !ok {
			missing = append(missing, name)
			return ""
		}
		return val
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("environment variable(s) referenced in config but not set: %s", strings.Join(missing, ", "))
	}
	return out, nil
}

// SafeConfig provides thread-safe access to configuration.
// It uses RWMutex to allow concurrent reads while serializing writes.
// Pattern from Prometheus blackbox_exporter.
//
// SafeConfig enables dynamic configuration reload without restarting the exporter:
//   - Operators can update credentials or server addresses via SIGHUP
//   - File watchers can trigger automatic reload when config files change
//   - Invalid configurations are rejected without affecting the running config
//
// Usage:
//
//	cfg := &models.Config{...}
//	safeCfg := NewSafeConfig(cfg)
//
//	// Read (concurrent-safe)
//	current := safeCfg.Get()
//
//	// Reload (validates before applying)
//	changed, err := safeCfg.ReloadConfig("/path/to/config.yaml")
type SafeConfig struct {
	mu sync.RWMutex
	C  *Config
	// Resolver, when set, resolves ${VAR} / ${VAR:-default} references on a freshly
	// decoded config during ReloadConfig — the same resolution applied at startup
	// (utils.ResolveSecrets, injected by main to avoid an import cycle). When nil,
	// only the legacy nbuserver host/apiKey are expanded.
	Resolver func(*Config) error
}

// NewSafeConfig creates a new SafeConfig with the provided initial config.
// The config is stored by reference; the caller should not modify it after
// passing it to NewSafeConfig.
func NewSafeConfig(cfg *Config) *SafeConfig {
	return &SafeConfig{
		C: cfg,
	}
}

// Get returns the current configuration (read-locked).
// The returned pointer is safe to use until the next reload.
// Multiple goroutines can call Get() concurrently.
func (sc *SafeConfig) Get() *Config {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.C
}

// ReloadConfig loads and validates a new configuration from the file.
// Validation happens BEFORE acquiring write lock (fail-fast pattern).
// This ensures invalid configurations never affect the running exporter.
//
// Returns:
//   - serverChanged: true if NBU server address changed (signals cache flush needed)
//   - err: error if file cannot be read or validation fails
//
// Thread-safety:
// The write lock is held only for the pointer swap operation, minimizing
// contention. Validation and file I/O happen without holding any locks.
func (sc *SafeConfig) ReloadConfig(configPath string) (serverChanged bool, err error) {
	// Step 1: Validate config WITHOUT holding any locks

	// Check file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return false, fmt.Errorf("config file not found: %s", configPath)
	}

	// Read and parse config file
	f, err := os.Open(configPath)
	if err != nil {
		return false, fmt.Errorf("failed to open config: %w", err)
	}
	defer func() { _ = f.Close() }()

	var newCfg Config
	decoder := yaml.NewDecoder(f)
	if err := decoder.Decode(&newCfg); err != nil {
		return false, fmt.Errorf("failed to decode config: %w", err)
	}

	// Expand ${ENV} references in secret fields before validation.
	if sc.Resolver != nil {
		if err := sc.Resolver(&newCfg); err != nil {
			return false, err
		}
	} else {
		host, err := expandEnv(newCfg.NbuServer.Host)
		if err != nil {
			return false, fmt.Errorf("nbuserver.host: %w", err)
		}
		newCfg.NbuServer.Host = host

		apiKey, err := expandEnv(newCfg.NbuServer.APIKey)
		if err != nil {
			return false, fmt.Errorf("nbuserver.apiKey: %w", err)
		}
		newCfg.NbuServer.APIKey = apiKey
	}

	// Validate config
	if err := newCfg.Validate(); err != nil {
		return false, fmt.Errorf("config validation failed: %w", err)
	}

	// Step 2: Acquire write lock ONLY for pointer swap
	sc.mu.Lock()
	old := sc.C
	sc.C = &newCfg
	sc.mu.Unlock()

	// Any change to the site list (added/removed site, host, port, key, version,
	// TLS setting) needs the collection loop rebuilt — signal it to the caller.
	serverChanged = !sameServers(old, &newCfg)

	log.Info("Configuration reloaded successfully")
	if serverChanged {
		log.Info("NetBackup server list changed; storage cache will be flushed")
	}

	return serverChanged, nil
}

// serverKey is the comparable connection identity of one configured site.
type serverKey struct {
	Site, Scheme, Host, Port, URI, APIKey, APIVersion string
	Insecure                                          bool
}

func serverKeys(c *Config) []serverKey {
	if c == nil {
		return nil
	}
	keys := make([]serverKey, 0, len(c.NbuServers))
	for _, s := range c.NbuServers {
		keys = append(keys, serverKey{s.Site, s.Scheme, s.Host, s.Port, s.URI, s.APIKey, s.APIVersion, s.InsecureSkipVerify.Bool()})
	}
	return keys
}

// sameServers reports whether two configs target exactly the same NetBackup sites.
func sameServers(a, b *Config) bool {
	ka, kb := serverKeys(a), serverKeys(b)
	if len(ka) != len(kb) {
		return false
	}
	for i := range ka {
		if ka[i] != kb[i] {
			return false
		}
	}
	return true
}
