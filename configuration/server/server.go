package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/bochkarevart/1c-selection/internal/cache"
	"github.com/bochkarevart/1c-selection/internal/httpapi"
	"github.com/bochkarevart/1c-selection/internal/jobs"
	"github.com/bochkarevart/1c-selection/internal/service"
)

type sessionCleanupJob struct {
	service *service.Service
}

func (j sessionCleanupJob) Run(ctx context.Context) error {
	return j.service.DeleteExpiredSessions(ctx)
}

func Serve(ctx context.Context, cfg Config) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	var certificate tls.Certificate
	if cfg.TLSCertFile != "" {
		var err error
		certificate, err = tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			return fmt.Errorf("load TLS certificate: %w", err)
		}
	}
	db, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()

	serviceConfig := cfg.serviceConfig()
	c := cache.New(func(ctx context.Context) (cache.Snapshot, error) {
		return service.LoadCache(ctx, db, serviceConfig)
	}, cfg.HalfLife)
	startupCtx, cancelStartup := context.WithTimeout(ctx, 30*time.Second)
	err = c.Refresh(startupCtx)
	cancelStartup()
	if err != nil {
		return fmt.Errorf("initial cache refresh: %w", err)
	}
	svc := service.New(db, c, serviceConfig)
	handler, err := httpapi.New(svc)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()
	server := &http.Server{
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{certificate},
			MinVersion:   tls.VersionTLS12,
		},
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	jobCtx, cancelJobs := context.WithCancel(context.Background())
	var jobsDone sync.WaitGroup
	var stopJobs sync.Once
	for _, job := range []jobs.Job{c, sessionCleanupJob{service: svc}} {
		runner := jobs.Runner{
			Job:      job,
			Interval: cfg.CacheRefresh,
			Timeout:  30 * time.Second,
		}
		jobsDone.Add(1)
		go func() {
			defer jobsDone.Done()
			_ = runner.Run(jobCtx)
		}()
	}
	stopBackgroundJobs := func() {
		stopJobs.Do(func() {
			cancelJobs()
			jobsDone.Wait()
		})
	}
	defer stopBackgroundJobs()

	result := make(chan error, 1)
	go func() {
		if cfg.TLSCertFile != "" {
			result <- server.ServeTLS(listener, "", "")
		} else {
			result <- server.Serve(listener)
		}
	}()
	slog.Info("newspaper server started", "address", listener.Addr().String())
	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		stopBackgroundJobs()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return fmt.Errorf("shutdown HTTP server: %w", err)
		}
		if err := <-result; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
