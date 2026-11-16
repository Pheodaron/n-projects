package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"core-v2/internal/agent"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	engine := agent.New(func() agent.Agent { return &agent.Echo{} })
	server := &http.Server{
		Addr:              ":8080",
		Handler:           agent.Handler(engine),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("Сервис запущен на %s", server.Addr)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Printf("Ошибка HTTP-сервера: %v", err)
		}
	}

	// Сначала отменяем работу агентов и подписки, затем завершаем HTTP-запросы.
	engine.Close()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		log.Printf("Ошибка завершения HTTP-сервера: %v", err)
		_ = server.Close()
	}
}
