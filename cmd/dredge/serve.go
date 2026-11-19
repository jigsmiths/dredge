package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/grafana/pyroscope-go"
	"github.com/jigsmiths/dredge/internal/deezer"
	"github.com/jigsmiths/dredge/internal/jobs"
	"github.com/jigsmiths/dredge/internal/library"
	"github.com/jigsmiths/dredge/internal/server"
	"github.com/jigsmiths/dredge/internal/slskd"
)

// startProfiling sends profiles to a Pyroscope server, if there is one, and
// returns the function that stops it.
func startProfiling(addr string) func() {
	if addr == "" {
		return func() {}
	}
	p, err := pyroscope.Start(pyroscope.Config{
		ApplicationName: "dredge",
		ServerAddress:   addr,
		ProfileTypes: []pyroscope.ProfileType{
			pyroscope.ProfileCPU,
			pyroscope.ProfileAllocObjects,
			pyroscope.ProfileAllocSpace,
			pyroscope.ProfileInuseObjects,
			pyroscope.ProfileInuseSpace,
			pyroscope.ProfileGoroutines,
		},
	})
	if err != nil {
		slog.Error("failed to start pyroscope", "err", err)
		return func() {}
	}
	return func() { _ = p.Stop() }
}

func serve(ctx context.Context, e *env) error {
	cfg := e.cfg
	if err := requireMusicDir(cfg.MusicDir); err != nil {
		return err
	}
	defer startProfiling(cfg.PyroscopeServerAddress)()

	d, err := e.open(cfg)
	if err != nil {
		return err
	}
	defer d.close()

	layout := library.Layout{Root: cfg.MusicDir, SinglesDir: cfg.SinglesDir}
	deps := jobs.Deps{
		Spotify: d.spotify, Search: d.search, Source: d.source, Library: d.index, Details: d.details,
		Lookup: deezer.New("", nil),
		Layout: layout, Workers: cfg.Workers, WantEvery: cfg.WantEvery,
	}
	switch on, partly := cfg.Soulseek(); {
	case on:
		deps.Soulseek = slskd.New(cfg.SlskdURL, cfg.SlskdAPIKey, cfg.SlskdDir, nil)
		slog.Info("wanted tracks are also looked for on Soulseek", "slskd", cfg.SlskdURL)
	case partly:
		slog.Warn("Soulseek is off: set DREDGE_SLSKD_URL, DREDGE_SLSKD_API_KEY and DREDGE_SLSKD_DIR together")
	}
	mgr := jobs.NewManager(jobs.NewStore(d.conn), deps)
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
			Jobs:    mgr,
			Search:  d.search,
			APIKey:  cfg.APIKey,
			Version: version,
			Scan:    func(ctx context.Context) (library.ScanStats, error) { return d.index.Scan(ctx, layout) },
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
