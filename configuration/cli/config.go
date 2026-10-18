package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	client "github.com/bochkarevart/1c-selection/internal/cli"
	"github.com/bochkarevart/1c-selection/internal/configfile"
	"github.com/spf13/pflag"
)

const (
	defaultConfigPath = "configuration/cli/config.yaml"
	defaultTokenFile  = ".CLI_TOKEN"
	tokenEnvName      = "TIMES_CLI_TOKEN"
)

type Config struct {
	Server         string        `yaml:"server"`
	StateDir       string        `yaml:"state_dir"`
	RequestTimeout time.Duration `yaml:"request_timeout"`
	ClientID       string        `yaml:"client_id"`
	Token          string        `yaml:"token"`
	TokenFile      string        `yaml:"token_file"`
}

func defaultConfig() Config {
	dir := ""
	if config, err := os.UserConfigDir(); err == nil {
		dir = filepath.Join(config, "times")
	}
	return Config{
		Server:         "http://127.0.0.1:8080",
		StateDir:       dir,
		RequestTimeout: 20 * time.Second,
		TokenFile:      defaultTokenFile,
	}
}

func loadConfig(path string, flags *pflag.FlagSet, overrides Config) (Config, error) {
	cfg := defaultConfig()
	if err := configfile.Load(path, flags.Changed("config"), &cfg); err != nil {
		return Config{}, err
	}
	configfile.Override(flags, "server", &cfg.Server, overrides.Server)
	configfile.Override(flags, "state-dir", &cfg.StateDir, overrides.StateDir)
	configfile.Override(flags, "request-timeout", &cfg.RequestTimeout, overrides.RequestTimeout)
	configfile.Override(flags, "client-id", &cfg.ClientID, overrides.ClientID)
	configfile.Override(flags, "token", &cfg.Token, overrides.Token)
	configfile.Override(flags, "token-file", &cfg.TokenFile, overrides.TokenFile)
	if flags.Changed("token") {
		return cfg, nil
	}
	if token := strings.TrimSpace(os.Getenv(tokenEnvName)); token != "" {
		cfg.Token = token
		return cfg, nil
	}
	if cfg.TokenFile == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(cfg.TokenFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && cfg.TokenFile == defaultTokenFile && !flags.Changed("token-file") {
			return cfg, nil
		}
		return Config{}, fmt.Errorf("read token file %q: %w", cfg.TokenFile, err)
	}
	cfg.Token = strings.TrimSpace(string(data))
	if cfg.Token == "" {
		return Config{}, fmt.Errorf("token file %q is empty", cfg.TokenFile)
	}
	return cfg, nil
}

func (cfg Config) clientConfig() client.Config {
	return client.Config{
		Server:         cfg.Server,
		StateDir:       cfg.StateDir,
		RequestTimeout: cfg.RequestTimeout,
		ClientID:       cfg.ClientID,
		Token:          cfg.Token,
	}
}
