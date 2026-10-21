package common

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

var upstreamBareDomain = regexp.MustCompile(`(?i)\b(?:[a-z0-9][a-z0-9-]*\.)+(?:com|net|org|io|ai|cn|cloud|dev|app|xyz|cc|me|top|online|site|biz|tech|ltd|info)(?:\.[a-z]{2})?\b`)
var publicDiagnosticIdentifier = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.:-]{0,255}$`)

// Routing aliases are supplied dynamically, so a newly configured channel does
// not need to be added to a vendor-name blacklist. Keep public model vocabulary.
func UpstreamIdentityValues(name, baseURL, privateModel, publicModel string) []string {
	result := []string{}
	if name != "" {
		result = append(result, name)
	}
	if privateModel != "" && privateModel != publicModel {
		result = append(result, privateModel)
	}
	for _, part := range strings.FieldsFunc(name, func(r rune) bool {
		return unicode.IsSpace(r) || r == '_' || r == '-' || r == '/' || r == '|' || r == '(' || r == ')'
	}) {
		if len(part) < 2 || strings.Contains(strings.ToLower(publicModel), strings.ToLower(part)) {
			continue
		}
		switch strings.ToLower(part) {
		case "ai", "llc", "inc", "ltd", "labs", "cloud", "clouds", "gateway", "api", "channel", "video", "image", "audio", "text", "fast", "mini", "pro", "default", "official", "seedance", "渠道", "官方":
			continue
		}
		result = append(result, part)
	}
	if parsed, err := url.Parse(baseURL); err == nil && parsed.Hostname() != "" {
		host := parsed.Hostname()
		result = append(result, host)
		labels := strings.Split(host, ".")
		if len(labels) >= 2 {
			brand := labels[len(labels)-2]
			if brand == "com" || brand == "co" {
				if len(labels) >= 3 {
					brand = labels[len(labels)-3]
				}
			}
			if len(brand) >= 3 {
				result = append(result, brand)
			}
		}
	}
	return result
}

func upstreamIdentityField(key string) bool {
	key = strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "").Replace(key))
	if !strings.Contains(key, "error") && !strings.Contains(key, "cause") {
		for _, word := range []string{"provider", "vendor", "supplier"} {
			if strings.Contains(key, word) {
				return true
			}
		}
	}
	switch key {
	case "platform", "provider", "providername", "providerid", "vendor", "vendorname", "vendorid", "supplier", "suppliername", "supplierid", "channel", "channelname", "channelid", "route", "routename", "routeid", "routing", "endpoint", "hostname", "host", "baseurl", "apibase", "deployment", "deploymentname", "deploymentid", "model", "modelid", "modelname", "internalmodel", "internalmodelid", "internalmodelname", "upstreammodelid", "upstreammodel", "upstreammodelname", "upstreamname", "upstreamid", "server", "servername", "organization", "organizationname":
		return true
	}
	return false
}

func collectUpstreamIdentities(value any, depth int) []string {
	if depth > 32 {
		return nil
	}
	result := []string{}
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if upstreamIdentityField(key) {
				if name, ok := child.(string); ok && name != "[redacted]" {
					result = append(result, name)
					normalized := strings.ToLower(key)
					if strings.Contains(normalized, "provider") || strings.Contains(normalized, "vendor") || strings.Contains(normalized, "supplier") {
						result = append(result, UpstreamIdentityValues(name, "", "", "")...)
					}
				}
				if object, ok := child.(map[string]any); ok {
					for _, field := range []string{"name", "Name", "display_name", "hostname", "host", "base_url"} {
						if name, ok := object[field].(string); ok && name != "[redacted]" {
							result = append(result, UpstreamIdentityValues(name, "", "", "")...)
						}
					}
				}
			}
			result = append(result, collectUpstreamIdentities(child, depth+1)...)
		}
	case string:
		if index := strings.Index(value, "{"); index >= 0 {
			var nested any
			if Unmarshal([]byte(value[index:]), &nested) == nil {
				result = append(result, collectUpstreamIdentities(nested, depth+1)...)
			}
		}
	case []any:
		for _, child := range value {
			result = append(result, collectUpstreamIdentities(child, depth+1)...)
		}
	}
	return result
}

// Provider-specific namespaces are private. Functional error families and the
// actionable leaf code remain available without the vendor prefix.
func PublicDiagnosticCode(code string, values ...string) string {
	code = strings.TrimSpace(code)
	if !publicDiagnosticIdentifier.MatchString(code) {
		return "upstream_error"
	}
	code = redactUpstreamValues(code, values...)
	parts := strings.Split(code, ".")
	clean := []string{}
	for _, part := range parts {
		part = strings.Trim(strings.ReplaceAll(part, "[redacted]", ""), "._:-")
		if part != "" {
			clean = append(clean, part)
		}
	}
	if len(clean) == 0 {
		return "upstream_error"
	}
	if len(clean) > 1 && !neutralErrorFamily(clean[0]) {
		clean = clean[len(clean)-1:]
	}
	result := strings.Trim(strings.Join(clean, "."), "._:-")
	if !publicDiagnosticIdentifier.MatchString(result) {
		return "upstream_error"
	}
	return result
}
func neutralErrorFamily(code string) bool {
	switch code {
	case "violation_fee", "InvalidParameter", "InvalidParameterValue", "MissingParameter", "MissingParameterValue", "UnsupportedParameter", "ResourceNotFound", "ResourceUnavailable", "LimitExceeded", "QuotaExceeded", "Throttling", "TooManyRequests", "InternalError", "ServiceUnavailable", "TaskExpired":
		return true
	}
	for _, prefix := range []string{"InputText", "InputImage", "InputVideo", "InputAudio", "OutputText", "OutputImage", "OutputVideo", "OutputAudio"} {
		if code == prefix+"SensitiveContentDetected" || code == prefix+"RiskDetection" {
			return true
		}
	}
	return false
}
func redactUpstreamValues(text string, values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || value == "[redacted]" {
			continue
		}
		if len(value) <= 3 {
			pattern := regexp.MustCompile(`(?i)(^|[^a-z0-9])` + regexp.QuoteMeta(value) + `($|[^a-z0-9])`)
			text = pattern.ReplaceAllString(text, "${1}[redacted]${2}")
			text = pattern.ReplaceAllString(text, "${1}[redacted]${2}")
		} else {
			text = regexp.MustCompile("(?i)"+regexp.QuoteMeta(value)).ReplaceAllString(text, "[redacted]")
		}
	}
	return text
}

// Values never serialize to JSON. They are available only while constructing
// public views; original errors still drive retries, health and billing.
func (e *PublicUpstreamError) RedactionValues() []string {
	if e == nil {
		return nil
	}
	return append([]string(nil), e.redactionValues...)
}
func RedactPublicUpstreamError(detail *PublicUpstreamError, values ...string) *PublicUpstreamError {
	if detail == nil {
		return nil
	}
	values = append(values, detail.redactionValues...)
	raw, _ := Marshal(detail.Body)
	result := NewPublicUpstreamError(detail.StatusCode, raw, nil, values...)
	result.Truncated = result.Truncated || detail.Truncated
	id := SanitizeUpstreamErrorText(detail.RequestID, values...)
	if id == detail.RequestID {
		result.RequestID = id
	}
	return result
}

// This policy is limited to diagnostic fields so customer media links and
// unrelated billing values in log metadata retain their existing semantics.
func RedactLogErrorIdentityFields(value any, values ...string) {
	var visit func(any, bool)
	visit = func(value any, publicRequest bool) {
		switch value := value.(type) {
		case map[string]any:
			for key, child := range value {
				normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
				if key == "upstream_error" {
					value[key] = RedactStoredUpstreamError(child, values...)
					continue
				}
				if upstreamIdentityField(key) && !(publicRequest && (normalized == "model" || normalized == "modelid" || normalized == "modelname")) {
					delete(value, key)
					continue
				}
				if text, ok := child.(string); ok {
					switch normalized {
					case "errorcode", "errortype", "taskfailcode":
						value[key] = PublicDiagnosticCode(text, values...)
					case "errormessage", "taskfailreason", "failreason", "reason", "message":
						value[key] = SanitizeUpstreamErrorText(text, values...)
					}
				}
				visit(child, publicRequest || normalized == "requestdata" || normalized == "requestbody" || normalized == "request")
			}
		case []any:
			for _, child := range value {
				visit(child, publicRequest)
			}
		}
	}
	visit(value, false)
}

func UpstreamIdentityRedactions(value any, values ...string) []string {
	return append(append([]string(nil), values...), collectUpstreamIdentities(value, 0)...)
}

func publicErrorSchemaKey(key string) bool {
	key = strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
	switch key {
	case "error", "errors", "errorcode", "errortype", "code", "type", "message", "detail", "details", "data", "response", "responsemetadata", "upstream", "upstreamerror", "status", "statuscode", "requestid", "traceid", "metadata", "param", "body", "truncated", "cause", "failure", "reason", "failreason", "provider", "vendor", "supplier", "model", "modelname", "modelid", "channelname", "channelid", "deployment":
		return true
	}
	return false
}
