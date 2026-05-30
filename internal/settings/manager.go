package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"

	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/internal/store"
)

type Manager struct {
	mu      sync.RWMutex
	cfg     config.Config
	baseDir string
	store   *store.Store
	onApply []func(config.Config)
}

func NewManager(st *store.Store, baseDir string) *Manager {
	return &Manager{
		cfg:     config.Default(baseDir),
		baseDir: baseDir,
		store:   st,
	}
}

func (m *Manager) OnApply(fn func(config.Config)) {
	m.onApply = append(m.onApply, fn)
}

func (m *Manager) Load(ctx context.Context) error {
	cfg := config.Default(m.baseDir)
	data, err := m.store.GetSettings(ctx)
	if err != nil {
		if err == sql.ErrNoRows {
			m.mu.Lock()
			m.cfg = cfg
			m.mu.Unlock()
			return nil
		}
		return err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	cfg.Normalize(m.baseDir)
	m.mu.Lock()
	m.cfg = cfg
	m.mu.Unlock()
	return nil
}

func (m *Manager) Get() config.Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

func (m *Manager) Save(ctx context.Context, cfg config.Config) error {
	cfg.Normalize(m.baseDir)
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := m.store.SaveSettings(ctx, data); err != nil {
		return err
	}
	m.mu.Lock()
	m.cfg = cfg
	m.mu.Unlock()
	for _, fn := range m.onApply {
		fn(cfg)
	}
	return nil
}

func (m *Manager) BaseDir() string {
	return m.baseDir
}
