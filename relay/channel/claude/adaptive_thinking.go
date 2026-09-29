package claude

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/reasoning"
)

type adaptiveThinkingProfile struct {
	defaultOn      bool
	offType        string
	offThroughHigh bool
}

var adaptiveThinkingProfiles = map[string]adaptiveThinkingProfile{
	"adaptive_opt_in":          {offType: "disabled"},
	"adaptive_default_on":      {defaultOn: true, offType: "disabled"},
	"adaptive_default_on_high": {defaultOn: true, offType: "disabled", offThroughHigh: true},
	"adaptive_required":        {defaultOn: true},
	"adaptive_between_tools":   {defaultOn: true, offType: "between_tools", offThroughHigh: true},
}

// Shared by OpenAI-to-Anthropic conversions, including streaming and cloud
// adaptors that use RequestOpenAI2ClaudeMessage. Native Messages is unchanged.
// Never translate a hard token budget into an arbitrary effort level.
func configureAdaptiveThinking(out *dto.ClaudeRequest, in dto.GeneralOpenAIRequest, profileName string) error {
	profile, ok := adaptiveThinkingProfiles[profileName]
	if !ok {
		return fmt.Errorf("unknown Claude thinking profile %q for %s", profileName, in.Model)
	}
	// These profiles describe models that reject custom sampling parameters.
	// OpenAI clients commonly send temperature=0 by default. As with the older
	// Opus 4.7 compatibility path, omit sampling controls on this surface.
	out.Temperature, out.TopP, out.TopK = nil, nil, nil
	effort := ""
	if base, suffix, ok := reasoning.TrimEffortSuffix(in.Model); ok {
		out.Model, effort = base, suffix
	} else if model_setting.GetClaudeSettings().ThinkingAdapterEnabled && strings.HasSuffix(in.Model, "-thinking") {
		effort = "high"
		if !model_setting.ShouldPreserveThinkingSuffix(in.Model) {
			out.Model = strings.TrimSuffix(in.Model, "-thinking")
		}
	}
	var explicitThinking map[string]any
	thinking := &dto.Thinking{Type: "adaptive", Display: "summarized"}
	if len(in.THINKING) > 0 {
		if err := common.Unmarshal(in.THINKING, &explicitThinking); err != nil || explicitThinking == nil {
			return fmt.Errorf("invalid thinking for %s: expected an object", in.Model)
		}
		var explicit dto.Thinking
		if err := common.Unmarshal(in.THINKING, &explicit); err != nil {
			return fmt.Errorf("invalid thinking: %w", err)
		}
		if _, exists := explicitThinking["budget_tokens"]; exists {
			return fmt.Errorf("%s does not support thinking.budget_tokens; use reasoning_effort", in.Model)
		}
		if explicit.Type != "adaptive" && (profile.offType == "" || explicit.Type != profile.offType) {
			return fmt.Errorf("%s does not support thinking.type=%q; use adaptive%s", in.Model, explicit.Type, offTypeHint(profile))
		}
		thinking = &explicit
		if thinking.Type == "adaptive" && thinking.Display == "" {
			thinking.Display = "summarized"
		}
		if thinking.Type != "adaptive" && len(explicitThinking) != 1 {
			return fmt.Errorf("%s thinking.type=%s accepts no additional fields", in.Model, thinking.Type)
		}
	}
	var r struct {
		Effort    string `json:"effort"`
		MaxTokens *int   `json:"max_tokens"`
		Enabled   *bool  `json:"enabled"`
		Exclude   *bool  `json:"exclude"`
	}
	if len(in.Reasoning) > 0 {
		if err := common.Unmarshal(in.Reasoning, &r); err != nil {
			return fmt.Errorf("invalid reasoning: %w", err)
		}
		if r.MaxTokens != nil {
			return fmt.Errorf("%s does not support reasoning.max_tokens; use reasoning_effort", in.Model)
		}
		if r.Effort != "" {
			effort = r.Effort
		}
	}
	if in.ReasoningEffort != "" {
		effort = in.ReasoningEffort
	}
	// "none" really means no thinking. Sonnet 5.5 between_tools still permits
	// inter-tool thinking/updates, so require clients to request it explicitly.
	requestedOff := effort == "none" || (r.Enabled != nil && !*r.Enabled)
	if requestedOff {
		if profile.offType != "disabled" {
			return fmt.Errorf("%s does not support disabling thinking; use reasoning_effort%s", in.Model, offTypeHint(profile))
		}
		if explicitThinking != nil && thinking.Type != "disabled" {
			return fmt.Errorf("conflicting thinking and reasoning settings for %s", in.Model)
		}
		thinking = &dto.Thinking{Type: "disabled"}
		if effort == "none" {
			effort = ""
		}
	}
	if r.Enabled != nil && *r.Enabled && thinking.Type != "adaptive" {
		return fmt.Errorf("conflicting thinking and reasoning.enabled for %s", in.Model)
	}
	if effort != "" {
		switch effort {
		case "low", "medium", "high", "xhigh", "max":
		default:
			return fmt.Errorf("unsupported reasoning_effort for %s: %q; use low, medium, high, xhigh or max", in.Model, effort)
		}
		if thinking.Type != "adaptive" && profile.offThroughHigh && (effort == "xhigh" || effort == "max") {
			return fmt.Errorf("%s thinking.type=%s requires effort low, medium or high", in.Model, thinking.Type)
		}
		config, err := common.Marshal(map[string]string{"effort": effort})
		if err != nil {
			return err
		}
		out.OutputConfig = config
	}
	if r.Exclude != nil && *r.Exclude {
		if thinking.Type == "between_tools" {
			return fmt.Errorf("%s between_tools cannot omit progress updates; use adaptive with reasoning.exclude", in.Model)
		}
		if thinking.Type == "adaptive" {
			thinking.Display = "omitted"
		}
	}
	// Preserve default-off behavior of older models unless thinking was requested.
	if profile.defaultOn || effort != "" || explicitThinking != nil || r.Enabled != nil || requestedOff {
		out.Thinking = thinking
	}
	return nil
}

func offTypeHint(profile adaptiveThinkingProfile) string {
	if profile.offType == "" {
		return ""
	}
	return " or thinking.type=" + profile.offType
}
