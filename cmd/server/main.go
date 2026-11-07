package main

import (
	"context"
	"fmt"
	"github.com/Pheodaron/n-projects/internal/agent"
	"github.com/Pheodaron/n-projects/internal/agent/engine"
	"github.com/Pheodaron/n-projects/internal/chat"
	"github.com/Pheodaron/n-projects/internal/rpc"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func main() {
	if e := run(); e != nil {
		slog.Error("server stopped", "error", e)
		os.Exit(1)
	}
}
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	token := os.Getenv("DEV_TOKEN")
	if token == "" {
		return fmt.Errorf("DEV_TOKEN is required")
	}
	pool, e := pgxpool.New(ctx, env("DATABASE_URL", "postgres://nprojects:dev@127.0.0.1:5432/nprojects?sslmode=disable"))
	if e != nil {
		return e
	}
	defer pool.Close()
	if e = pool.Ping(ctx); e != nil {
		return e
	}
	lock, e := pool.Acquire(ctx)
	if e != nil {
		return e
	}
	defer lock.Release()
	var ok bool
	if e = lock.QueryRow(ctx, "SELECT pg_try_advisory_lock(78423982)").Scan(&ok); e != nil {
		return e
	}
	if !ok {
		return fmt.Errorf("another Core server owns this database")
	}
	defer lock.Exec(context.Background(), "SELECT pg_advisory_unlock(78423982)")
	store := chat.NewStore(pool)
	model, e := engine.New(engine.Config{BaseURL: env("LLAMA_BASE_URL", "http://127.0.0.1:8081/v1"), Model: env("LLAMA_MODEL", "local"), APIKey: os.Getenv("LLAMA_API_KEY")})
	if e != nil {
		return e
	}
	svc, e := agent.NewService(ctx, store, model, agent.Config{})
	if e != nil {
		return e
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		svc.Close(shutdown)
	}()
	mux := http.NewServeMux()
	mux.Handle("/", rpc.NewHandler(store, svc, rpc.Config{Token: token, UserID: "dev", WorkspaceID: "dev"}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		c, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if e := model.Check(c); e != nil {
			http.Error(w, "model unavailable", 503)
			return
		}
		w.Write([]byte("ok"))
	})
	server := &http.Server{Addr: env("SERVER_ADDR", "127.0.0.1:8080"), Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe() }()
	slog.Info("Core listening", "address", server.Addr)
	select {
	case e := <-errCh:
		if e != http.ErrServerClosed {
			return e
		}
	case <-ctx.Done():
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(c)
	}
	return nil
}
