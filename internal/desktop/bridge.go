package desktop

import (
	"connectrpc.com/connect"
	"context"
	"fmt"
	pb "github.com/Pheodaron/n-projects/gen/go/nprojects/v1"
	rpc "github.com/Pheodaron/n-projects/gen/go/nprojects/v1/nprojectsv1connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"net/http"
	"sync"
	"time"
)

type Bridge struct {
	ctx      context.Context
	token    string
	sessions rpc.SessionServiceClient
	runs     rpc.RunServiceClient
	mu       sync.Mutex
	watching map[string]bool
	Emit     func(string, string)
}

func New(ctx context.Context, url, token string) *Bridge {
	return &Bridge{ctx: ctx, token: token, sessions: rpc.NewSessionServiceClient(http.DefaultClient, url), runs: rpc.NewRunServiceClient(http.DefaultClient, url), watching: map[string]bool{}}
}
func invoke[Q any, P any](ctx context.Context, token, payload string, q *Q, call func(context.Context, *connect.Request[Q]) (*connect.Response[P], error)) (string, error) {
	if e := protojson.Unmarshal([]byte(payload), any(q).(proto.Message)); e != nil {
		return "", e
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req := connect.NewRequest(q)
	req.Header().Set("Authorization", "Bearer "+token)
	response, e := call(ctx, req)
	if e != nil {
		return "", e
	}
	b, e := protojson.Marshal(any(response.Msg).(proto.Message))
	return string(b), e
}
func (b *Bridge) Call(ctx context.Context, method, payload string) (string, error) {
	switch method {
	case "CreateSession":
		return invoke(ctx, b.token, payload, &pb.CreateSessionRequest{}, b.sessions.CreateSession)
	case "ListSessions":
		return invoke(ctx, b.token, payload, &pb.ListSessionsRequest{}, b.sessions.ListSessions)
	case "GetSession":
		return invoke(ctx, b.token, payload, &pb.GetSessionRequest{}, b.sessions.GetSession)
	case "UpdateSession":
		return invoke(ctx, b.token, payload, &pb.UpdateSessionRequest{}, b.sessions.UpdateSession)
	case "DeleteSession":
		return invoke(ctx, b.token, payload, &pb.DeleteSessionRequest{}, b.sessions.DeleteSession)
	case "ListMessages":
		return invoke(ctx, b.token, payload, &pb.ListMessagesRequest{}, b.sessions.ListMessages)
	case "StartRun":
		return invoke(ctx, b.token, payload, &pb.StartRunRequest{}, b.runs.StartRun)
	case "GetRun":
		return invoke(ctx, b.token, payload, &pb.GetRunRequest{}, b.runs.GetRun)
	case "CancelRun":
		return invoke(ctx, b.token, payload, &pb.CancelRunRequest{}, b.runs.CancelRun)
	}
	return "", fmt.Errorf("unknown method")
}
func (b *Bridge) Watch(id string) {
	b.mu.Lock()
	if b.watching[id] {
		b.mu.Unlock()
		return
	}
	b.watching[id] = true
	b.mu.Unlock()
	go func() {
		defer func() { b.mu.Lock(); delete(b.watching, id); b.mu.Unlock() }()
		var revision int64 = -1
		for b.ctx.Err() == nil {
			req := connect.NewRequest(&pb.WatchRunRequest{RunId: id})
			req.Header().Set("Authorization", "Bearer "+b.token)
			stream, e := b.runs.WatchRun(b.ctx, req)
			terminal := false
			if e == nil {
				for stream.Receive() {
					r := stream.Msg().Run
					if r.Revision > revision {
						revision = r.Revision
						data, _ := protojson.Marshal(r)
						b.Emit("run", string(data))
					}
					terminal = r.Status != "running" && r.Status != "cancelling"
				}
				e = stream.Err()
				stream.Close()
			}
			if terminal {
				return
			}
			if connect.CodeOf(e) == connect.CodeUnauthenticated || connect.CodeOf(e) == connect.CodeNotFound || connect.CodeOf(e) == connect.CodePermissionDenied {
				b.Emit("connection", "Подписка недоступна: проверьте подключение и доступ")
				return
			}
			select {
			case <-b.ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}()
}
