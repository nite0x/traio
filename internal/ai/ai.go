package ai

import (
	"context"
	"fmt"

	"github.com/nite/traio/internal/config"
)

type Service struct {
	cfg config.ClaudeConfig
}

func New(cfg config.ClaudeConfig) *Service {
	return &Service{cfg: cfg}
}

func (s *Service) SetConfig(cfg config.ClaudeConfig) {
	s.cfg = cfg
}

func (s *Service) AnalyzeTicker(ctx context.Context, symbol string, context string) (string, error) {
	_ = ctx
	if s.cfg.APIKey == "" {
		return "", fmt.Errorf("claude: api_key not configured")
	}
	_ = symbol
	_ = context
	return "", fmt.Errorf("claude: AnalyzeTicker not implemented")
}
