package connectagent

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"my-gitlab/ai-support/n-projects/agentcore"
	agentv1 "my-gitlab/ai-support/n-projects/proto/agent/v1"
	"my-gitlab/ai-support/n-projects/proto/agent/v1/agentv1connect"
)

type service struct {
	engine *agentcore.Engine
}

// NewHandler returns the service path and handler for http.ServeMux.Handle.
func NewHandler(engine *agentcore.Engine, opts ...connect.HandlerOption) (string, http.Handler) {
	return agentv1connect.NewAgentServiceHandler(&service{engine: engine}, opts...)
}

func (s *service) CreateSession(ctx context.Context, _ *agentv1.CreateSessionRequest) (*agentv1.CreateSessionResponse, error) {
	id, err := s.engine.CreateSession(ctx)
	if err != nil {
		return nil, rpcError(err)
	}
	return &agentv1.CreateSessionResponse{SessionId: id}, nil
}

func (s *service) ListSessions(ctx context.Context, req *agentv1.ListSessionsRequest) (*agentv1.ListSessionsResponse, error) {
	page, err := s.engine.ListSessions(ctx, req.GetPage(), req.GetPageSize())
	if err != nil {
		return nil, rpcError(err)
	}
	response := &agentv1.ListSessionsResponse{Sessions: make([]*agentv1.Session, 0, len(page.Sessions))}
	for _, session := range page.Sessions {
		entry := &agentv1.Session{
			Id:        session.ID,
			Title:     session.Title,
			CreatedAt: timestamppb.New(session.CreatedAt),
		}
		if session.LastActivityAt != nil {
			entry.LastActivityAt = timestamppb.New(*session.LastActivityAt)
		}
		response.Sessions = append(response.Sessions, entry)
	}
	return response, nil
}

func (s *service) RenameSession(ctx context.Context, req *agentv1.RenameSessionRequest) (*agentv1.RenameSessionResponse, error) {
	if req.GetSessionId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("session_id is required"))
	}
	if err := s.engine.RenameSession(ctx, req.SessionId, req.Title); err != nil {
		return nil, rpcError(err)
	}
	return &agentv1.RenameSessionResponse{}, nil
}

func (s *service) Send(ctx context.Context, req *agentv1.SendRequest) (*agentv1.SendResponse, error) {
	if req.GetSessionId() == "" || req.GetText() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("session_id and text are required"))
	}
	if err := s.engine.Send(ctx, req.SessionId, req.Text, req.Preempt); err != nil {
		return nil, rpcError(err)
	}
	return &agentv1.SendResponse{}, nil
}

func (s *service) GetHistory(ctx context.Context, req *agentv1.GetHistoryRequest) (*agentv1.GetHistoryResponse, error) {
	if req.GetSessionId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("session_id is required"))
	}
	messages, err := s.engine.History(ctx, req.SessionId)
	if err != nil {
		return nil, rpcError(err)
	}
	response := &agentv1.GetHistoryResponse{SessionId: req.SessionId}
	for _, message := range messages {
		entry := &agentv1.HistoryMessage{
			Id: message.ID, CreatedAt: timestamppb.New(message.CreatedAt),
			Role: message.Role, Text: message.Text,
		}
		for _, block := range message.Blocks {
			entry.Blocks = append(entry.Blocks, &agentv1.HistoryBlock{
				Type: block.Type, Text: block.Text, ToolJson: block.Tool,
			})
		}
		response.Messages = append(response.Messages, entry)
	}
	return response, nil
}

func (s *service) Subscribe(ctx context.Context, req *agentv1.SubscribeRequest, stream *connect.ServerStream[agentv1.Event]) error {
	if req.GetSessionId() == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("session_id is required"))
	}
	events, err := s.engine.Subscribe(ctx, req.SessionId)
	if err != nil {
		return rpcError(err)
	}
	for event := range events {
		if err := stream.Send(&agentv1.Event{
			SessionId: event.SessionID,
			Type:      string(event.Type),
			AgentName: event.AgentName,
			Text:      event.Text,
			ToolJson:  event.Tool,
		}); err != nil {
			return err
		}
	}
	return rpcError(ctx.Err())
}

func rpcError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled):
		return connect.NewError(connect.CodeCanceled, context.Canceled)
	case errors.Is(err, context.DeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, context.DeadlineExceeded)
	case errors.Is(err, agentcore.ErrInvalidSessionPage):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, agentcore.ErrInvalidSessionTitle):
		return connect.NewError(connect.CodeInvalidArgument, agentcore.ErrInvalidSessionTitle)
	case errors.Is(err, agentcore.ErrSessionNotFound):
		return connect.NewError(connect.CodeNotFound, agentcore.ErrSessionNotFound)
	case errors.Is(err, agentcore.ErrSessionStopped):
		return connect.NewError(connect.CodeFailedPrecondition, agentcore.ErrSessionStopped)
	default:
		return connect.NewError(connect.CodeInternal, errors.New("agent operation failed"))
	}
}
