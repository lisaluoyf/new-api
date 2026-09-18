package openai

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

func TestNormalizeGLM53Reasoning(t *testing.T) {
	for _, model := range []string{"glm-5.3", "glm-5.3-flash", "zai-org/GLM-5.3", "zai-org/GLM-5.3-Flash"} {
		for _, base := range []string{"https://api.apikey.fan", "https://api.gmi-serving.com"} {
			for _, tc := range []struct{ name, input, want string }{
				{"missing stays missing", `{}`, `{}`},
				{"disabled becomes low", `{"thinking":{"type":"disabled"},"enable_thinking":false}`, `{"reasoning_effort":"low"}`},
				{"none becomes low", `{"reasoning_effort":"none"}`, `{"reasoning_effort":"low"}`},
				{"invalid effort becomes low", `{"reasoning_effort":"medium"}`, `{"reasoning_effort":"low"}`},
				{"false becomes low", `{"enable_thinking":false}`, `{"reasoning_effort":"low"}`},
				{"ollama false becomes low", `{"think":false}`, `{"reasoning_effort":"low"}`},
				{"enabled needs no switch", `{"thinking":{"type":"enabled"}}`, `{}`},
				{"legal low preserved", `{"reasoning_effort":"low"}`, `{"reasoning_effort":"low"}`},
				{"legal high wins", `{"reasoning_effort":"high","thinking":{"type":"disabled"},"enable_thinking":false}`, `{"reasoning_effort":"high"}`},
				{"legal max preserved", `{"reasoning_effort":"max"}`, `{"reasoning_effort":"max"}`},
				{"alternate legal wins", `{"reasoning":{"effort":"max","enabled":false},"thinking":{"type":"disabled","effort":"high"}}`, `{"reasoning_effort":"max"}`},
				{"template false cleaned", `{"chat_template_kwargs":{"enable_thinking":false,"unrelated":7}}`, `{"reasoning_effort":"low","chat_template_kwargs":{"unrelated":7}}`},
				{"nested extra disabled", `{"extra_body":{"thinking":{"type":"disabled"},"unrelated":7}}`, `{"reasoning_effort":"low","extra_body":{"unrelated":7}}`},
				{"nested legal wins", `{"thinking":{"type":"disabled"},"extra_body":{"reasoning_effort":"high","other":7}}`, `{"reasoning_effort":"high","extra_body":{"other":7}}`},
			} {
				t.Run(model+"/"+base+"/"+tc.name, func(t *testing.T) {
					var request, want dto.GeneralOpenAIRequest
					if err := common.Unmarshal([]byte(tc.input), &request); err != nil {
						t.Fatal(err)
					}
					if err := common.Unmarshal([]byte(tc.want), &want); err != nil {
						t.Fatal(err)
					}
					normalizeGLM53Reasoning(model, base, &request)
					gotJSON, err := common.Marshal(request)
					if err != nil {
						t.Fatal(err)
					}
					wantJSON, _ := common.Marshal(want)
					assertJSONEqual(t, gotJSON, wantJSON)
					if result := normalizeGLM53Reasoning(model, base, &request); result.Changed {
						t.Fatalf("normalization not idempotent: %+v", result)
					}
				})
			}
		}
	}
}

func TestNormalizeGLM53ReasoningPreservesOtherModelsAndSampling(t *testing.T) {
	for _, model := range []string{"glm-5.2", "glm-5.3-extra", "glm-5.3-flash-preview", "gpt-5.4", "claude-opus-4-6"} {
		request := dto.GeneralOpenAIRequest{THINKING: json.RawMessage(`{"type":"disabled"}`), ReasoningEffort: "none"}
		before, _ := common.Marshal(request)
		if result := normalizeGLM53Reasoning(model, "https://api.gmi-serving.com", &request); result.Changed {
			t.Fatalf("unrelated model %s changed", model)
		}
		after, _ := common.Marshal(request)
		assertJSONEqual(t, after, before)
	}
	var request dto.GeneralOpenAIRequest
	if err := common.Unmarshal([]byte(`{"temperature":0,"top_p":0.45,"top_k":45,"frequency_penalty":1.45,"messages":[{"role":"user","content":"hello"}],"stream":true}`), &request); err != nil {
		t.Fatal(err)
	}
	before, _ := common.Marshal(request)
	normalizeGLM53Reasoning("glm-5.3", "https://api.apikey.fan", &request)
	after, _ := common.Marshal(request)
	assertJSONEqual(t, after, before)
	if result := normalizeGLM53Reasoning("glm-5.3", "", nil); result.Changed {
		t.Fatal("nil request changed")
	}
}

func TestNormalizeGLM53ReasoningPreservesThinkingHistoryOutsideGMI(t *testing.T) {
	for _, base := range []string{"https://api.apikey.fan", "https://api.bigmodel.cn", "https://api.gmi-serving.com.evil.invalid"} {
		request := dto.GeneralOpenAIRequest{THINKING: json.RawMessage(`{"type":"disabled","clear_thinking":false}`)}
		normalizeGLM53Reasoning("glm-5.3", base, &request)
		if request.ReasoningEffort != "low" {
			t.Fatal("disabled did not become low")
		}
		assertJSONEqual(t, request.THINKING, []byte(`{"type":"enabled","clear_thinking":false}`))
	}
	request := dto.GeneralOpenAIRequest{THINKING: json.RawMessage(`{"type":"enabled","clear_thinking":false}`)}
	normalizeGLM53Reasoning("glm-5.3", "https://API.GMI-SERVING.COM:443/v1", &request)
	if len(request.THINKING) != 0 || request.ReasoningEffort != "" {
		t.Fatalf("GMI thinking must be omitted without changing default effort: %+v", request)
	}
	request = dto.GeneralOpenAIRequest{ExtraBody: json.RawMessage(`{"thinking":{"type":"disabled","clear_thinking":false},"other":7}`)}
	normalizeGLM53Reasoning("glm-5.3", "https://api.apikey.fan", &request)
	assertJSONEqual(t, request.THINKING, []byte(`{"type":"enabled","clear_thinking":false}`))
	assertJSONEqual(t, request.ExtraBody, []byte(`{"other":7}`))
}

func TestGLM53AdaptorNormalizesMappedRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, upstream := range []string{"glm-5.3", "glm-5.3-flash", "zai-org/GLM-5.3-Flash", "private-deployment"} {
		t.Run(upstream, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			info := &relaycommon.RelayInfo{OriginModelName: "glm-5.3-flash"}
			info.ChannelMeta = &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, ChannelBaseUrl: "https://api.gmi-serving.com", UpstreamModelName: upstream}
			request := &dto.GeneralOpenAIRequest{Model: upstream, THINKING: json.RawMessage(`{"type":"disabled"}`), EnableThinking: json.RawMessage(`false`)}
			converted, err := (&Adaptor{}).ConvertOpenAIRequest(c, info, request)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := common.Marshal(converted)
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]any
			if err = common.Unmarshal(encoded, &body); err != nil {
				t.Fatal(err)
			}
			if body["model"] != upstream || body["reasoning_effort"] != "low" {
				t.Fatalf("bad upstream payload: %s", encoded)
			}
			if _, ok := body["thinking"]; ok {
				t.Fatalf("thinking survived: %s", encoded)
			}
			if _, ok := body["enable_thinking"]; ok {
				t.Fatalf("enable_thinking survived: %s", encoded)
			}
		})
	}
}

func assertJSONEqual(t *testing.T, got, want []byte) {
	t.Helper()
	var a, b any
	if err := common.Unmarshal(got, &a); err != nil {
		t.Fatal(err)
	}
	if err := common.Unmarshal(want, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("JSON = %s, want %s", got, want)
	}
}
