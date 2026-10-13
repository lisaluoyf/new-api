package model

// VideoFeeWebhookInbox durably records authenticated supplier events before ACK.
// Event IDs remain available for deduplication for at least seven days.
type VideoFeeWebhookInbox struct {
	ID          string `gorm:"primaryKey;type:varchar(128)"`
	TaskID      int64  `gorm:"index"`
	Payload     string `gorm:"type:text"`
	Status      string `gorm:"type:varchar(16);index"`
	ReceivedAt  int64
	ProcessedAt int64
	NextAt      int64 `gorm:"index"`
	LeaseUntil  int64
	Attempts    int
}
