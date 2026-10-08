package ai

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/suma/suma/server/internal/database"
)

func TestGuestQueryCannotEnterDiagnosisProposalOrApproval(t *testing.T) {
	s, executions, _ := aiFixture(t)
	var reads atomic.Int32
	s.deps.Query = func(context.Context, string) (QuerySummary, error) {
		reads.Add(1)
		out := QuerySummary{Available: true}
		out.Containers.Total = 2
		return out, nil
	}
	ctx := context.Background()
	if _, err := s.Query(ctx, "other"); !errors.Is(err, ErrScope) || reads.Load() != 0 {
		t.Fatal("out-of-scope query reached adapter", err)
	}
	out, err := s.Query(ctx, "local")
	if err != nil || out.Containers.Total != 2 || reads.Load() != 1 {
		t.Fatal("safe query failed", err)
	}
	guest := Actor{Source: "chat", ExternalUserID: "guest", ChatID: "chat"}
	if _, err := s.Start(ctx, RunInput{NodeID: "local", Question: "restart"}, guest); !errors.Is(err, ErrScope) {
		t.Fatal("guest started model diagnosis", err)
	}
	if _, err := s.Start(ctx, RunInput{NodeID: "local", Question: "restart"}, guest); !errors.Is(err, ErrScope) {
		t.Fatal("guest created proposal", err)
	}
	if _, err := s.Decide(ctx, "fake", Decision{Approve: true}, guest); !errors.Is(err, ErrScope) {
		t.Fatal("guest approved operation", err)
	}
	s.db.Create(&database.AIOperation{ID: "queued-for-guest-test", NodeID: "local", Status: "queued", Action: "container.restart", ResourceID: "frozen-container"})
	if err := s.execute(ctx, "queued-for-guest-test", guest, func(int, string) {}); !errors.Is(err, ErrScope) || executions.Load() != 0 {
		t.Fatal("guest reached execution")
	}
	guest.UserID = 1
	if _, err := s.Start(ctx, RunInput{NodeID: "local", Question: "restart"}, guest); !errors.Is(err, ErrScope) {
		t.Fatal("forged SUMA ID bypassed binding")
	}
}

func TestQueryRechecksScopeAndHidesAdapterErrors(t *testing.T) {
	s, _, _ := aiFixture(t)
	s.deps.Query = func(context.Context, string) (QuerySummary, error) {
		return QuerySummary{}, errors.New("raw-private-key-and-endpoint")
	}
	if _, err := s.Query(context.Background(), "local"); err == nil || err.Error() == "raw-private-key-and-endpoint" {
		t.Fatal("private adapter error escaped")
	}
	entries, err := s.Audits(context.Background())
	if err != nil || len(entries) != 1 || entries[0].Result != "unavailable" || strings.Contains(entries[0].Result, "raw-private-key-and-endpoint") {
		t.Fatal("safe query audit lost failure or exposed the private error", entries, err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	s.deps.Query = func(context.Context, string) (QuerySummary, error) {
		close(entered)
		<-release
		return QuerySummary{Available: true}, nil
	}
	done := make(chan error, 1)
	go func() { _, err := s.Query(context.Background(), "local"); done <- err }()
	<-entered
	cfg := s.Settings()
	cfg.Enabled = false
	if _, err := s.SaveSettings(context.Background(), SettingsInput{Settings: cfg}, Actor{UserID: 1}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; !errors.Is(err, ErrScope) {
		t.Fatal("disabled AI returned collected data", err)
	}
}

func TestGuestQueryResolvesOneExplicitNodeWithoutFallingBackToAnother(t *testing.T) {
	s, _, _ := aiFixture(t)
	ctx := context.Background()
	nodes := []database.Node{
		{ID: "remote-01", Name: "ganzhou", Enabled: true},
		{ID: "remote-02", Name: "赣州测试", Enabled: true},
		{ID: "outside-01", Name: "outside", Enabled: true},
		{ID: "disabled-01", Name: "paused", Enabled: false},
	}
	if err := s.db.Create(&nodes).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.db.Model(&database.Node{}).Where("id = ?", "disabled-01").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	var reads []string
	s.deps.Query = func(_ context.Context, node string) (QuerySummary, error) {
		reads = append(reads, node)
		return QuerySummary{Available: true}, nil
	}
	guest := Actor{Source: "chat", ExternalUserID: "guest", ChatID: "private"}
	for _, tc := range []struct {
		name, text, want string
		scope            []string
	}{
		{name: "user report", text: "发送ganzhou节点的信息给我", want: "remote-01", scope: []string{"local", "remote-01"}},
		{name: "English case", text: "Send GANZHOU node information", want: "remote-01", scope: []string{"local", "remote-01"}},
		{name: "Chinese name", text: "查询赣州测试节点状态", want: "remote-02", scope: []string{"local", "remote-02"}},
		{name: "natural ID", text: "发送remote-01节点的信息给我", want: "remote-01", scope: []string{"local", "remote-01"}},
		{name: "command ID", text: "/node remote-01 status", want: "remote-01", scope: []string{"local", "remote-01"}},
		{name: "command name", text: "/node GANZHOU status", want: "remote-01", scope: []string{"local", "remote-01"}},
		{name: "tab separator", text: "/node\tremote-01 status", want: "remote-01", scope: []string{"local", "remote-01"}},
		{name: "single default", text: "查询状态", want: "local", scope: []string{"local"}},
		{name: "similar name", text: "发送ganzhou2节点的信息给我", scope: []string{"remote-01"}},
		{name: "hyphenated name", text: "show ganzhou-backup node status", scope: []string{"remote-01"}},
		{name: "unknown node", text: "发送missing节点的信息给我", scope: []string{"local"}},
		{name: "unknown ID", text: "/node missing status", scope: []string{"local"}},
		{name: "unauthorized name", text: "发送outside节点的信息给我", scope: []string{"local"}},
		{name: "unauthorized without node keyword", text: "show outside status", scope: []string{"local"}},
		{name: "unauthorized ID", text: "/node outside-01 status", scope: []string{"local"}},
		{name: "authorized and unauthorized", text: "show ganzhou and outside nodes", scope: []string{"remote-01"}},
		{name: "disabled node", text: "show paused node status", scope: []string{"disabled-01"}},
		{name: "disabled default", text: "查询状态", scope: []string{"disabled-01"}},
		{name: "multiple names", text: "查询local和ganzhou节点", scope: []string{"local", "remote-01"}},
		{name: "missing multi-node target", text: "查询状态", scope: []string{"local", "remote-01"}},
		{name: "all nodes", text: "show all nodes", scope: []string{"local", "remote-01"}},
		{name: "missing command argument", text: "/node", scope: []string{"local"}},
		{name: "empty text", text: " ", scope: []string{"local"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := s.Settings()
			cfg.NodeIDs = tc.scope
			if _, err := s.SaveSettings(ctx, SettingsInput{Settings: cfg}, Actor{UserID: 1}); err != nil {
				t.Fatal(err)
			}
			before := len(reads)
			_, err := s.QueryTextAs(ctx, tc.text, guest)
			if tc.want == "" {
				if !errors.Is(err, ErrQueryTarget) || len(reads) != before {
					t.Fatal("unresolved target reached an adapter or silently selected another node", err, reads)
				}
			} else if err != nil || len(reads) != before+1 || reads[before] != tc.want {
				t.Fatal("query used the wrong node", err, reads)
			}
		})
	}
	// A name that collides with a real ID is ambiguous in prose. An explicit
	// /node ID still addresses that exact entry, regardless of its display name.
	if err := s.db.Create(&database.Node{ID: "ganzhou", Name: "another-node", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	cfg := s.Settings()
	cfg.NodeIDs = []string{"ganzhou", "remote-01"}
	if _, err := s.SaveSettings(ctx, SettingsInput{Settings: cfg}, Actor{UserID: 1}); err != nil {
		t.Fatal(err)
	}
	before := len(reads)
	if _, err := s.QueryTextAs(ctx, "发送ganzhou节点的信息给我", guest); !errors.Is(err, ErrQueryTarget) || len(reads) != before {
		t.Fatal("ambiguous node name was silently selected", err, reads)
	}
	if _, err := s.QueryTextAs(ctx, "/node ganzhou status", guest); err != nil || len(reads) != before+1 || reads[before] != "ganzhou" {
		t.Fatal("explicit node ID lost precedence", err, reads)
	}
	if err := s.db.Delete(&database.Node{}, "id = ?", "remote-01").Error; err != nil {
		t.Fatal(err)
	}
	before = len(reads)
	if _, err := s.QueryTextAs(ctx, "/node remote-01 status", guest); !errors.Is(err, ErrQueryTarget) || len(reads) != before {
		t.Fatal("deleted target reached the adapter", err)
	}
	cfg = s.Settings()
	cfg.Enabled = false
	cfg.NodeIDs = []string{"ganzhou"}
	if _, err := s.SaveSettings(ctx, SettingsInput{Settings: cfg}, Actor{UserID: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.QueryTextAs(ctx, "发送ganzhou节点的信息给我", guest); !errors.Is(err, ErrScope) || len(reads) != before {
		t.Fatal("disabled AI performed a query", err)
	}
}
