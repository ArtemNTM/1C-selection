package cli

import "time"

type Config struct {
	Server         string
	StateDir       string
	RequestTimeout time.Duration
	ClientID       string
	Token          string
}
