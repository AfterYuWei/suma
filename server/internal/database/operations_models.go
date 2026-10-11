package database

import "time"

type NotificationChannel struct {
	ID                 string     `gorm:"primaryKey;size:64" json:"id"`
	Name               string     `json:"name"`
	Provider           string     `json:"provider"`
	Enabled            bool       `json:"enabled"`
	Version            uint64     `json:"version"`
	DestinationVersion uint64     `json:"-"`
	ConfigJSON         string     `json:"-"`
	SecretCiphertext   []byte     `json:"-"`
	LastError          string     `json:"last_error,omitempty"`
	LastSentAt         *time.Time `json:"last_sent_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}
type NotificationRule struct {
	ID         string    `gorm:"primaryKey;size:64" json:"id"`
	Name       string    `json:"name"`
	Version    uint64    `json:"version"`
	ConfigJSON string    `json:"-"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// These rows are historical notification evidence, not current Docker state.
type NotificationEvent struct {
	ID        string    `gorm:"primaryKey;size:64" json:"id"`
	Type      string    `gorm:"index" json:"type"`
	Severity  string    `json:"severity"`
	NodeID    string    `gorm:"index" json:"node_id,omitempty"`
	DedupeKey string    `gorm:"index" json:"-"`
	DataJSON  string    `json:"-"`
	CreatedAt time.Time `gorm:"index" json:"created_at"`
}
type NotificationRead struct {
	UserID  uint   `gorm:"primaryKey"`
	EventID string `gorm:"primaryKey;size:64"`
	ReadAt  time.Time
}
type NotificationDelivery struct {
	ID                         string     `gorm:"primaryKey;size:64" json:"id"`
	ChannelID                  string     `gorm:"index" json:"channel_id"`
	ChatID                     string     `gorm:"size:128" json:"chat_id,omitempty"`
	Source                     string     `json:"source"`
	OriginDeliveryID           string     `json:"origin_delivery_id,omitempty"`
	DestinationVersion         uint64     `json:"-"`
	FallbackDestinationVersion uint64     `json:"-"`
	SnapshotJSON               string     `json:"-"`
	BatchKey                   string     `gorm:"index" json:"-"`
	Status                     string     `gorm:"index" json:"status"`
	Reason                     string     `json:"reason,omitempty"`
	Attempts                   int        `json:"attempts"`
	DueAt                      time.Time  `gorm:"index" json:"due_at"`
	LeaseUntil                 *time.Time `gorm:"index" json:"-"`
	ProviderMessageID          string     `json:"provider_message_id,omitempty"`
	CreatedAt                  time.Time  `json:"created_at"`
	UpdatedAt                  time.Time  `json:"updated_at"`
}

// Automatic delivery claims span primary and fallback routes. Manual resends
// have a NULL dedupe key so an explicit resend never consumes another claim.
type NotificationDeliveryEvent struct {
	DeliveryID       string  `gorm:"primaryKey;size:64" json:"-"`
	EventID          string  `gorm:"primaryKey;size:64;index" json:"-"`
	DedupeKey        *string `gorm:"size:64;uniqueIndex" json:"-"`
	MatchedRulesJSON string  `json:"-"`
}
type NotificationIncoming struct {
	Key       string    `gorm:"primaryKey;size:256"`
	CreatedAt time.Time `gorm:"index"`
}
type NotificationChat struct {
	ChannelID string    `gorm:"primaryKey;size:64" json:"channel_id"`
	ChatID    string    `gorm:"primaryKey;size:128" json:"chat_id"`
	Name      string    `json:"name"`
	Private   bool      `json:"private"`
	UpdatedAt time.Time `json:"updated_at"`
}
