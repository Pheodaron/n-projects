package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func Handler(engine *Engine) http.Handler {
	h := &handler{engine: engine}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sessions/{id}/send", h.send)
	mux.HandleFunc("POST /sessions/{id}/stop", h.stop)
	mux.HandleFunc("GET /sessions/{id}/subscribe", h.subscribe)
	return mux
}

type handler struct {
	engine *Engine
}

func (h *handler) send(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ожидается JSON с полем text, не более 64 КиБ"})
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF || strings.TrimSpace(body.Text) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ожидается одно сообщение с непустым text"})
		return
	}
	message, err := h.engine.Send(r.PathValue("id"), body.Text)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, message)
}

func (h *handler) stop(w http.ResponseWriter, r *http.Request) {
	if err := h.engine.Stop(r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	// Принятие отмены не означает, что выполняющийся Step уже завершился.
	w.WriteHeader(http.StatusAccepted)
}

func (h *handler) subscribe(w http.ResponseWriter, r *http.Request) {
	s, err := h.engine.find(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	cursor := r.URL.Query().Get("after")
	if cursor == "" {
		cursor = r.Header.Get("Last-Event-ID")
	}
	var after uint64
	if cursor != "" {
		after, err = strconv.ParseUint(cursor, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "неверный номер события"})
			return
		}
	}
	events, changed, err := s.events(after)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	controller := http.NewResponseController(w)
	write := func(data string) error {
		if err := controller.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
			return err
		}
		if _, err := io.WriteString(w, data); err != nil {
			return err
		}
		if err := controller.Flush(); err != nil {
			return err
		}
		return controller.SetWriteDeadline(time.Time{})
	}
	if err := write(": connected\n\n"); err != nil {
		return
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		for _, event := range events {
			data, err := json.Marshal(event)
			if err != nil {
				return
			}
			if err := write(fmt.Sprintf("id: %d\ndata: %s\n\n", event.ID, data)); err != nil {
				return
			}
			after = event.ID
		}
		select {
		case <-r.Context().Done():
			return
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			if err := write(": ping\n\n"); err != nil {
				return
			}
		case <-changed:
		}
		events, changed, err = s.events(after)
		if err != nil {
			return
		}
	}
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ErrClosed):
		status = http.StatusServiceUnavailable
	case errors.Is(err, ErrQueueFull):
		status = http.StatusTooManyRequests
	case errors.Is(err, ErrCursor):
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
