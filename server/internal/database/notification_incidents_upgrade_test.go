package database_test

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/testutil"
)

func TestNotificationIncidentUpgradePreservesHistoryAndQueuedEvidence(t *testing.T) {
	dsn := testutil.DSN(t)
	connection, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	for _, name := range []string{"0001_initial.sql", "0002_container_monitoring.sql"} {
		raw, err := os.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := connection.ExecContext(context.Background(), string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		"CREATE TABLE schema_migrations (key varchar(128) PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())",
		"INSERT INTO schema_migrations(key) VALUES ('0001_initial.sql'),('0002_container_monitoring.sql')",
		"INSERT INTO users(id,username,password_hash) VALUES (41,'retained','retained-password-hash')",
		"INSERT INTO nodes(id,name,connection_type,endpoint,engine_id) VALUES ('node','Retained','unix','unix:///fixture.sock','retained-engine')",
		"INSERT INTO registry_credentials(id,name,server_address,auth_type,secret_ciphertext) VALUES (51,'Retained','registry.test','token',decode('deadbeef','hex'))",
		"INSERT INTO settings(key,value) VALUES ('general.server_name','Retained server')",
		"INSERT INTO tasks(id,type,name,status) VALUES ('retained-task','fixture','Retained task','pending')",
		"INSERT INTO audit_logs(id,action,result,details) VALUES (61,'fixture','success','Retained audit')",
		"INSERT INTO notification_channels(id,version,destination_version,config_json,secret_ciphertext) VALUES ('channel',8,2,'original-subscription',decode('aabb','hex'))",
		"INSERT INTO notification_rules(id,name,version,config_json) VALUES ('rule','Retained rule',6,'original-filter')",
		"INSERT INTO notification_events(id,type,severity,node_id,dedupe_key,data_json,created_at) VALUES ('event','container.oom','error','node','original-key','original-event',now())",
		"INSERT INTO notification_reads(user_id,event_id,read_at) VALUES (41,'event',now())",
		"INSERT INTO notification_deliveries(id,channel_id,status,snapshot_json) VALUES ('pending','channel','pending','queued-snapshot'),('sent','channel','sent','historical-snapshot')",
		"INSERT INTO notification_delivery_events(delivery_id,event_id,dedupe_key,matched_rules_json) VALUES ('pending','event','retained-dedupe','original-rule-evidence')",
	} {
		if _, err := connection.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	db, err := database.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := db.DB()
	defer pool.Close()
	var user database.User
	if err := db.First(&user, 41).Error; err != nil || user.Username != "retained" || user.PasswordHash != "retained-password-hash" {
		t.Fatal("account changed", err)
	}
	var node database.Node
	if err := db.First(&node, "id = ?", "node").Error; err != nil || node.EngineID != "retained-engine" {
		t.Fatal("node changed", err)
	}
	var credential database.RegistryCredential
	if err := db.First(&credential, 51).Error; err != nil || string(credential.SecretCiphertext) != string([]byte{0xde, 0xad, 0xbe, 0xef}) {
		t.Fatal("registry credential changed", err)
	}
	var setting database.Setting
	if err := db.First(&setting, "key = ?", "general.server_name").Error; err != nil || setting.Value != "Retained server" {
		t.Fatal("setting changed", err)
	}
	var task database.Task
	if err := db.First(&task, "id = ?", "retained-task").Error; err != nil || task.Status != "pending" {
		t.Fatal("queued task changed", err)
	}
	var audit database.AuditLog
	if err := db.First(&audit, 61).Error; err != nil || audit.Details != "Retained audit" {
		t.Fatal("audit history changed", err)
	}
	var channel database.NotificationChannel
	if err := db.First(&channel, "id = ?", "channel").Error; err != nil || channel.Version != 8 || channel.DestinationVersion != 2 || channel.ConfigJSON != "original-subscription" || string(channel.SecretCiphertext) != string([]byte{0xaa, 0xbb}) {
		t.Fatal("channel changed", channel.ID, err)
	}
	var rule database.NotificationRule
	if err := db.First(&rule, "id = ?", "rule").Error; err != nil || rule.Version != 6 || rule.ConfigJSON != "original-filter" {
		t.Fatal("rule changed", err)
	}
	var event database.NotificationEvent
	if err := db.First(&event, "id = ?", "event").Error; err != nil || event.DataJSON != "original-event" || event.DedupeKey != "original-key" || event.IncidentKey != "" || event.Occurrences != 0 || event.LastOccurredAt != nil || event.ResolvedAt != nil {
		t.Fatal("old history rewritten or current fault fabricated", event, err)
	}
	var link database.NotificationDeliveryEvent
	if err := db.First(&link, "delivery_id = ?", "pending").Error; err != nil || link.MatchedRulesJSON != "original-rule-evidence" || link.DedupeKey == nil || *link.DedupeKey != "retained-dedupe" {
		t.Fatal("delivery links changed", link, err)
	}
	for id, snapshot := range map[string]string{"pending": "queued-snapshot", "sent": "historical-snapshot"} {
		var delivery database.NotificationDelivery
		if err := db.First(&delivery, "id = ?", id).Error; err != nil || delivery.SnapshotJSON != snapshot {
			t.Fatal("delivery snapshot changed", id, err)
		}
	}
	var count int64
	db.Model(&database.NotificationRead{}).Where("user_id = ? AND event_id = ?", 41, "event").Count(&count)
	if count != 1 {
		t.Fatal("read evidence lost")
	}
	again, err := database.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	againPool, _ := again.DB()
	defer againPool.Close()
	again.Model(&database.SchemaMigration{}).Count(&count)
	if count != 5 {
		t.Fatal("migration replayed", count)
	}
}
