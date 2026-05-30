package news

import (
	"context"
	"fmt"
	"time"

	"github.com/nite/traio/internal/config"
)

type Article struct {
	ID        string    `json:"id"`
	Symbol    string    `json:"symbol"`
	Headline  string    `json:"headline"`
	Summary   string    `json:"summary"`
	Source    string    `json:"source"`
	URL       string    `json:"url"`
	Published time.Time `json:"published"`
}

type Service struct {
	cfg config.FinnhubConfig
}

func New(cfg config.FinnhubConfig) *Service {
	return &Service{cfg: cfg}
}

func (s *Service) SetConfig(cfg config.FinnhubConfig) {
	s.cfg = cfg
}

func (s *Service) BySymbol(ctx context.Context, symbol string, limit int) ([]Article, error) {
	_ = ctx
	if s.cfg.APIKey == "" {
		return nil, fmt.Errorf("finnhub: api_key not configured")
	}
	_ = symbol
	_ = limit
	return nil, fmt.Errorf("finnhub: BySymbol not implemented")
}
