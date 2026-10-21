package model

// Read-only queue with stable ordering; no provider I/O or financial mutation.
func ListVideoSubmissionReviews(before int64, offset, limit int) ([]VideoSubmission, error) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > 51 {
		limit = 51
	}
	var rows []VideoSubmission
	err := DB.Where("provider_lookup_enabled = ? AND task_ready = ? AND created_at > ? AND created_at <= ? AND status IN ?", true, false, 0, before, []string{VideoSubmissionUnknown, VideoSubmissionSubmitting, VideoSubmissionAccepted}).Order("created_at ASC, id ASC").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, err
}
