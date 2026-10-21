package model

import (
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

const ImageSubmissionLeaseSeconds = int64(1800)
const ImageSubmissionMaxResponseBytes = 64 << 20

// Keys are permanent tombstones: expiration never authorizes another execution.
// Request bodies and raw keys are not retained. Results are private account data.
type ImageSubmission struct {
	ID               string `gorm:"primaryKey;size:64"`
	UserID           int    `gorm:"index"`
	Owner            string `gorm:"size:64"`
	Fingerprint      string `gorm:"size:64"`
	Model            string `gorm:"size:128"`
	Path             string `gorm:"size:256"`
	RequestID        string `gorm:"size:128"`
	Status           string `gorm:"size:32"`
	TaskID           string `gorm:"size:128"`
	ResponseCode     int
	ResponseType     string `gorm:"size:128"`
	ResponseBody     ImageSubmissionBody
	ResponseComplete bool
	CreatedAt        int64
	UpdatedAt        int64
	Deadline         int64
}

type ImageSubmissionBody string

func (ImageSubmissionBody) GormDataType() string { return "string" }
func (ImageSubmissionBody) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	if db.Dialector.Name() == "mysql" {
		return "LONGTEXT"
	}
	return "TEXT"
}

var ErrImageSubmissionConflict = errors.New("Idempotency-Key was used for a different image request")
var ErrImageSubmissionClaimLost = errors.New("image submission execution claim is no longer valid")

func ClaimImageSubmission(row *ImageSubmission) (*ImageSubmission, bool, error) {
	row.Owner = common.GetUUID()
	row.Status = "pending"
	row.CreatedAt = time.Now().Unix()
	row.UpdatedAt = row.CreatedAt
	row.Deadline = row.CreatedAt + ImageSubmissionLeaseSeconds
	if err := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(row).Error; err != nil {
		return nil, false, err
	}
	saved, err := GetImageSubmission(row.UserID, row.ID)
	if err != nil {
		return nil, false, err
	}
	if saved.Fingerprint != row.Fingerprint {
		return saved, false, ErrImageSubmissionConflict
	}
	return saved, saved.Owner == row.Owner, nil
}

// Strictly read-only. Pending/submitting receipts remain unknown after a crash;
// neither a lease timeout nor an absent record proves non-acceptance.
func GetImageSubmission(userID int, id string) (*ImageSubmission, error) {
	var row ImageSubmission
	err := DB.Where("id = ? AND user_id = ?", id, userID).First(&row).Error
	return &row, err
}

func BeginImageSubmission(row *ImageSubmission) error {
	now := time.Now().Unix()
	r := DB.Model(&ImageSubmission{}).Where("id = ? AND owner = ? AND status = ? AND deadline > ?", row.ID, row.Owner, "pending", now).
		Updates(map[string]any{"status": "submitting", "updated_at": now})
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return ErrImageSubmissionClaimLost
	}
	return nil
}

func FinishImageSubmission(row *ImageSubmission, status, taskID, contentType string, code int, body []byte, complete bool) error {
	r := DB.Model(&ImageSubmission{}).Where("id = ? AND owner = ? AND status IN ?", row.ID, row.Owner, []string{"pending", "submitting"}).Updates(map[string]any{
		"status": status, "task_id": taskID, "response_type": contentType, "response_code": code,
		"response_body": string(body), "response_complete": complete, "updated_at": time.Now().Unix(),
	})
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return ErrImageSubmissionClaimLost
	}
	return nil
}
