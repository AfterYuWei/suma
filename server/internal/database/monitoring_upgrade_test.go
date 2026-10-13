package database_test

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/testutil"
)

func TestMonitoringUpgradeRetainsProductionEvidence(t *testing.T) {
	dsn := testutil.DSN(t)
	connection, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal("open isolated schema failed")
	}
	defer connection.Close()
	baseline, err := os.ReadFile("migrations/0001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.ExecContext(context.Background(), string(baseline)); err != nil {
		t.Fatal("apply supported previous baseline", err)
	}
	fixtures := []string{
		"CREATE TABLE schema_migrations (key varchar(128) PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())",
		"INSERT INTO schema_migrations(key) VALUES ('0001_initial.sql')",
		"INSERT INTO users(id,username,email,password_hash) VALUES (41,'retained','retained@example.test','fixture-password-hash')",
		"INSERT INTO nodes(id,name,connection_type,endpoint,engine_id) VALUES ('retained-node','Retained','unix','unix:///fixture.sock','retained-engine')",
		"INSERT INTO registry_credentials(id,name,server_address,auth_type,secret_ciphertext) VALUES (51,'Retained','registry.test','token',decode('deadbeef','hex'))",
		"INSERT INTO settings(key,value) VALUES ('general.server_name','Retained server')",
		"INSERT INTO tasks(id,type,name,status) VALUES ('retained-task','fixture','Retained task','pending')",
		"INSERT INTO audit_logs(id,action,result,details) VALUES (61,'fixture','success','Retained audit')",
		"INSERT INTO notification_channels(id,secret_ciphertext,config_json) VALUES ('retained-channel',decode('aabb','hex'),'retained-config')",
		"INSERT INTO notification_deliveries(id,channel_id,status,snapshot_json) VALUES ('queued-delivery','retained-channel','pending','retained-snapshot'),('historical-delivery','retained-channel','sent','historical-snapshot')",
	}
	for _, statement := range fixtures {
		if _, err := connection.ExecContext(context.Background(), statement); err != nil {
			t.Fatal("seed prior-schema fixture", err)
		}
	}
	upgraded, err := database.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := upgraded.DB()
	defer pool.Close()
	var user database.User
	upgraded.First(&user, 41)
	if user.ID != 41 || user.PasswordHash != "fixture-password-hash" {
		t.Fatal("upgrade lost account ID or credentials")
	}
	var credential database.RegistryCredential
	upgraded.First(&credential, 51)
	if string(credential.SecretCiphertext) != string([]byte{0xde, 0xad, 0xbe, 0xef}) {
		t.Fatal("upgrade changed encrypted registry material")
	}
	var node database.Node
	upgraded.First(&node, "id = ?", "retained-node")
	if node.EngineID != "retained-engine" {
		t.Fatal("upgrade changed node")
	}
	var setting database.Setting
	upgraded.First(&setting, "key = ?", "general.server_name")
	if setting.Value != "Retained server" {
		t.Fatal("upgrade changed settings")
	}
	var queued database.Task
	upgraded.First(&queued, "id = ?", "retained-task")
	if queued.Status != "pending" {
		t.Fatal("migration changed queued Task")
	}
	var delivery database.NotificationDelivery
	upgraded.First(&delivery, "id = ?", "queued-delivery")
	if delivery.SnapshotJSON != "retained-snapshot" {
		t.Fatal("upgrade lost queued delivery evidence")
	}
	var historical database.NotificationDelivery
	upgraded.First(&historical, "id = ?", "historical-delivery")
	if historical.Status != "sent" || historical.SnapshotJSON != "historical-snapshot" {
		t.Fatal("upgrade lost historical evidence")
	}
	var policy database.MonitoringPolicy
	if err := upgraded.First(&policy, 1).Error; err != nil || policy.Revision != 1 {
		t.Fatal("monitoring defaults missing", err)
	}
	reopened, err := database.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := reopened.DB()
	defer again.Close()
	var migrations int64
	reopened.Model(&database.SchemaMigration{}).Count(&migrations)
	if migrations != 2 {
		t.Fatal("migration ledger replayed or lost entries")
	}
}
