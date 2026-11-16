package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"agent-app/pkg/agentcore"
)

func main() {
	bgCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	engine, mux := serverInit(bgCtx)
	defer engine.Shutdown() // Вызывается только при завершении main()

	srv := &http.Server{Addr: ":8090", Handler: mux}
	go func() {
		log.Println("Server listening on http://localhost:8090")
		_ = srv.ListenAndServe()
	}()

	<-bgCtx.Done()
	_ = srv.Shutdown(context.Background())
}

func serverInit(bgCtx context.Context) (*agentcore.Engine, *http.ServeMux) {
	factory, err := agentcore.NewLlamaCppAgentFactory(agentcore.LLMConfig{
		BaseURL:        "http://127.0.0.1:8080/v1",
		Model:          "qwen",
		SystemPrompt:   "Ты полезный ИИ-ассистент. Отвечай четко и по делу.",
		EnableThinking: true,
	})
	if err != nil {
		log.Fatalf("init agent factory: %v", err)
	}

	engine := agentcore.NewEngine(bgCtx, agentcore.EngineConfig{
		Factory: factory,
	})

	mux := http.NewServeMux()

	// 1. POST /send — отправить сообщение или уточнение на ходу
	mux.HandleFunc("POST /send", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			SessionID string `json:"sessionId"`
			Text      string `json:"text"`
			Preempt   bool   `json:"preempt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SessionID == "" {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		engine.Send(req.SessionID, req.Text, req.Preempt)
		w.WriteHeader(http.StatusAccepted)
	})

	// 2. POST /stop — остановить генерацию в чате
	mux.HandleFunc("POST /stop", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SessionID == "" {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		engine.Stop(req.SessionID)
		w.WriteHeader(http.StatusOK)
	})

	// 3. GET /events?sessionId=c1 — подписаться на стрим событий
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.URL.Query().Get("sessionId")
		if sessionID == "" {
			http.Error(w, "sessionId required", http.StatusBadRequest)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")

		ch, unsub := engine.Subscribe(sessionID)
		defer unsub()

		for {
			select {
			case <-r.Context().Done():
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				data, _ := json.Marshal(ev)
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}
		}
	})

	return engine, mux
}
