package model

import "time"

// Keep bounded, already-redacted failure evidence independently from acceptance.
// In particular, recording an error must never overwrite an accepted original.
func RecordVideoSubmissionError(id, owner string, status int, code, message, reference string, details ...string) error {
	diagnostic := ""
	if len(details) > 0 {
		diagnostic = details[0]
	}
	bound := func(s string, n int) string {
		r := []rune(s)
		if len(r) > n {
			r = r[:n]
		}
		return string(r)
	}
	return DB.Model(&VideoSubmission{}).Where("id = ? AND owner_token = ? AND status IN ?", id, owner,
		[]string{VideoSubmissionPending, VideoSubmissionSubmitting, VideoSubmissionUnknown, VideoSubmissionNotAccepted}).Updates(map[string]any{
		"error_status": status, "error_code": bound(code, 128), "error_message": bound(message, 1024),
		"error_details": diagnostic, "error_reference": bound(reference, 128), "error_recorded_at": time.Now().Unix(),
	}).Error
}
