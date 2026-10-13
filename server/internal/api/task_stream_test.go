package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/suma/suma/server/internal/task"
	"github.com/suma/suma/server/internal/testutil"
)

func TestTaskProgressStreamClosesOnDisconnectAndLateCompletion(t *testing.T) {
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	tasks := task.NewService(db)
	release := make(chan struct{})
	defer close(release)
	row, err := tasks.StartForNode("local", "Local", "container.create", "Controlled creation", func(ctx context.Context, report task.Reporter) error {
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{}, 2)
	router := gin.New()
	router.GET("/ws/tasks/:id", func(c *gin.Context) { streamTask(c, tasks); exited <- struct{}{} })
	server := httptest.NewServer(router)
	defer server.Close()
	header := http.Header{"Origin": []string{server.URL}}
	connection, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http", "ws", 1)+"/ws/tasks/"+row.ID, header)
	if err != nil {
		t.Fatal(err)
	}
	connection.SetReadDeadline(time.Now().Add(3 * time.Second))
	var event task.Event
	if err := connection.ReadJSON(&event); err != nil {
		t.Fatal(err)
	}
	connection.Close()
	select {
	case <-exited:
	case <-time.After(3 * time.Second):
		t.Fatal("task subscription remained after browser disconnected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := tasks.CancelForNode(ctx, "local", row.ID); err != nil {
		t.Fatal(err)
	}
	if err := tasks.Wait(ctx, row.ID); err != nil {
		t.Fatal(err)
	}
	connection, _, err = websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http", "ws", 1)+"/ws/tasks/"+row.ID, header)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	connection.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if err := connection.ReadJSON(&event); err != nil {
			t.Fatal("finished task did not send terminal status", err)
		}
		if event.Type == "status" && event.Status == task.StatusCanceled {
			break
		}
	}
	select {
	case <-exited:
	case <-time.After(3 * time.Second):
		t.Fatal("finished task progress connection remained open")
	}
}
