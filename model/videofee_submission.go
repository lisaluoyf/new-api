package model

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

type VideoFeeEvidenceJSON string

func (VideoFeeEvidenceJSON) GormDataType() string { return "text" }
func (VideoFeeEvidenceJSON) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	if db.Dialector.Name() == "mysql" {
		return "LONGTEXT"
	}
	return "TEXT"
}

// Existing submissions default to disabled: deployment never scans old requests.
type VideoFeeReconciliation struct {
	ProviderLookupLastAt     int64                `json:"-"`
	ProviderLookupLastCode   string               `json:"-" gorm:"size:64"`
	ProviderLookupEnabled    bool                 `json:"-"`
	ProviderLookupNext       int64                `json:"-" gorm:"index"`
	ProviderLookupAttempts   int                  `json:"-"`
	ProviderLookupLease      string               `json:"-" gorm:"size:64"`
	ProviderLookupDone       bool                 `json:"-"`
	ProviderRefundRecordedAt int64                `json:"-"`
	ProviderRefundCompleted  bool                 `json:"-"`
	ProviderObservation      VideoFeeEvidenceJSON `json:"-"`
}

type VideoFeeLedgerEntry struct {
	ID        string `json:"id"`
	Type      string `json:"entry_type"`
	Credit    string `json:"amount_credit"`
	Yuan      string `json:"amount_yuan"`
	CreatedAt string `json:"created_at"`
}
type VideoFeeLedgerFacts struct {
	Status  string                `json:"status"`
	Entries []VideoFeeLedgerEntry `json:"entries"`
}
type VideoFeeBillingFacts struct {
	Status      string              `json:"status"`
	Currency    string              `json:"currency"`
	Reservation string              `json:"reservation_status"`
	Hold        VideoFeeLedgerFacts `json:"hold"`
	Release     VideoFeeLedgerFacts `json:"release"`
	Charge      VideoFeeLedgerFacts `json:"charge"`
	Refund      VideoFeeLedgerFacts `json:"refund"`
}
type VideoFeeTaskObservation struct {
	ID         string  `json:"id"`
	Model      string  `json:"model"`
	Status     string  `json:"status"`
	CreatedAt  string  `json:"created_at"`
	FinishedAt *string `json:"finished_at"`
	ResultURL  *string `json:"result_url"`
}
type VideoFeeSubmissionObservation struct {
	Object            string                   `json:"object"`
	Key               string                   `json:"idempotency_key"`
	Acceptance        string                   `json:"acceptance_status"`
	ObservedAt        string                   `json:"observed_at"`
	TaskID            *string                  `json:"task_id"`
	Task              *VideoFeeTaskObservation `json:"task"`
	Billing           VideoFeeBillingFacts     `json:"billing"`
	NewSubmissionSafe bool                     `json:"new_submission_safe"`
	RetryAfter        *int                     `json:"retry_after_seconds"`
}

var videoFeeUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func VideoFeeCredentialFingerprint(key string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimSpace(key))))
}

// Empty ledger facts remain unknown, never a fabricated zero charge.
func (o *VideoFeeSubmissionObservation) Validate(key string, now time.Time) error {
	if o.Object != "submission" || o.Key != key {
		return errors.New("submission identity mismatch")
	}
	observed, err := time.Parse(time.RFC3339Nano, o.ObservedAt)
	if err != nil || observed.After(now.Add(5*time.Minute)) || observed.Before(now.Add(-24*time.Hour)) {
		return errors.New("invalid submission snapshot time")
	}
	switch o.Acceptance {
	case "accepted":
		if o.NewSubmissionSafe || o.TaskID == nil || !videoFeeUUID.MatchString(*o.TaskID) || o.Task == nil || o.Task.ID != *o.TaskID {
			return errors.New("invalid accepted identity")
		}
		switch o.Task.Status {
		case "created", "queued", "processing", "submission_unknown", "succeeded", "failed", "cancelled", "refunded":
		default:
			return errors.New("unknown provider task status")
		}
	case "not_accepted":
		if !o.NewSubmissionSafe || o.TaskID != nil || o.Task != nil || o.Billing.Reservation != "none" {
			return errors.New("unfenced rejection")
		}
	case "processing", "acceptance_unknown":
		if o.NewSubmissionSafe || o.TaskID != nil || o.Task != nil {
			return errors.New("contradictory unknown acceptance")
		}
	default:
		return errors.New("unknown acceptance status")
	}
	seen := map[string]bool{}
	for kind, facts := range map[string]VideoFeeLedgerFacts{"hold": o.Billing.Hold, "release": o.Billing.Release, "capture": o.Billing.Charge, "refund": o.Billing.Refund} {
		if o.Acceptance == "not_accepted" && (facts.Status != "not_applicable" || len(facts.Entries) != 0) {
			return errors.New("rejection has applicable ledger")
		}
		if len(facts.Entries) > 0 && (facts.Status != "recorded" || o.Billing.Currency != "CNY") {
			return errors.New("invalid ledger facts")
		}
		for _, entry := range facts.Entries {
			if entry.ID == "" || seen[entry.ID] || entry.Type != kind {
				return errors.New("conflicting ledger entry")
			}
			seen[entry.ID] = true
			for _, amount := range []string{entry.Credit, entry.Yuan} {
				d, e := decimal.NewFromString(amount)
				if e != nil || d.IsNegative() {
					return errors.New("invalid ledger amount")
				}
			}
		}
	}
	return nil
}

// Each worker obtains a bounded lease before network I/O. No DB transaction
// spans the provider call; restarts and multiple workers cannot hot-loop a key.
func ClaimVideoFeeLookups(now int64, limit int) ([]VideoSubmission, error) {
	var rows, claimed []VideoSubmission
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	err := DB.Where("channel_id = ? AND provider_lookup_enabled = ? AND provider_lookup_done = ? AND provider_lookup_next <= ? AND updated_at <= ? AND status IN ?", 273, true, false, now, now-10, []string{VideoSubmissionUnknown, VideoSubmissionAccepted}).Where("task_ready = ? OR provider_observation <> ?", false, "").Order("provider_lookup_next ASC, id ASC").Limit(limit).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		token := common.GetUUID()
		r := DB.Model(&VideoSubmission{}).Where("id = ? AND provider_lookup_next = ? AND provider_lookup_lease = ? AND provider_lookup_done = ?", row.ID, row.ProviderLookupNext, row.ProviderLookupLease, false).Updates(map[string]any{"provider_lookup_next": now + 120, "provider_lookup_lease": token, "provider_lookup_attempts": gorm.Expr("provider_lookup_attempts + 1")})
		if r.Error != nil {
			return nil, r.Error
		}
		if r.RowsAffected == 1 {
			row.ProviderLookupLease = token
			row.ProviderLookupAttempts++
			claimed = append(claimed, row)
		}
	}
	return claimed, nil
}

func ScheduleVideoFeeLookup(row *VideoSubmission, now int64, retryAfter *int) error {
	delay := int64(10)
	for i := 1; i < row.ProviderLookupAttempts && delay < 300; i++ {
		delay *= 2
	}
	if delay > 300 {
		delay = 300
	}
	if retryAfter != nil && *retryAfter > int(delay) {
		delay = int64(*retryAfter)
		if delay > 3600 {
			delay = 3600
		}
	}
	return DB.Model(&VideoSubmission{}).Where("id = ? AND provider_lookup_lease = ?", row.ID, row.ProviderLookupLease).Update("provider_lookup_next", now+delay).Error
}

// Apply only authoritative identity evidence. This function never changes a
// wallet, token, reservation or customer settlement. Supplier CNY is not USD.
func ApplyVideoFeeObservation(row *VideoSubmission, o *VideoFeeSubmissionObservation, key string) error {
	if err := o.Validate(key, time.Now()); err != nil {
		return err
	}
	// Task private_data contains the original provider credential. Never log
	// SQL parameter values if an insertion/update fails.
	return DB.Session(&gorm.Session{Logger: logger.Discard}).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&VideoSubmission{}).Where("id = ?", row.ID).Update("updated_at", gorm.Expr("updated_at")).Error; err != nil {
			return err
		}
		var current VideoSubmission
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ? AND channel_id = ? AND provider_lookup_lease = ? AND provider_lookup_enabled = ?", row.ID, row.UserID, 273, row.ProviderLookupLease, true).First(&current).Error; err != nil {
			return err
		}
		if current.Status != VideoSubmissionUnknown && current.Status != VideoSubmissionAccepted {
			if current.Status == VideoSubmissionNotAccepted && o.Acceptance == "not_accepted" {
				return nil
			}
			return ErrVideoSubmissionClaimLost
		}
		if current.ProviderObservation != "" {
			var previous VideoFeeSubmissionObservation
			if common.UnmarshalJsonStr(string(current.ProviderObservation), &previous) != nil {
				return errors.New("invalid previous provider evidence")
			}
			p, _ := time.Parse(time.RFC3339Nano, previous.ObservedAt)
			n, _ := time.Parse(time.RFC3339Nano, o.ObservedAt)
			if n.Before(p) {
				return errors.New("stale provider snapshot")
			}
		}
		if current.UpstreamTaskID != "" && (o.TaskID == nil || *o.TaskID != current.UpstreamTaskID) {
			return errors.New("conflicting original task identity")
		}
		raw, err := common.Marshal(o)
		if err != nil {
			return err
		}
		updates := map[string]any{"provider_observation": string(raw)}
		var snapshot VideoSubmissionRecovery
		if current.RecoveryJSON == "" || common.UnmarshalJsonStr(string(current.RecoveryJSON), &snapshot) != nil {
			return errors.New("missing original recovery snapshot")
		}
		if snapshot.Task.TaskID != current.TaskID || snapshot.Task.UserId != current.UserID || snapshot.Task.ChannelId != 273 || snapshot.Task.Properties.OriginModelName != current.Model || snapshot.Task.ID != 0 || snapshot.Task.Quota < 0 || snapshot.PrivateData.UpstreamTaskID != "" {
			return errors.New("original snapshot identity mismatch")
		}
		refunded, err := videoFeeFinancialReady(tx, &current, &snapshot)
		if err != nil {
			return err
		}
		ready := refunded || current.RecoverySettled
		if refunded {
			updates["provider_refund_completed"] = true
		}
		if o.Acceptance == "not_accepted" && refunded && current.UpstreamTaskID == "" && !current.TaskReady {
			var count int64
			if err := tx.Model(&Task{}).Where("task_id = ?", current.TaskID).Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				return errors.New("rejected request has local task")
			}
			updates["status"] = VideoSubmissionNotAccepted
			updates["provider_lookup_done"] = true
		}
		if o.Acceptance == "accepted" && ready {
			var tasks []Task
			if err := tx.Where("task_id = ?", current.TaskID).Limit(2).Find(&tasks).Error; err != nil {
				return err
			}
			if len(tasks) > 1 {
				return errors.New("ambiguous original task")
			}
			if len(tasks) == 1 && (tasks[0].UserId != current.UserID || tasks[0].ChannelId != 273 || tasks[0].PrivateData.UpstreamTaskID != *o.TaskID) {
				return errors.New("local task identity conflict")
			}
			if len(tasks) == 1 && !tasks[0].PrivateData.ProviderObservationOnly {
				updates["provider_lookup_done"] = true
			}
			if len(tasks) == 0 || tasks[0].PrivateData.ProviderObservationOnly {
				task := snapshot.Task
				if task.CreatedAt == 0 {
					task.CreatedAt = current.CreatedAt
				}
				if task.SubmitTime == 0 {
					task.SubmitTime = current.CreatedAt
				}
				if refunded {
					task.Quota = 0
				} // Preserve original reservation in the snapshot, not as a new customer charge.
				task.PrivateData = snapshot.PrivateData
				task.PrivateData.ProviderObservationOnly = true
				task.PrivateData.Key = ""
				task.PrivateData.UpstreamTaskID = *o.TaskID
				task.PrivateData.SeedanceFallback = nil
				if err := videoFeeBindTaskKey(tx, &current, &snapshot, &task); err != nil {
					return err
				}
				task.Status = TaskStatusInProgress
				task.Progress = "30%"
				if o.Task.Status == "succeeded" {
					task.Status = TaskStatusSuccess
					task.Progress = "100%"
					// Use the authenticated original content endpoint. Cache delivery URLs may
					// be absent, relative, expired or private; the worker never downloads them.
					task.PrivateData.ResultURL = "https://seedance2026.vip/v1/videos/" + *o.TaskID + "/content"
					updates["provider_lookup_done"] = true
				}
				if o.Task.Status == "failed" || o.Task.Status == "cancelled" || o.Task.Status == "refunded" {
					task.Status = TaskStatusFailure
					task.Progress = "100%"
					task.FailReason = "Original provider task ended without a result"
					updates["provider_lookup_done"] = true
				}
				if task.Progress == "100%" && o.Task.FinishedAt != nil {
					if at, e := time.Parse(time.RFC3339Nano, *o.Task.FinishedAt); e == nil {
						task.FinishTime = at.Unix()
					}
				}
				task.Data, _ = common.Marshal(map[string]any{"status": o.Task.Status, "model": current.Model})
				if len(tasks) == 0 {
					task.ID = 0
					if err := tx.Create(&task).Error; err != nil {
						return err
					}
				} else {
					existing := tasks[0]
					if existing.Status != TaskStatusSuccess && existing.Status != TaskStatusFailure {
						task.ID = existing.ID
						if err := tx.Model(&existing).Select("status", "progress", "finish_time", "fail_reason", "private_data", "data").Updates(&task).Error; err != nil {
							return err
						}
					}
				}
			}
			if err := videoFeePublishBilling(tx, &current, &snapshot, refunded); err != nil {
				return err
			}
			updates["status"] = VideoSubmissionAccepted
			updates["upstream_task_id"] = *o.TaskID
			updates["task_ready"] = true
		}
		return tx.Model(&VideoSubmission{}).Where("id = ?", current.ID).Updates(updates).Error
	})
}

func videoFeeBindTaskKey(tx *gorm.DB, row *VideoSubmission, snapshot *VideoSubmissionRecovery, task *Task) error {
	var channel Channel
	if err := tx.First(&channel, row.ChannelID).Error; err != nil {
		return err
	}
	for _, key := range channel.GetKeys() {
		if VideoFeeCredentialFingerprint(key) == snapshot.CredentialFingerprint {
			task.PrivateData.Key = key
			return nil
		}
	}
	return fmt.Errorf("original provider credential unavailable")
}

// Keep read-only originals out of both ordinary polling and timeout queues,
// before LIMIT is applied. They cannot starve normal tasks or trigger a poll
// endpoint that performs supplier settlement. All three supported DBs retain
// the private flag in their existing JSON column; no tasks-table migration.
func ordinaryTaskQuery(db *gorm.DB) *gorm.DB {
	switch db.Dialector.Name() {
	case "postgres":
		return db.Where("COALESCE(private_data->>'provider_observation_only', 'false') <> 'true'")
	case "mysql":
		return db.Where("IF(JSON_VALID(private_data), COALESCE(JSON_UNQUOTE(JSON_EXTRACT(private_data, '$.provider_observation_only')), 'false'), 'false') <> 'true'")
	case "sqlite":
		return db.Where("CASE WHEN json_valid(private_data) THEN COALESCE(json_extract(private_data, '$.provider_observation_only'), 0) ELSE 0 END <> 1")
	}
	return db
}

// This audit update uses the existing lookup lease and never touches billing,
// acceptance, scheduling or historical rows that have lookup disabled.
func RecordVideoFeeLookupOutcome(row *VideoSubmission, at int64, code string) error {
	if code != "provider_observation_recorded" && code != "provider_reconciliation_unavailable" {
		return errors.New("invalid lookup outcome")
	}
	return DB.Model(&VideoSubmission{}).Where("id = ? AND provider_lookup_lease = ? AND provider_lookup_enabled = ?", row.ID, row.ProviderLookupLease, true).UpdateColumns(map[string]any{"provider_lookup_last_at": at, "provider_lookup_last_code": code}).Error
}
