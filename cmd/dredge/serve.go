package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jigsmiths/dredge/internal/jobs"
	"github.com/jigsmiths/dredge/internal/library"
	"github.com/jigsmiths/dredge/internal/server"
)

func serve(ctx context.Context, e *env) error {
	cfg := e.cfg
	if fi, err := os.Stat(cfg.MusicDir); err != nil || !fi.IsDir() {
		return fmt.Errorf("music dir %q does not exist; refusing to download somewhere that may not be the library", cfg.MusicDir)
	}
	d, err := e.open(cfg)
	if err != nil {
		return err
	}
	defer d.close()

	layout := library.Layout{Root: cfg.MusicDir, SinglesDir: cfg.SinglesDir}
	mgr := jobs.NewManager(jobs.NewStore(d.conn), jobs.Deps{
		Spotify: d.spotify, Search: d.search, Source: d.source, Library: d.index, Details: d.details,
		Layout: layout, Workers: cfg.Workers,
	})
	runCtx, stopJobs := context.WithCancel(ctx)
	jobsDone := make(chan struct{})
	go func() {
		defer close(jobsDone)
		if err := mgr.Run(runCtx); err != nil {
			slog.Error("job runner stopped", "err", err)
		}
	}()
	defer func() { stopJobs(); <-jobsDone }()

	srv := &http.Server{
		Addr: ":" + cfg.Port,
		Handler: server.New(server.Options{
			Jobs:   mgr,
			Search: d.search,
			APIKey: cfg.APIKey,
			Scan:   func(ctx context.Context) (library.ScanStats, error) { return d.index.Scan(ctx, layout) },
		}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	serveErr := make(chan error, 1)
	go func() {
		slog.Info("server listening", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
