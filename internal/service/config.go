package service

import (
	"math"
	"time"

	"github.com/bochkarevart/1c-selection/internal/model"
)

type Config struct {
	Threshold    float64
	HalfLife     time.Duration
	SessionTTL   time.Duration
	Congratulate int
	Now          func() time.Time
}

func DefaultConfig() Config {
	return Config{
		Threshold:    1.1,
		HalfLife:     6 * time.Hour,
		SessionTTL:   24 * time.Hour,
		Congratulate: 3,
		Now:          time.Now,
	}
}

func (cfg Config) Validate() error {
	if math.IsNaN(cfg.Threshold) || math.IsInf(cfg.Threshold, 0) || cfg.Threshold <= 0 {
		return model.NewError("invalid", "publication threshold must be finite and positive")
	}
	if cfg.HalfLife <= 0 || cfg.Congratulate < 0 {
		return model.NewError("invalid", "half-life must be positive and congratulations count non-negative")
	}
	if cfg.SessionTTL <= 0 {
		return model.NewError("invalid", "session TTL must be positive")
	}
	if cfg.Now == nil {
		return model.NewError("invalid", "clock function is required")
	}
	return nil
}
