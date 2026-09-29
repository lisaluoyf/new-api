package claude

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/reasoning"
)

func isOpus55(model string) bool {
	return model == "claude-opus-5-5" || strings.HasPrefix(model, "claude-opus-5-5-")
}

// The OpenAI compatibility surface asks for visible summaries. Native Messages
// requests retain Anthropic's default display behavior and bypass this helper.
func configureOpus55Thinking(out *dto.ClaudeRequest, in dto.GeneralOpenAIRequest) error {
	effort := ""
	if base, suffix, ok := reasoning.TrimEffortSuffix(in.Model); ok {
		out.Model, effort = base, suffix
	} else if strings.HasSuffix(in.Model, "-thinking") {
		effort = "high"
	}
	out.Thinking = &dto.Thinking{Type: "adaptive", Display: "summarized"}
	if len(in.THINKING) > 0 {
		var thinking dto.Thinking
		if err := common.Unmarshal(in.THINKING, &thinking); err != nil {
			return fmt.Errorf("invalid thinking: %w", err)
		}
		if thinking.Type != "adaptive" || thinking.BudgetTokens != nil {
			return fmt.Errorf("claude-opus-5-5 requires thinking.type=adaptive without budget_tokens; use reasoning_effort")
		}
		if thinking.Display != "" {
			out.Thinking.Display = thinking.Display
		}
	}
	if len(in.Reasoning) > 0 {
		var r struct {
			Effort    string `json:"effort"`
			MaxTokens *int   `json:"max_tokens"`
			Enabled   *bool  `json:"enabled"`
			Exclude   *bool  `json:"exclude"`
		}
		if err := common.Unmarshal(in.Reasoning, &r); err != nil {
			return fmt.Errorf("invalid reasoning: %w", err)
		}
		if r.MaxTokens != nil || (r.Enabled != nil && !*r.Enabled) {
			return fmt.Errorf("claude-opus-5-5 does not support disabling thinking or reasoning.max_tokens; use reasoning_effort")
		}
		if r.Effort != "" {
			effort = r.Effort
		}
		if r.Exclude != nil && *r.Exclude {
			out.Thinking.Display = "omitted"
		}
	}
	if in.ReasoningEffort != "" {
		effort = in.ReasoningEffort
	}
	if effort != "" {
		switch effort {
		case "low", "medium", "high", "xhigh", "max":
		default:
			return fmt.Errorf("unsupported reasoning_effort for claude-opus-5-5: %q; use low, medium, high, xhigh or max", effort)
		}
		config, err := common.Marshal(map[string]string{"effort": effort})
		if err != nil {
			return err
		}
		out.OutputConfig = config
	}
	return nil
}
