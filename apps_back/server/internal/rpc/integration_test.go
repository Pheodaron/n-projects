package rpc_test

import (
	"connectrpc.com/connect"
	"context"
	"fmt"
	"github.com/Pheodaron/n-projects/apps_back/server/internal/agent"
	"github.com/Pheodaron/n-projects/apps_back/server/internal/agent/engine"
	"github.com/Pheodaron/n-projects/apps_back/server/internal/chat"
	pb "github.com/Pheodaron/n-projects/apps_back/server/internal/gen/nprojects/v1"
	pc "github.com/Pheodaron/n-projects/apps_back/server/internal/gen/nprojects/v1/nprojectsv1connect"
	"github.com/Pheodaron/n-projects/apps_back/server/internal/rpc"
	"github.com/Pheodaron/n-projects/apps_back/server/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func req[T any](m *T) *connect.Request[T] {
	r := connect.NewRequest(m)
	r.Header().Set("Authorization", "Bearer test-token")
	return r
}
func TestCoreConnectPostgresTurnLoop(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, e := pgxpool.New(ctx, url)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close()
	schema := "test_" + chat.ID()
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	defer admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	cfg, _ := pgxpool.ParseConfig(url)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	store := chat.NewStore(pool)
	if e = testdb.Up(ctx, pool); e != nil {
		t.Fatal(e)
	}
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		if strings.Contains(string(body), "медленно") {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"часть\"}}]}\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		for _, text := range []string{"Привет", " мир"} {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":%q}}]}\n\n", text)
			w.(http.Flusher).Flush()
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer modelServer.Close()
	model, e := engine.New(engine.Config{BaseURL: modelServer.URL, Model: "test"})
	if e != nil {
		t.Fatal(e)
	}
	svc, e := agent.NewService(ctx, store, model, agent.Config{})
	if e != nil {
		t.Fatal(e)
	}
	defer svc.Close(context.Background())
	server := httptest.NewServer(rpc.NewHandler(store, svc, rpc.Config{Token: "test-token", UserID: "u", WorkspaceID: "w"}))
	defer server.Close()
	sessions := pc.NewSessionServiceClient(server.Client(), server.URL)
	runs := pc.NewRunServiceClient(server.Client(), server.URL)
	if _, e = sessions.ListSessions(ctx, connect.NewRequest(&pb.ListSessionsRequest{})); connect.CodeOf(e) != connect.CodeUnauthenticated {
		t.Fatalf("auth: %v", e)
	}
	created, e := sessions.CreateSession(ctx, req(&pb.CreateSessionRequest{RequestId: "create", Title: "Чат"}))
	if e != nil {
		t.Fatal(e)
	}
	id := created.Msg.Session.Id
	again, e := sessions.CreateSession(ctx, req(&pb.CreateSessionRequest{RequestId: "create", Title: "Чат"}))
	if e != nil || again.Msg.Session.Id != id {
		t.Fatal("create idempotency", e)
	}
	r, e := runs.StartRun(ctx, req(&pb.StartRunRequest{SessionId: id, RequestId: "run", Content: "Привет"}))
	if e != nil {
		t.Fatal(e)
	}
	duplicate, e := runs.StartRun(ctx, req(&pb.StartRunRequest{SessionId: id, RequestId: "run", Content: "Привет"}))
	if e != nil || duplicate.Msg.Run.Id != r.Msg.Run.Id {
		t.Fatal("run idempotency", e)
	}
	stream, e := runs.WatchRun(ctx, req(&pb.WatchRunRequest{RunId: r.Msg.Run.Id}))
	if e != nil {
		t.Fatal(e)
	}
	var final *pb.Run
	for stream.Receive() {
		final = stream.Msg().Run
	}
	if e = stream.Err(); e != nil {
		t.Fatal(e)
	}
	if final == nil || final.Status != "succeeded" || final.Message.Content != "Привет мир" {
		t.Fatalf("final: %+v", final)
	}
	history, e := sessions.ListMessages(ctx, req(&pb.ListMessagesRequest{SessionId: id}))
	if e != nil || len(history.Msg.Messages) != 2 || history.Msg.Messages[1].Status != "completed" {
		t.Fatalf("history: %v %v", history, e)
	}
	if _, e = store.GetSession(ctx, chat.Actor{UserID: "other", WorkspaceID: "w"}, id); e != chat.ErrNotFound {
		t.Fatalf("ownership: %v", e)
	}
	restored := chat.NewStore(pool)
	if v, e := restored.GetSession(ctx, chat.Actor{UserID: "u", WorkspaceID: "w"}, id); e != nil || len(v.Messages) != 2 {
		t.Fatal("persistence", e)
	}
	bad, e := runs.StartRun(ctx, req(&pb.StartRunRequest{SessionId: id, RequestId: "run", Content: "different"}))
	if connect.CodeOf(e) != connect.CodeAlreadyExists {
		t.Fatal("payload mismatch", bad, e)
	}
	slow, e := runs.StartRun(ctx, req(&pb.StartRunRequest{SessionId: id, RequestId: "slow", Content: "медленно"}))
	if e != nil {
		t.Fatal(e)
	}
	slowStream, e := runs.WatchRun(ctx, req(&pb.WatchRunRequest{RunId: slow.Msg.Run.Id}))
	if e != nil {
		t.Fatal(e)
	}
	for slowStream.Receive() {
		if slowStream.Msg().Run.Message.Content != "" {
			break
		}
	}
	if _, e = runs.CancelRun(ctx, req(&pb.CancelRunRequest{RunId: slow.Msg.Run.Id, RequestId: "cancel"})); e != nil {
		t.Fatal(e)
	}
	var cancelled *pb.Run
	for slowStream.Receive() {
		cancelled = slowStream.Msg().Run
	}
	if e = slowStream.Err(); e != nil {
		t.Fatal(e)
	}
	if cancelled == nil || cancelled.Status != "cancelled" || cancelled.Message.Content == "" {
		t.Fatalf("cancel: %+v", cancelled)
	}
	// Reconnecting after completion must deliver the latest snapshot too.
	againStream, e := runs.WatchRun(ctx, req(&pb.WatchRunRequest{RunId: slow.Msg.Run.Id}))
	if e != nil || !againStream.Receive() || againStream.Msg().Run.Status != "cancelled" {
		t.Fatal("reconnect", e)
	}
	againStream.Close()
	interrupted, _, e := store.Start(ctx, chat.Actor{UserID: "u", WorkspaceID: "w"}, id, "restart", "restore")
	if e != nil {
		t.Fatal(e)
	}
	if e = store.Recover(ctx); e != nil {
		t.Fatal(e)
	}
	recovered, e := store.GetRun(ctx, chat.Actor{UserID: "u", WorkspaceID: "w"}, interrupted.ID)
	if e != nil || recovered.Status != "interrupted" {
		t.Fatal("recover", e)
	}

	if _, e = sessions.UpdateSession(ctx, req(&pb.UpdateSessionRequest{SessionId: id, RequestId: "rename", Title: "Новое имя"})); e != nil {
		t.Fatal(e)
	}
	if _, e = sessions.DeleteSession(ctx, req(&pb.DeleteSessionRequest{SessionId: id, RequestId: "delete"})); e != nil {
		t.Fatal(e)
	}
}
