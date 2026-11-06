package rpc

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	"github.com/Pheodaron/n-projects/internal/chat"
)

type actorKey struct{}

func actorFrom(ctx context.Context) chat.Actor {
	actor, _ := ctx.Value(actorKey{}).(chat.Actor)
	return actor
}

type authInterceptor struct {
	tokenHash [32]byte
	enabled   bool
	actor     chat.Actor
}

func newAuthInterceptor(cfg Config) *authInterceptor {
	return &authInterceptor{
		tokenHash: sha256.Sum256([]byte("Bearer " + cfg.Token)),
		enabled:   cfg.Token != "" && cfg.UserID != "" && cfg.WorkspaceID != "",
		actor:     chat.Actor{UserID: cfg.UserID, WorkspaceID: cfg.WorkspaceID},
	}
}

func (a *authInterceptor) authenticate(ctx context.Context, header http.Header) (context.Context, error) {
	actual := sha256.Sum256([]byte(header.Get("Authorization")))
	if !a.enabled || subtle.ConstantTimeCompare(actual[:], a.tokenHash[:]) != 1 {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("valid bearer authentication is required"))
	}
	return context.WithValue(ctx, actorKey{}, a.actor), nil
}

func (a *authInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
		ctx, err := a.authenticate(ctx, request.Header())
		if err != nil {
			return nil, err
		}
		return next(ctx, request)
	}
}

func (a *authInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		ctx, err := a.authenticate(ctx, conn.RequestHeader())
		if err != nil {
			return err
		}
		return next(ctx, conn)
	}
}

func (a *authInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}
