// Package rpc translates the public ConnectRPC contract to application services.
package rpc

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/Pheodaron/n-projects/apps_back/server/internal/agent"
	"github.com/Pheodaron/n-projects/apps_back/server/internal/chat"
	nprojectsv1 "github.com/Pheodaron/n-projects/gen/go/nprojects/v1"
	"github.com/Pheodaron/n-projects/gen/go/nprojects/v1/nprojectsv1connect"
)

type sessionStore interface {
	CreateSession(context.Context, chat.Actor, string, string) (chat.Session, error)
	ListSessions(context.Context, chat.Actor) ([]chat.Session, error)
	GetSession(context.Context, chat.Actor, string) (chat.Session, error)
	UpdateSession(context.Context, chat.Actor, string, string, string) (chat.Session, error)
	DeleteSession(context.Context, chat.Actor, string, string) error
	ListMessages(context.Context, chat.Actor, string) ([]chat.Message, error)
	GetRun(context.Context, chat.Actor, string) (chat.Run, error)
}

type runService interface {
	StartRun(context.Context, chat.Actor, string, string, string) (chat.Run, error)
	CancelRun(context.Context, chat.Actor, string, string) (chat.Run, error)
}

type Config struct {
	Token             string
	UserID            string
	WorkspaceID       string
	WatchPollInterval time.Duration
}

type handler struct {
	store        sessionStore
	runs         runService
	pollInterval time.Duration
}

// NewHandler serves both unary and server-streaming calls through the same
// authentication interceptor. The supplied services own the run lifecycle.
func NewHandler(store sessionStore, runs runService, cfg Config) http.Handler {
	if cfg.WatchPollInterval <= 0 {
		cfg.WatchPollInterval = 100 * time.Millisecond
	}
	h := &handler{store: store, runs: runs, pollInterval: cfg.WatchPollInterval}
	options := []connect.HandlerOption{
		connect.WithInterceptors(newAuthInterceptor(cfg)),
		connect.WithReadMaxBytes(2 << 20),
	}
	mux := http.NewServeMux()
	mux.Handle(nprojectsv1connect.NewSessionServiceHandler(h, options...))
	mux.Handle(nprojectsv1connect.NewRunServiceHandler(h, options...))
	return mux
}

func (h *handler) CreateSession(ctx context.Context, req *connect.Request[nprojectsv1.CreateSessionRequest]) (*connect.Response[nprojectsv1.CreateSessionResponse], error) {
	if err := checkRequestID(req.Msg.RequestId); err != nil {
		return nil, err
	}
	session, err := h.store.CreateSession(ctx, actorFrom(ctx), req.Msg.RequestId, req.Msg.Title)
	if err != nil {
		return nil, rpcError(err)
	}
	return connect.NewResponse(&nprojectsv1.CreateSessionResponse{Session: sessionProto(session)}), nil
}

func (h *handler) ListSessions(ctx context.Context, _ *connect.Request[nprojectsv1.ListSessionsRequest]) (*connect.Response[nprojectsv1.ListSessionsResponse], error) {
	sessions, err := h.store.ListSessions(ctx, actorFrom(ctx))
	if err != nil {
		return nil, rpcError(err)
	}
	response := &nprojectsv1.ListSessionsResponse{Sessions: make([]*nprojectsv1.Session, 0, len(sessions))}
	for _, session := range sessions {
		response.Sessions = append(response.Sessions, sessionProto(session))
	}
	return connect.NewResponse(response), nil
}

func (h *handler) GetSession(ctx context.Context, req *connect.Request[nprojectsv1.GetSessionRequest]) (*connect.Response[nprojectsv1.GetSessionResponse], error) {
	if err := checkID(req.Msg.SessionId); err != nil {
		return nil, err
	}
	session, err := h.store.GetSession(ctx, actorFrom(ctx), req.Msg.SessionId)
	if err != nil {
		return nil, rpcError(err)
	}
	return connect.NewResponse(&nprojectsv1.GetSessionResponse{Session: sessionProto(session)}), nil
}

func (h *handler) UpdateSession(ctx context.Context, req *connect.Request[nprojectsv1.UpdateSessionRequest]) (*connect.Response[nprojectsv1.UpdateSessionResponse], error) {
	if err := checkMutation(req.Msg.SessionId, req.Msg.RequestId); err != nil {
		return nil, err
	}
	session, err := h.store.UpdateSession(ctx, actorFrom(ctx), req.Msg.SessionId, req.Msg.RequestId, req.Msg.Title)
	if err != nil {
		return nil, rpcError(err)
	}
	return connect.NewResponse(&nprojectsv1.UpdateSessionResponse{Session: sessionProto(session)}), nil
}

func (h *handler) DeleteSession(ctx context.Context, req *connect.Request[nprojectsv1.DeleteSessionRequest]) (*connect.Response[nprojectsv1.DeleteSessionResponse], error) {
	if err := checkMutation(req.Msg.SessionId, req.Msg.RequestId); err != nil {
		return nil, err
	}
	if err := h.store.DeleteSession(ctx, actorFrom(ctx), req.Msg.SessionId, req.Msg.RequestId); err != nil {
		return nil, rpcError(err)
	}
	return connect.NewResponse(&nprojectsv1.DeleteSessionResponse{}), nil
}

func (h *handler) ListMessages(ctx context.Context, req *connect.Request[nprojectsv1.ListMessagesRequest]) (*connect.Response[nprojectsv1.ListMessagesResponse], error) {
	if err := checkID(req.Msg.SessionId); err != nil {
		return nil, err
	}
	messages, err := h.store.ListMessages(ctx, actorFrom(ctx), req.Msg.SessionId)
	if err != nil {
		return nil, rpcError(err)
	}
	response := &nprojectsv1.ListMessagesResponse{Messages: make([]*nprojectsv1.Message, 0, len(messages))}
	for _, message := range messages {
		response.Messages = append(response.Messages, messageProto(message))
	}
	return connect.NewResponse(response), nil
}

func (h *handler) StartRun(ctx context.Context, req *connect.Request[nprojectsv1.StartRunRequest]) (*connect.Response[nprojectsv1.StartRunResponse], error) {
	if err := checkMutation(req.Msg.SessionId, req.Msg.RequestId); err != nil {
		return nil, err
	}
	run, err := h.runs.StartRun(ctx, actorFrom(ctx), req.Msg.SessionId, req.Msg.RequestId, req.Msg.Content)
	if err != nil {
		return nil, rpcError(err)
	}
	return connect.NewResponse(&nprojectsv1.StartRunResponse{Run: runProto(run)}), nil
}

func (h *handler) GetRun(ctx context.Context, req *connect.Request[nprojectsv1.GetRunRequest]) (*connect.Response[nprojectsv1.GetRunResponse], error) {
	if err := checkID(req.Msg.RunId); err != nil {
		return nil, err
	}
	run, err := h.store.GetRun(ctx, actorFrom(ctx), req.Msg.RunId)
	if err != nil {
		return nil, rpcError(err)
	}
	return connect.NewResponse(&nprojectsv1.GetRunResponse{Run: runProto(run)}), nil
}

func (h *handler) CancelRun(ctx context.Context, req *connect.Request[nprojectsv1.CancelRunRequest]) (*connect.Response[nprojectsv1.CancelRunResponse], error) {
	if err := checkMutation(req.Msg.RunId, req.Msg.RequestId); err != nil {
		return nil, err
	}
	run, err := h.runs.CancelRun(ctx, actorFrom(ctx), req.Msg.RunId, req.Msg.RequestId)
	if err != nil {
		return nil, rpcError(err)
	}
	return connect.NewResponse(&nprojectsv1.CancelRunResponse{Run: runProto(run)}), nil
}

func (h *handler) WatchRun(ctx context.Context, req *connect.Request[nprojectsv1.WatchRunRequest], stream *connect.ServerStream[nprojectsv1.WatchRunResponse]) error {
	if err := checkID(req.Msg.RunId); err != nil {
		return err
	}
	ticker := time.NewTicker(h.pollInterval)
	defer ticker.Stop()
	var revision int64 = -1
	for {
		// GetRun returns message, state and revision from one consistent DB
		// snapshot. Polling cannot miss a completion between subscribe and read.
		run, err := h.store.GetRun(ctx, actorFrom(ctx), req.Msg.RunId)
		if err != nil {
			return rpcError(err)
		}
		if run.Revision > revision {
			if err := stream.Send(&nprojectsv1.WatchRunResponse{Run: runProto(run)}); err != nil {
				return err
			}
			revision = run.Revision
		}
		if terminal(run.Status) {
			return nil
		}
		select {
		case <-ctx.Done():
			return rpcError(ctx.Err())
		case <-ticker.C:
		}
	}
}

func terminal(status string) bool {
	switch status {
	case "succeeded", "failed", "cancelled", "interrupted":
		return true
	default:
		return false
	}
}

func checkID(id string) error {
	if strings.TrimSpace(id) == "" || len(id) > 128 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("a valid resource ID is required"))
	}
	return nil
}

func checkRequestID(id string) error {
	if strings.TrimSpace(id) == "" || len(id) > 128 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("request_id is required and must be at most 128 bytes"))
	}
	return nil
}

func checkMutation(id, requestID string) error {
	if err := checkID(id); err != nil {
		return err
	}
	return checkRequestID(requestID)
}

func rpcError(err error) error {
	if err == nil {
		return nil
	}
	var code connect.Code
	var message string
	switch {
	case errors.Is(err, context.Canceled):
		code, message = connect.CodeCanceled, "request cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		code, message = connect.CodeDeadlineExceeded, "request timed out"
	case errors.Is(err, chat.ErrNotFound):
		code, message = connect.CodeNotFound, "resource not found"
	case errors.Is(err, chat.ErrConflict):
		code, message = connect.CodeFailedPrecondition, "resource has an active run or cannot be changed in its current state"
	case errors.Is(err, chat.ErrIdempotency):
		code, message = connect.CodeAlreadyExists, "request_id was already used with different input"
	case errors.Is(err, chat.ErrInvalid):
		code, message = connect.CodeInvalidArgument, "invalid input"
	case errors.Is(err, agent.ErrBusy):
		code, message = connect.CodeResourceExhausted, "agent is busy; retry after the active run finishes"
	default:
		slog.Error("RPC operation failed", "error", err)
		code, message = connect.CodeInternal, "internal server error"
	}
	return connect.NewError(code, errors.New(message))
}

func sessionProto(session chat.Session) *nprojectsv1.Session {
	return &nprojectsv1.Session{
		Id: session.ID, Title: session.Title,
		CreatedAtUnixMs: session.CreatedAt.UnixMilli(), UpdatedAtUnixMs: session.UpdatedAt.UnixMilli(),
		ActiveRunId: session.ActiveRunID, LastRunId: session.LastRunID,
	}
}

func messageProto(message chat.Message) *nprojectsv1.Message {
	return &nprojectsv1.Message{
		Id: message.ID, SessionId: message.SessionID, RunId: message.RunID,
		Role: message.Role, Content: message.Content, Status: message.Status,
		Seq: message.Seq, CreatedAtUnixMs: message.CreatedAt.UnixMilli(),
	}
}

func runProto(run chat.Run) *nprojectsv1.Run {
	return &nprojectsv1.Run{
		Id: run.ID, SessionId: run.SessionID, Status: run.Status, Revision: run.Revision,
		ContextTruncated: run.ContextTruncated, ErrorCode: run.ErrorCode, ErrorMessage: run.ErrorMessage,
		CreatedAtUnixMs: run.CreatedAt.UnixMilli(), UpdatedAtUnixMs: run.UpdatedAt.UnixMilli(),
		Message: messageProto(run.Message),
	}
}
