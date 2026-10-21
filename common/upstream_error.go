package common

import (
	"net/http"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// PublicUpstreamError contains response diagnostics only, never request headers or
// credentials. Keep the same value in API responses, receipts and customer logs.
type PublicUpstreamError struct {
	redactionValues []string
	StatusCode      int    `json:"status_code,omitempty"`
	RequestID       string `json:"request_id,omitempty"`
	Body            any    `json:"body"`
	Truncated       bool   `json:"truncated,omitempty"`
}

const MaxPublicUpstreamErrorBytes = 48 * 1024

var upstreamIdentityLabel = regexp.MustCompile(`(?i)(?:provider|vendor|supplier|channel|deployment|upstream[_ -]?model)(?:[_ -]?(?:name|id))?["']?\s*[:=]\s*(?:"[^"\r\n]*"|'[^'\r\n]*'|[A-Za-z0-9_.@/-]+)`)
var upstreamChannelNumber = regexp.MustCompile(`(?i)(?:\b(?:channel|route)[ _-]*(?:id)?[ _:=#-]*[0-9]+\b|(?:渠道|路由)\s*(?:编号|ID)?[ _:=#-]*[0-9]+)`)
var upstreamCredential = regexp.MustCompile(`(?i)(?:authorization|proxy-authorization|x-api-key|api[_-]?key|key|access[_-]?key(?:[_-]?id)?|secret(?:[_-]?access[_-]?key)?|token|session|signature|password|cookie|credential)["']?\s*[:=]\s*(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;]+)`)
var upstreamBearer = regexp.MustCompile(`(?i)\b(?:Bearer|Basic)\s+[A-Za-z0-9._~+/=-]+`)
var upstreamKey = regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]+|AIza[A-Za-z0-9_-]+|eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)\b`)
var upstreamIP = regexp.MustCompile(`(?:\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b|\[[0-9a-fA-F]*:[0-9a-fA-F:]+\])`)
var upstreamEmail = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
var upstreamPrivatePath = regexp.MustCompile(`(?:/var/|/opt/|/home/|/root/|/app/)[^\s"']+`)
var upstreamURL = regexp.MustCompile(`(?i)(?:https?|wss?|s3|gs|asset)://[^\s<>"']+`)

func SanitizeUpstreamErrorText(text string, secrets ...string) string {
	text = redactUpstreamValues(text, secrets...)
	// Remove the complete auth scheme/value before stripping its field label.
	text = upstreamBearer.ReplaceAllString(text, "[redacted]")
	text = upstreamCredential.ReplaceAllString(text, "[redacted]")
	text = upstreamIdentityLabel.ReplaceAllString(text, "[redacted]")
	text = upstreamChannelNumber.ReplaceAllString(text, "[redacted]")
	text = upstreamKey.ReplaceAllString(text, "[redacted]")
	text = upstreamURL.ReplaceAllString(text, "[redacted]")
	text = upstreamBareDomain.ReplaceAllString(text, "[redacted]")
	text = upstreamIP.ReplaceAllString(text, "[redacted]")
	text = upstreamEmail.ReplaceAllString(text, "[redacted]")
	text = upstreamPrivatePath.ReplaceAllString(text, "[redacted]")
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, text)
}

func privateErrorField(key string) bool {
	if upstreamIdentityField(key) {
		return true
	}
	key = strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "").Replace(key))
	for _, word := range []string{"authorization", "apikey", "accesskey", "secret", "password", "cookie", "signature", "credential", "channel", "merchant", "account"} {
		if strings.Contains(key, word) {
			return true
		}
	}
	switch key {
	case "key", "token", "accesstoken", "refreshtoken", "sessiontoken", "idtoken", "session", "headers", "request", "requestbody", "requestdata", "prompt", "messages", "input", "output", "outputs", "images", "videos", "image", "video", "audio", "b64json", "imagedata", "filedata", "privatekey", "stack", "stacktrace", "traceback":
		return true
	}
	return false
}

func NewPublicUpstreamError(status int, body []byte, headers http.Header, secrets ...string) *PublicUpstreamError {
	result := &PublicUpstreamError{StatusCode: status}
	// Response identifiers are safe opaque values; never forward arbitrary headers.
	for _, key := range []string{"X-Request-Id", "Request-Id", "X-Tt-Logid", "X-Amzn-Requestid"} {
		if id := headers.Get(key); id != "" {
			result.RequestID = SanitizeUpstreamErrorText(id, secrets...)
			if len(result.RequestID) > 256 {
				result.RequestID = "[redacted]"
			}
			break
		}
	}
	var value any
	if len(body) > 2<<20 {
		value = "Upstream error response exceeds diagnostic limit"
		result.Truncated = true
	} else if Unmarshal(body, &value) != nil {
		value = string(body)
	}
	secrets = append(secrets, collectUpstreamIdentities(value, 0)...)
	result.redactionValues = append([]string(nil), secrets...)
	budget := MaxPublicUpstreamErrorBytes / 2 // JSON escaping and envelope overhead.
	var clean func(any, int) any
	clean = func(v any, depth int) any {
		if budget <= 0 || depth > 32 {
			result.Truncated = true
			return "[truncated]"
		}
		switch v := v.(type) {
		case map[string]any:
			out := map[string]any{}
			for k, child := range v {
				if budget <= 0 {
					result.Truncated = true
					break
				}
				if privateErrorField(k) {
					out[k] = "[redacted]"
					budget -= len(k) + 12
					continue
				}
				safeKey := SanitizeUpstreamErrorText(k, secrets...)
				if len(safeKey) > 256 {
					safeKey = "[redacted]"
				}
				encodedKey, _ := Marshal(safeKey)
				budget -= len(encodedKey) + 8
				switch strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(k)) {
				case "code", "errorcode", "type", "errortype":
					if code, ok := child.(string); ok {
						out[safeKey] = PublicDiagnosticCode(code, secrets...)
						continue
					}
				}
				out[safeKey] = clean(child, depth+1)
			}
			return out
		case []any:
			out := []any{}
			for _, child := range v {
				if budget <= 0 {
					result.Truncated = true
					break
				}
				out = append(out, clean(child, depth+1))
				budget -= 2
			}
			return out
		case string:
			text := v
			if index := strings.Index(v, "{"); index >= 0 {
				var nested any
				if Unmarshal([]byte(v[index:]), &nested) == nil {
					encoded, _ := Marshal(clean(nested, depth+1))
					text = v[:index] + string(encoded)
				}
			}
			text = SanitizeUpstreamErrorText(text, secrets...)
			// Bound by encoded bytes, including escaping, so MySQL TEXT remains safe.
			encoded, _ := Marshal(text)
			if len(encoded) > budget {
				result.Truncated = true
				for len(text) > 0 && len(encoded) > budget {
					n := len(text) / 2
					for n > 0 && !utf8.RuneStart(text[n]) {
						n--
					}
					text = text[:n]
					encoded, _ = Marshal(text)
				}
			}
			budget -= len(encoded)
			return text
		default:
			budget -= 32
			return v
		}
	}
	result.Body = clean(value, 0)
	result.redactionValues = append([]string(nil), secrets...)
	id := SanitizeUpstreamErrorText(result.RequestID, secrets...)
	if id != result.RequestID || strings.Contains(id, "[redacted]") {
		result.RequestID = ""
	}
	return result
}

func PublicUpstreamErrorFromResponse(resp *http.Response, body []byte, secrets ...string) *PublicUpstreamError {
	if resp.Request != nil && resp.Request.URL != nil {
		secrets = append(secrets, UpstreamIdentityValues("", resp.Request.URL.String(), "", "")...)
	}
	if resp.Request != nil {
		for _, key := range []string{"Authorization", "X-Api-Key", "Api-Key", "X-Goog-Api-Key"} {
			if value := resp.Request.Header.Get(key); value != "" {
				secrets = append(secrets, value, strings.TrimPrefix(value, "Bearer "))
			}
		}
	}
	return NewPublicUpstreamError(resp.StatusCode, body, resp.Header, secrets...)
}

// FailureDiagnostics extracts error fields without depending on a provider or
// channel ID. Successful task payloads are never returned to the customer here.
func FailureDiagnostics(body []byte) any {
	var value any
	if Unmarshal(body, &value) != nil {
		return nil
	}
	var visit func(any, int) any
	visit = func(value any, depth int) any {
		if depth > 32 {
			return nil
		}
		switch v := value.(type) {
		case map[string]any:
			result := map[string]any{}
			for key, child := range v {
				name := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
				switch name {
				case "error", "errors", "failreason", "failurereason", "errormessage", "responsemetadata":
					if child != nil && child != "" {
						result[key] = child
					}
				case "code", "message", "reason", "detail", "details", "type", "param", "requestid", "traceid":
					if !privateErrorField(key) {
						result[key] = child
					}
				default:
					if nested := visit(child, depth+1); nested != nil {
						result[key] = nested
					}
				}
			}
			if len(result) > 0 {
				return result
			}
		case []any:
			out := []any{}
			for _, child := range v {
				if nested := visit(child, depth+1); nested != nil {
					out = append(out, nested)
				}
			}
			if len(out) > 0 {
				return out
			}
		}
		return nil
	}
	return visit(value, 0)
}

// Reapply the diagnostic redaction when reading storage, retaining its public
// object shape and any explicit truncation flag from the original capture.
func RedactStoredUpstreamError(value any, values ...string) any {
	raw, err := Marshal(value)
	if err != nil {
		return nil
	}
	safe := NewPublicUpstreamError(0, raw, nil, values...)
	if safe.Truncated {
		if object, ok := safe.Body.(map[string]any); ok {
			object["truncated"] = true
		}
	}
	return safe.Body
}
