package dto

import (
	"encoding/json"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"strings"
)

// Raw question objects preserve provider extensions and explicit zero/false values.
type TypeSafeRequest struct {
	Model     string                     `json:"model"`
	State     json.RawMessage            `json:"state"`
	Questions map[string]json.RawMessage `json:"questions"`
	Stream    *bool                      `json:"stream,omitempty"`
}

func (r *TypeSafeRequest) IsStream(*gin.Context) bool { return false }
func (r *TypeSafeRequest) SetModelName(name string)   { r.Model = name }
func (r *TypeSafeRequest) GetTokenCountMeta() *types.TokenCountMeta {
	questions, _ := common.Marshal(r.Questions)
	return &types.TokenCountMeta{TokenType: types.TokenTypeTokenizer, CombineText: string(r.State) + "\n" + string(questions)}
}

func (r *TypeSafeRequest) Validate() error {
	if strings.TrimSpace(r.Model) == "" {
		return fmt.Errorf("model is required")
	}
	if r.Stream != nil && *r.Stream {
		return fmt.Errorf("TypeSafe does not support streaming")
	}
	if !typeSafeStructuredValue(r.State) {
		return fmt.Errorf("state must be a string, object, or array")
	}
	if len(r.Questions) == 0 {
		return fmt.Errorf("questions must not be empty")
	}
	for id, raw := range r.Questions {
		var q struct {
			Type         string          `json:"type"`
			Instructions json.RawMessage `json:"instructions"`
			Criteria     json.RawMessage `json:"criteria"`
		}
		if err := common.Unmarshal(raw, &q); err != nil {
			return fmt.Errorf("question %q must be an object", id)
		}
		if !typeSafeStructuredValue(q.Instructions) {
			return fmt.Errorf("question %q requires instructions", id)
		}
		switch q.Type {
		case "noul":
		case "choice":
			var options map[string]json.RawMessage
			if common.Unmarshal(q.Criteria, &options) != nil || len(options) == 0 || len(options) > 255 {
				return fmt.Errorf("question %q requires 1–255 choice criteria", id)
			}
		case "score":
			var levels []json.RawMessage
			if common.Unmarshal(q.Criteria, &levels) != nil || len(levels) < 2 || len(levels) > 10 {
				return fmt.Errorf("question %q requires 2–10 score criteria", id)
			}
		default:
			return fmt.Errorf("question %q has unsupported type %q", id, q.Type)
		}
	}
	return nil
}

func typeSafeStructuredValue(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return len(s) > 0 && (s[0] == '"' || s[0] == '{' || s[0] == '[')
}

type TypeSafeModelsResponse struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

func (r TypeSafeModelsResponse) ModelNames() []string {
	names := make([]string, 0, len(r.Models))
	for _, model := range r.Models {
		if model.Name != "" {
			names = append(names, model.Name)
		}
	}
	return names
}

func ParseTypeSafeResponse(data []byte, request Request) (*Usage, error) {
	var result struct {
		Model   string                     `json:"model"`
		Answers map[string]json.RawMessage `json:"answers"`
		Usage   *struct {
			Input  *int `json:"input_tokens"`
			Output *int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := common.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("invalid TypeSafe JSON response")
	}
	if result.Model == "" || len(result.Answers) == 0 || result.Usage == nil || result.Usage.Input == nil || result.Usage.Output == nil {
		return nil, fmt.Errorf("TypeSafe response missing model, answers, or usage")
	}
	input, output := *result.Usage.Input, *result.Usage.Output
	if input < 0 || output < 0 || input > 1000000000 || output > 1000000000 {
		return nil, fmt.Errorf("invalid TypeSafe token usage")
	}
	if req, ok := request.(*TypeSafeRequest); ok {
		for id, question := range req.Questions {
			if len(result.Answers[id]) == 0 || string(result.Answers[id]) == "null" {
				return nil, fmt.Errorf("TypeSafe response missing answer %q", id)
			}
			var q struct {
				Type string `json:"type"`
			}
			var answer struct {
				Type   string   `json:"type"`
				Noul   *float64 `json:"noul"`
				Choice *string  `json:"choice"`
				Score  *float64 `json:"score"`
			}
			if common.Unmarshal(question, &q) != nil || common.Unmarshal(result.Answers[id], &answer) != nil || answer.Type != q.Type {
				return nil, fmt.Errorf("TypeSafe answer %q has invalid type", id)
			}
			if (q.Type == "noul" && (answer.Noul == nil || *answer.Noul < 0 || *answer.Noul > 1)) ||
				(q.Type == "choice" && answer.Choice == nil) || (q.Type == "score" && answer.Score == nil) {
				return nil, fmt.Errorf("TypeSafe answer %q is missing a valid result", id)
			}
		}
	}
	return &Usage{PromptTokens: input, CompletionTokens: output, TotalTokens: input + output}, nil
}
