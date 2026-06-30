package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/go-playground/validator/v10"
	"gopkg.in/yaml.v3"
)

// EnvConfigPath overrides the config file location.
const EnvConfigPath = "STELLA_CONFIG_PATH"

// Manager owns the live Config and serialises access to it.
type Manager struct {
	mu       sync.RWMutex
	path     string
	cfg      *Config
	validate *validator.Validate
}

// NewManager loads the config at path, creating a default file if missing,
// ensuring data directories exist, and validating the result.
func NewManager(path string) (*Manager, error) {
	m := &Manager{path: path, validate: validator.New()}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		def := defaultConfig()
		m.cfg = &def
		if err := m.Save(); err != nil {
			return nil, fmt.Errorf("writing default config: %w", err)
		}
	} else if err := m.load(); err != nil {
		return nil, err
	}
	if err := m.ensureDirs(); err != nil {
		return nil, err
	}
	if err := m.validate.Struct(m.cfg); err != nil {
		return nil, fmt.Errorf("config validation: %w", err)
	}
	return m, nil
}

func (m *Manager) load() error {
	data, err := os.ReadFile(m.path)
	if err != nil {
		return fmt.Errorf("reading config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parsing config: %w", err)
	}
	m.cfg = &cfg
	return nil
}

// Get returns the current config. Read it through here everywhere.
func (m *Manager) Get() *Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

// Update mutates the config under lock and persists it.
func (m *Manager) Update(fn func(*Config)) error {
	m.mu.Lock()
	fn(m.cfg)
	m.mu.Unlock()
	return m.Save()
}

// Save writes the current config to disk.
func (m *Manager) Save() error {
	m.mu.RLock()
	data, err := yaml.Marshal(m.cfg)
	path := m.path
	m.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (m *Manager) ensureDirs() error {
	for _, d := range []string{m.cfg.DataPath, filepath.Dir(m.cfg.Database.Path)} {
		if d == "" {
			continue
		}
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("creating dir %q: %w", d, err)
		}
	}
	return nil
}
