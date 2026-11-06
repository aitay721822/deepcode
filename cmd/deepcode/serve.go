package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"deepcode/internal/app"
	"deepcode/internal/app/server/webui"
	"deepcode/internal/platform/storage"
)

// serve runs the HTTP server and the background pipeline until SIGINT/SIGTERM.
func serve(args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	addr := env("DEEPCODE_ADDR", "127.0.0.1:8080")
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	fs.StringVar(&addr, "addr", addr, "listen address")
	fs.StringVar(&cfg.DataDir, "data", cfg.DataDir, "data directory")
	fs.Parse(args)
	data, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return err
	}
	cfg.DataDir = data
	if err := os.MkdirAll(data, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(data, 0o700); err != nil { // also one made by an earlier version
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := storage.Open(ctx, filepath.Join(data, "deepcode.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	ui, err := webui.Handler()
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr) // fail on a busy port before any worker starts
	if err != nil {
		return err
	}
	host, _, _ := net.SplitHostPort(ln.Addr().String())
	loopback := net.ParseIP(host).IsLoopback()
	gin.SetMode(gin.ReleaseMode)
	a, err := app.New(ctx, cfg, db, ui, loopback)
	if err != nil {
		ln.Close()
		return err
	}
	if !loopback && len(cfg.TrustedProxies) == 0 {
		log.Printf("sign-in limits count failures per client address: behind a reverse proxy, set DEEPCODE_TRUSTED_PROXIES to its address, or every user shares the proxy's count")
	}
	srv := &http.Server{
		Handler:           a.Handler,
		ReadHeaderTimeout: 10 * time.Second,
		// ReadTimeout bounds reading a request and its body; net/http lifts
		// it once the body is read, so streams outlive it. No WriteTimeout,
		// which would cut them: a stream bounds each write itself.
		ReadTimeout: 30 * time.Second,
		IdleTimeout: 2 * time.Minute,
		BaseContext: func(net.Listener) context.Context { return ctx }, // shutdown ends SSE streams
	}

	var wg sync.WaitGroup
	var failed error // the pipeline's, which stops the server too
	wg.Go(func() {
		err := a.Run(ctx)
		switch {
		case err == nil:
		case ctx.Err() != nil: // a stop was asked for: most likely its doing (a startup query cut short), so not a failure
			log.Printf("pipeline, while stopping: %v", err)
		default:
			failed = fmt.Errorf("pipeline: %w", err)
			stop()
		}
	})
	wg.Go(func() {
		<-ctx.Done()
		stop() // signals get their default back: a second Ctrl-C ends the process, should a job not stop
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	})

	log.Printf("deepcode listening on http://%s (data: %s, chat: %s, embeddings: %s)",
		ln.Addr(), data, orNone(cfg.ChatModel), orNone(cfg.EmbedModel))
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		stop()
		wg.Wait()
		return errors.Join(err, failed) // failed: the pipeline's, should it have failed meanwhile
	}
	wg.Wait()
	return failed
}

func orNone(s string) string {
	if s == "" {
		return "off"
	}
	return s
}
