package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

const VideoSubmissionContextKey = "durable_video_submission"
const VideoSubmissionSentContextKey = "durable_video_submission_sent"

func ValidVideoSubmissionKey(key string) bool {
	if len(key) < 8 || len(key) > 128 {
		return false
	}
	for _, ch := range []byte(key) {
		if ch < 33 || ch > 126 {
			return false
		}
	}
	return true
}
func VideoSubmissionID(userID int, key string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte("seedance-video-v1:"+strconv.Itoa(userID)+":"+key)))
}
func VideoSubmissionFromContext(c *gin.Context) *model.VideoSubmission {
	if value, ok := c.Get(VideoSubmissionContextKey); ok {
		record, _ := value.(*model.VideoSubmission)
		return record
	}
	return nil
}

// Canonicalize recursively without rounding large JSON integers through float64.
// Array order and numeric spelling remain significant; omitted and explicit
// defaults are intentionally distinct. Do not resolve mutable media URLs here.
func canonicalSubmissionJSON(raw json.RawMessage) (json.RawMessage, error) {
	switch common.GetJsonType(raw) {
	case "object":
		var fields map[string]json.RawMessage
		if err := common.Unmarshal(raw, &fields); err != nil {
			return nil, err
		}
		for key, value := range fields {
			normalized, err := canonicalSubmissionJSON(value)
			if err != nil {
				return nil, err
			}
			fields[key] = normalized
		}
		return common.Marshal(fields)
	case "array":
		var fields []json.RawMessage
		if err := common.Unmarshal(raw, &fields); err != nil {
			return nil, err
		}
		for i, value := range fields {
			normalized, err := canonicalSubmissionJSON(value)
			if err != nil {
				return nil, err
			}
			fields[i] = normalized
		}
		return common.Marshal(fields)
	case "string":
		var value string
		if err := common.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		return common.Marshal(value)
	default:
		var value json.RawMessage
		if err := common.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		return bytes.TrimSpace(value), nil
	}
}

type submissionFileFingerprint struct {
	Field  string
	Digest string
}

func VideoSubmissionFingerprint(c *gin.Context) (string, string, error) {
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return "", "", err
	}
	raw, err := storage.Bytes()
	if err != nil {
		return "", "", err
	}
	contentType, params, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil {
		return "", "", fmt.Errorf("valid Content-Type is required")
	}
	fields := map[string]json.RawMessage{}
	var files []submissionFileFingerprint
	switch contentType {
	case "application/json":
		if err := common.Unmarshal(raw, &fields); err != nil || fields == nil {
			return "", "", fmt.Errorf("a JSON object is required")
		}
	case "multipart/form-data":
		reader := multipart.NewReader(bytes.NewReader(raw), params["boundary"])
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", "", fmt.Errorf("invalid multipart body")
			}
			name := part.FormName()
			if part.FileName() != "" {
				hash := sha256.New()
				if _, err := io.Copy(hash, part); err != nil {
					return "", "", err
				}
				files = append(files, submissionFileFingerprint{Field: name, Digest: fmt.Sprintf("%x", hash.Sum(nil))})
			} else {
				if _, exists := fields[name]; exists {
					return "", "", fmt.Errorf("duplicate multipart field %s", name)
				}
				data, err := io.ReadAll(part)
				if err != nil {
					return "", "", err
				}
				fields[name], err = common.Marshal(string(data))
				if err != nil {
					return "", "", err
				}
			}
			part.Close()
		}
		// Group by upload field, keeping order within each field.
		sort.SliceStable(files, func(i, j int) bool { return files[i].Field < files[j].Field })
	default:
		return "", "", fmt.Errorf("use application/json or multipart/form-data")
	}
	name := ""
	if err := common.Unmarshal(fields["model"], &name); err != nil {
		return "", "", fmt.Errorf("model is required")
	}
	normalized, err := common.Marshal(fields)
	if err != nil {
		return "", "", err
	}
	normalized, err = canonicalSubmissionJSON(normalized)
	if err != nil {
		return "", "", err
	}
	// Group and explicit routing affect price/provider. API key identity itself
	// does not: another valid key in the same account may recover the receipt.
	envelope, err := common.Marshal(map[string]any{"body": json.RawMessage(normalized), "files": files, "transport": contentType, "group": common.GetContextKeyString(c, constant.ContextKeyUsingGroup), "channel": c.GetString("specific_channel_id")})
	if err != nil {
		return "", "", err
	}
	return name, fmt.Sprintf("%x", sha256.Sum256(envelope)), nil
}

func VideoSubmissionDTO(record *model.VideoSubmission) map[string]any {
	action := "query_submission"
	if record.Status == model.VideoSubmissionAccepted && record.TaskReady {
		action = "query_task"
	}
	if record.Status == model.VideoSubmissionUnknown || (record.Status == model.VideoSubmissionAccepted && !record.TaskReady) {
		action = "contact_support_do_not_resubmit"
	}
	if record.Status == model.VideoSubmissionNotAccepted {
		action = "new_key_after_correcting_error"
	}
	return map[string]any{"submission_id": record.ID, "task_id": record.TaskID, "request_id": record.RequestID, "model": record.Model, "status": record.Status, "task_ready": record.TaskReady, "created_at": record.CreatedAt, "updated_at": record.UpdatedAt, "next_action": action, "same_key_replay_safe": true, "new_submission_safe": record.Status == model.VideoSubmissionNotAccepted, "retention": "at_least_30_days_no_automatic_key_reuse"}
}

func MarkVideoSubmissionSending(c *gin.Context, channelID int) error {
	record := VideoSubmissionFromContext(c)
	if record == nil {
		return nil
	}
	if err := model.MarkVideoSubmissionSending(record.ID, channelID); err != nil {
		return err
	}
	c.Set(VideoSubmissionSentContextKey, true)
	return nil
}

func IsSeedanceSubmissionPath(path string) bool {
	return strings.EqualFold(path, "/v1/videos") || path == "/v1/videos/generations" || path == "/v1/video/generations"
}
