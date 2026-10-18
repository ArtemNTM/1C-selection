package server

import (
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/bochkarevart/1c-selection/internal/configfile"
	"github.com/bochkarevart/1c-selection/internal/model"
	"github.com/bochkarevart/1c-selection/internal/service"
	"github.com/spf13/pflag"
)

const defaultConfigPath = "configuration/server/config.yaml"

type Config struct {
	TLSCertFile  string                         `yaml:"tls_cert_file"`
	TLSKeyFile   string                         `yaml:"tls_key_file"`
	Addr         string                         `yaml:"addr"`
	DatabaseURL  string                         `yaml:"database_url"`
	Threshold    float64                        `yaml:"threshold"`
	HalfLife     time.Duration                  `yaml:"half_life"`
	Congratulate int                            `yaml:"congratulate"`
	CacheRefresh time.Duration                  `yaml:"cache_refresh"`
	SessionTTL   time.Duration                  `yaml:"session_ttl"`
	Clients      []model.ProvisionClientRequest `yaml:"clients"`
}

func defaultConfig() Config {
	defaults := service.DefaultConfig()
	return Config{
		Addr:         "127.0.0.1:8080",
		DatabaseURL:  "postgres://times:times@127.0.0.1:54329/times?sslmode=disable",
		Threshold:    defaults.Threshold,
		HalfLife:     defaults.HalfLife,
		Congratulate: defaults.Congratulate,
		CacheRefresh: 5 * time.Minute,
		SessionTTL:   defaults.SessionTTL,
	}
}

func loadConfig(path string, flags *pflag.FlagSet, overrides Config) (Config, error) {
	cfg := defaultConfig()
	if err := configfile.Load(path, flags.Changed("config"), &cfg); err != nil {
		return Config{}, err
	}
	configfile.Override(flags, "addr", &cfg.Addr, overrides.Addr)
	configfile.Override(flags, "database-url", &cfg.DatabaseURL, overrides.DatabaseURL)
	configfile.Override(flags, "threshold", &cfg.Threshold, overrides.Threshold)
	configfile.Override(flags, "half-life", &cfg.HalfLife, overrides.HalfLife)
	configfile.Override(flags, "congratulate", &cfg.Congratulate, overrides.Congratulate)
	configfile.Override(flags, "cache-refresh", &cfg.CacheRefresh, overrides.CacheRefresh)
	configfile.Override(flags, "session-ttl", &cfg.SessionTTL, overrides.SessionTTL)
	return cfg, cfg.validate()
}

func (cfg Config) validate() error {
	u, err := url.Parse(cfg.DatabaseURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return errors.New("database_url must be a postgres:// or postgresql:// URL")
	}
	if strings.TrimSpace(cfg.Addr) == "" {
		return errors.New("addr must not be empty")
	}
	if cfg.CacheRefresh <= 0 {
		return errors.New("cache_refresh must be greater than zero")
	}
	if err := cfg.serviceConfig().Validate(); err != nil {
		return err
	}
	seen := make(map[string]bool)
	tokenFiles := make(map[string]bool)
	if (cfg.TLSCertFile == "") != (cfg.TLSKeyFile == "") {
		return errors.New("tls_cert_file and tls_key_file must be set together")
	}
	for _, client := range cfg.Clients {
		if err := service.ValidateProvisionClient(client); err != nil {
			return err
		}
		if seen[client.Name] {
			return errors.New("clients must have unique names")
		}
		seen[client.Name] = true
		if client.Token == "" && strings.TrimSpace(client.TokenFile) == "" {
			return errors.New("token_file is required to save a server-issued client token")
		}
		if client.Token != "" && client.TokenFile != "" {
			return errors.New("configure either token_file or a legacy token, not both")
		}
		if client.TokenFile != "" {
			path, err := filepath.Abs(client.TokenFile)
			if err != nil {
				return err
			}
			if tokenFiles[path] {
				return errors.New("clients must have separate token_file paths")
			}
			tokenFiles[path] = true
		}
	}
	return nil
}

func (cfg Config) serviceConfig() service.Config {
	return service.Config{
		Threshold:    cfg.Threshold,
		HalfLife:     cfg.HalfLife,
		Congratulate: cfg.Congratulate,
		SessionTTL:   cfg.SessionTTL,
		Now:          time.Now,
	}
}
