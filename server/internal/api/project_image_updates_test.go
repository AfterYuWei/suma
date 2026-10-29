package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/suma/suma/server/internal/imageupdate"
	"gorm.io/gorm"
)

func TestProjectImageScheduleHTTPValidation(t *testing.T) {
	router := gin.New()
	service := imageupdate.NewService(nil, nil, nil, imageupdate.Dependencies{Node: func(context.Context, string) (imageupdate.Node, error) {
		return imageupdate.Node{}, gorm.ErrRecordNotFound
	}})
	registerProjectImageUpdateRoutes(router.Group("/api/v1/nodes/:nodeID/image-updates"), Dependencies{ImageUpdates: service}, func(*gin.Context) imageupdate.Actor { return imageupdate.Actor{} })
	for _, row := range []struct {
		method, path, body string
		status             int
	}{{"POST", "/schedule/preview", `{"cron":"30 9 * * 1-5","timezone":"Asia/Shanghai"}`, 200}, {"POST", "/schedule/preview", `{"cron":"0 0 0 * * *","timezone":"UTC"}`, 422}, {"POST", "/schedule/preview", `{"cron":"* * * * *","timezone":"not/a-zone"}`, 422}, {"POST", "/schedule/preview", `{"cron":`, 400}, {"POST", "/schedule/preview", `{"cron":"* * * * *","timezone":"UTC","unknown":true}`, 400},
		{"GET", "/projects", "", 404}, {"GET", "/projects/invalid.name/policy", "", 422}, {"POST", "/projects/shop/candidates/id/publish", `{"allow_docker_socket":"yes"}`, 400}} {
		req := httptest.NewRequest(row.method, "/api/v1/nodes/local/image-updates"+row.path, bytes.NewBufferString(row.body))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != row.status {
			t.Fatalf("%s: status %d: %s", row.path, res.Code, res.Body.String())
		}
		if row.status == http.StatusOK && strings.Count(res.Body.String(), "Z\"") != 5 {
			t.Fatal("preview did not return five absolute timestamps", res.Body.String())
		}
	}
}
