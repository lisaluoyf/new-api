package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// Parse notification configuration once, before distribution, charging or upstream I/O.
func TaskWebhookNotifications() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodPost {
			c.Next()
			return
		}
		path := c.Request.URL.Path
		if !strings.Contains(path, "video") && !strings.Contains(path, "images/") && !strings.Contains(path, "midjourney/") && !strings.HasPrefix(path, "/suno/") {
			c.Next()
			return
		}
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			c.Next()
			return
		}
		body, err := storage.Bytes()
		if err != nil {
			c.Next()
			return
		}
		contentType := c.GetHeader("Content-Type")
		media, params, _ := mime.ParseMediaType(contentType)
		var notification json.RawMessage
		modelName := ""
		reference := ""
		legacy := false
		var cleaned []byte
		var fields map[string]json.RawMessage
		if media == "application/json" {
			if common.Unmarshal(body, &fields) != nil {
				c.Next()
				return
			}
			notification = fields["notifications"]
			_ = common.Unmarshal(fields["model"], &modelName)
			_ = common.Unmarshal(fields["client_reference_id"], &reference)
			_, legacy1 := fields["webhook"]
			_, legacy2 := fields["callback_url"]
			legacy = legacy1 || legacy2
			if len(notification) == 0 {
				c.Next()
				return
			}
			delete(fields, "notifications")
			delete(fields, "client_reference_id")
			cleaned, err = common.Marshal(fields)
		} else if media == "multipart/form-data" {
			if !bytes.Contains(body, []byte(`name="notifications"`)) {
				storage.Seek(0, io.SeekStart)
				c.Request.Body = io.NopCloser(storage)
				c.Next()
				return
			}
			reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
			var output bytes.Buffer
			writer := multipart.NewWriter(&output)
			for {
				part, e := reader.NextPart()
				if e == io.EOF {
					break
				}
				if e != nil {
					err = e
					break
				}
				name := part.FormName()
				if part.FileName() == "" && (name == "notifications" || name == "client_reference_id" || name == "model" || name == "webhook" || name == "callback_url") {
					raw, e := io.ReadAll(io.LimitReader(part, 65537))
					if e != nil || len(raw) > 65536 {
						err = io.ErrUnexpectedEOF
						break
					}
					switch name {
					case "notifications":
						notification = raw
						continue
					case "client_reference_id":
						reference = string(raw)
						continue
					case "model":
						modelName = string(raw)
					case "webhook", "callback_url":
						legacy = true
					}
					dst, e := writer.CreatePart(part.Header)
					if e != nil {
						err = e
						break
					}
					_, err = dst.Write(raw)
				} else {
					dst, e := writer.CreatePart(part.Header)
					if e != nil {
						err = e
						break
					}
					_, err = io.Copy(dst, part)
				}
				if err != nil {
					break
				}
			}
			if len(notification) == 0 {
				storage.Seek(0, io.SeekStart)
				c.Request.Body = io.NopCloser(storage)
				c.Next()
				return
			}
			if err == nil {
				err = writer.Close()
				cleaned = output.Bytes()
				contentType = writer.FormDataContentType()
			}
		} else {
			c.Next()
			return
		}
		reject := func(message string) {
			c.AbortWithStatusJSON(400, gin.H{"error": gin.H{"code": "invalid_webhook_configuration", "message": message}})
		}
		if err != nil {
			reject("Invalid notification request body")
			return
		}
		var n struct {
			Webhook *struct {
				EndpointID string   `json:"endpoint_id"`
				Events     []string `json:"events"`
			} `json:"webhook"`
		}
		if common.Unmarshal(notification, &n) != nil || n.Webhook == nil {
			reject("notifications must contain a webhook object")
			return
		}
		if n.Webhook != nil {
			if legacy {
				reject("Do not combine notifications with legacy webhook or callback_url")
				return
			}
			asyncPath := strings.HasPrefix(modelName, "midjourney-") || strings.Contains(path, "video") || strings.Contains(path, "/async") || strings.Contains(path, "midjourney") || strings.HasPrefix(path, "/suno/")
			if !asyncPath || !service.TaskWebhookModelSupported(modelName) {
				reject("Webhook notifications require a supported asynchronous model and endpoint")
				return
			}
			if len(reference) > 128 || n.Webhook.EndpointID == "" {
				reject("endpoint_id is required; client_reference_id must be at most 128 bytes")
				return
			}
			for _, event := range n.Webhook.Events {
				if event != "task.completed" && event != "task.failed" && !(strings.HasPrefix(modelName, "midjourney-") && event == "batch.completed") {
					reject("Unsupported event type")
					return
				}
			}
			endpoint, e := model.WebhookEndpointForUser(n.Webhook.EndpointID, c.GetInt("id"))
			if e != nil || !endpoint.Enabled || !endpoint.Verified {
				reject("Endpoint is not verified, enabled, or owned by this account")
				return
			}
			c.Set(service.TaskWebhookContextKey, &model.TaskWebhookConfig{EndpointID: endpoint.ID, Events: n.Webhook.Events, Reference: reference})
		}
		replacement, e := common.CreateBodyStorage(cleaned)
		if e != nil {
			reject("Unable to prepare notification request")
			return
		}
		common.CleanupBodyStorage(c)
		c.Set(common.KeyBodyStorage, replacement)
		c.Request.Body = io.NopCloser(replacement)
		c.Request.ContentLength = int64(len(cleaned))
		c.Request.Header.Set("Content-Type", contentType)
		c.Next()
	}
}
