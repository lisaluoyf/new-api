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

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
)

type imageSubmissionFileFingerprint struct {
	Field       string
	Digest      string
	ContentType string
}

func ImageSubmissionFingerprint(c *gin.Context) (string, string, error) {
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
	var files []imageSubmissionFileFingerprint
	scalarDigests := map[string]string{}
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
				files = append(files, imageSubmissionFileFingerprint{Field: name, Digest: fmt.Sprintf("%x", hash.Sum(nil)), ContentType: part.Header.Get("Content-Type")})
			} else {
				if _, exists := fields[name]; exists {
					return "", "", fmt.Errorf("duplicate multipart field %s", name)
				}
				data, err := io.ReadAll(part)
				if err != nil {
					return "", "", err
				}
				scalarDigests[name] = fmt.Sprintf("%x", sha256.Sum256(data))
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
	_ = common.Unmarshal(fields["model"], &name)
	if name == "" {
		if marker := bytes.Index([]byte(c.Request.URL.Path), []byte("/models/")); marker >= 0 {
			name = c.Request.URL.Path[marker+8:]
			for i, ch := range name {
				if ch == ':' {
					name = name[:i]
					break
				}
			}
		}
	}
	normalized, err := common.Marshal(fields)
	if err != nil {
		return "", "", err
	}
	if contentType == "application/json" {
		normalized, err = common.Marshal(fmt.Sprintf("%x", sha256.Sum256(raw)))
	}
	normalized, err = canonicalSubmissionJSON(normalized)
	if err != nil {
		return "", "", err
	}
	// Group and explicit routing affect price/provider. API key identity itself
	// does not: another valid key in the same account may recover the receipt.
	envelope, err := common.Marshal(map[string]any{"path": c.Request.URL.Path, "body": json.RawMessage(normalized), "files": files, "scalar_digests": scalarDigests, "transport": contentType, "group": common.GetContextKeyString(c, constant.ContextKeyUsingGroup), "channel": c.GetString("specific_channel_id")})
	if err != nil {
		return "", "", err
	}
	return name, fmt.Sprintf("%x", sha256.Sum256(envelope)), nil
}
