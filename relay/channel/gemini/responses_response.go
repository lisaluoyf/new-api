package gemini

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type geminiResponsesBlock struct {
	item map[string]any
	text strings.Builder
}
type geminiResponsesState struct {
	response        map[string]any
	blocks          []*geminiResponsesBlock
	indices         map[string]int
	sequence        int
	started, usable bool
	finish          string
	grounding       json.RawMessage
	annotations     []any
}

func newGeminiResponsesState(info *relaycommon.RelayInfo) *geminiResponsesState {
	r := map[string]any{"id": "resp_" + common.GetUUID(), "object": "response", "created_at": common.GetTimestamp(), "model": info.OriginModelName, "status": "in_progress", "output": []any{}, "error": nil, "incomplete_details": nil, "usage": nil, "store": false, "background": false, "parallel_tool_calls": true, "metadata": map[string]any{}, "tools": []any{}, "tool_choice": "auto", "previous_response_id": nil}
	if req, ok := info.Request.(*dto.OpenAIResponsesRequest); ok {
		for k, v := range map[string]json.RawMessage{"instructions": req.Instructions, "tools": req.Tools, "tool_choice": req.ToolChoice, "metadata": req.Metadata, "text": req.Text} {
			if responsesPresent(v) {
				r[k] = v
			}
		}

		if req.MaxOutputTokens != nil {
			r["max_output_tokens"] = *req.MaxOutputTokens
		}
		if req.Reasoning != nil {
			r["reasoning"] = req.Reasoning
		}
		if req.Temperature != nil {
			r["temperature"] = *req.Temperature
		}
		if req.TopP != nil {
			r["top_p"] = *req.TopP
		}
	}
	return &geminiResponsesState{response: r, indices: map[string]int{}, annotations: []any{}}
}
func (s *geminiResponsesState) emit(c *gin.Context, typ string, fields map[string]any) {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["type"] = typ
	fields["sequence_number"] = s.sequence
	s.sequence++
	data, err := common.Marshal(fields)
	if err != nil {
		return
	}
	c.Render(-1, common.CustomEvent{Data: fmt.Sprintf("event: %s\n", typ)})
	c.Render(-1, common.CustomEvent{Data: "data: " + string(data)})
	_ = helper.FlushWriter(c)
}
func (s *geminiResponsesState) fields(i int) map[string]any {
	return map[string]any{"item_id": s.blocks[i].item["id"], "output_index": i}
}
func (s *geminiResponsesState) addBlock(c *gin.Context, item map[string]any, stream bool) int {
	i := len(s.blocks)
	item["id"] = fmt.Sprintf("%s_%d", s.response["id"], i)
	item["status"] = "in_progress"
	s.blocks = append(s.blocks, &geminiResponsesBlock{item: item})
	if stream {
		f := s.fields(i)
		f["item"] = item
		s.emit(c, "response.output_item.added", f)
	}
	return i
}
func (s *geminiResponsesState) addText(c *gin.Context, kind, text string, stream bool) {
	if text == "" {
		return
	}
	i, ok := s.indices[kind]
	if !ok {
		item := map[string]any{"type": "message", "role": "assistant", "content": []any{}}
		event := "response.content_part.added"
		part := map[string]any{"type": "output_text", "text": "", "annotations": []any{}}
		if kind == "reasoning" {
			item = map[string]any{"type": "reasoning", "summary": []any{}}
			event = "response.reasoning_summary_part.added"
			part = map[string]any{"type": "summary_text", "text": ""}
		}
		i = s.addBlock(c, item, stream)
		s.indices[kind] = i
		if stream {
			f := s.fields(i)
			if kind == "reasoning" {
				f["summary_index"] = 0
			} else {
				f["content_index"] = 0
			}
			f["part"] = part
			s.emit(c, event, f)
		}
	}
	s.blocks[i].text.WriteString(text)
	if kind != "reasoning" {
		s.usable = true
	}
	if stream {
		f := s.fields(i)
		f["delta"] = text
		if kind == "reasoning" {
			f["summary_index"] = 0
			s.emit(c, "response.reasoning_summary_text.delta", f)
		} else {
			f["content_index"] = 0
			s.emit(c, "response.output_text.delta", f)
		}
	}
}
func (s *geminiResponsesState) accept(c *gin.Context, r *dto.GeminiChatResponse, stream bool) error {
	if len(r.Candidates) == 0 && r.PromptFeedback != nil && r.PromptFeedback.BlockReason != nil {
		return fmt.Errorf("Gemini blocked request: %s", *r.PromptFeedback.BlockReason)
	}
	if !s.started && len(r.Candidates) > 0 {
		s.started = true
		if stream {
			s.emit(c, "response.created", map[string]any{"response": s.response})
			s.emit(c, "response.in_progress", map[string]any{"response": s.response})
		}
	}
	// Responses exposes a single generated response, not multiple choices.
	if len(r.Candidates) > 1 {
		return fmt.Errorf("multiple Gemini candidates cannot be represented as one Responses output")
	}
	for _, candidate := range r.Candidates {
		if candidate.FinishReason != nil {
			s.finish = *candidate.FinishReason
		}
		for _, p := range candidate.Content.Parts {
			if p.FunctionCall != nil {
				args, err := common.Marshal(p.FunctionCall.Arguments)
				if err != nil {
					return err
				}
				i := s.addBlock(c, map[string]any{"type": "function_call", "call_id": "call_" + common.GetUUID(), "name": p.FunctionCall.FunctionName, "arguments": ""}, stream)
				s.blocks[i].text.Write(args)
				s.usable = true
				if stream {
					f := s.fields(i)
					f["delta"] = string(args)
					s.emit(c, "response.function_call_arguments.delta", f)
				}
			} else if p.Thought {
				s.addText(c, "reasoning", p.Text, stream)
			} else if p.InlineData != nil || p.ExecutableCode != nil || p.CodeExecutionResult != nil {
				return fmt.Errorf("unsupported Gemini Responses output part")
			} else {
				s.addText(c, "message", p.Text, stream)
			}
		}
	}
	return nil
}
func (s *geminiResponsesState) complete(c *gin.Context, u *dto.Usage, stream bool) error {
	if !s.usable {
		return fmt.Errorf("empty Gemini Responses output")
	}
	s.buildAnnotations()
	status := "completed"
	switch s.finish {
	case "STOP":
	case "MAX_TOKENS":
		status = "incomplete"
		s.response["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
	case "":
		return fmt.Errorf("Gemini response missing finishReason")
	default:
		status = "incomplete"
		s.response["incomplete_details"] = map[string]any{"reason": "content_filter"}
	}
	output := make([]any, 0, len(s.blocks))
	for i, b := range s.blocks {
		b.item["status"] = status
		text := b.text.String()
		f := s.fields(i)
		switch b.item["type"] {
		case "web_search_call":
			if stream {
				s.emit(c, "response.web_search_call.completed", s.fields(i))
			}
		case "function_call":
			b.item["arguments"] = text
			if stream {
				f["arguments"] = text
				s.emit(c, "response.function_call_arguments.done", f)
			}
		case "reasoning":
			part := map[string]any{"type": "summary_text", "text": text}
			b.item["summary"] = []any{part}
			if stream {
				f["summary_index"] = 0
				f["text"] = text
				s.emit(c, "response.reasoning_summary_text.done", f)
				f = s.fields(i)
				f["summary_index"] = 0
				f["part"] = part
				s.emit(c, "response.reasoning_summary_part.done", f)
			}
		case "message":
			part := map[string]any{"type": "output_text", "text": text, "annotations": s.annotations, "logprobs": []any{}}
			b.item["content"] = []any{part}
			if stream {
				f["content_index"] = 0
				f["text"] = text
				for j, annotation := range s.annotations {
					af := s.fields(i)
					af["content_index"] = 0
					af["annotation_index"] = j
					af["annotation"] = annotation
					s.emit(c, "response.output_text.annotation.added", af)
				}
				s.emit(c, "response.output_text.done", f)
				f = s.fields(i)
				f["content_index"] = 0
				f["part"] = part
				s.emit(c, "response.content_part.done", f)
			}
		}
		output = append(output, b.item)
		if stream {
			f = s.fields(i)
			f["item"] = b.item
			s.emit(c, "response.output_item.done", f)
		}
	}
	s.response["status"] = status
	s.response["output"] = output
	s.response["usage"] = map[string]any{"input_tokens": u.PromptTokens, "output_tokens": u.CompletionTokens, "total_tokens": u.TotalTokens, "input_tokens_details": map[string]any{"cached_tokens": u.PromptTokensDetails.CachedTokens}, "output_tokens_details": map[string]any{"reasoning_tokens": u.CompletionTokenDetails.ReasoningTokens}}
	if stream {
		s.emit(c, "response."+status, map[string]any{"response": s.response})
	}
	return nil
}
func geminiResponsesError(err error) *types.NewAPIError {
	return types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusBadGateway)
}
func GeminiResponsesHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	s := newGeminiResponsesState(info)
	if !info.IsStream {
		defer service.CloseResponseBodyGracefully(resp)
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, geminiResponsesError(err)
		}
		var r dto.GeminiChatResponse
		if err := common.Unmarshal(data, &r); err != nil {
			return nil, geminiResponsesError(err)
		}
		s.captureGrounding(c, string(data), false)
		if err := s.accept(c, &r, false); err != nil {
			return nil, geminiResponsesError(err)
		}
		u := buildUsageFromGeminiMetadata(r.UsageMetadata, info.GetEstimatePromptTokens())
		if err := s.complete(c, &u, false); err != nil {
			return nil, geminiResponsesError(err)
		}
		data, err = common.Marshal(s.response)
		if err != nil {
			return nil, geminiResponsesError(err)
		}
		service.IOCopyBytesGracefully(c, resp, data)
		return &u, nil
	}
	var conversionErr error
	u, apiErr := geminiStreamHandler(c, info, resp, func(data string, r *dto.GeminiChatResponse) bool {
		conversionErr = s.accept(c, r, true)
		if conversionErr == nil {
			s.captureGrounding(c, data, true)
		}
		return conversionErr == nil
	})
	if conversionErr != nil {
		apiErr = geminiResponsesError(conversionErr)
	}
	if apiErr != nil {
		if s.started && c.Request.Context().Err() == nil {
			s.response["status"] = "failed"
			s.response["error"] = service.PublicStreamError(c, apiErr)
			s.emit(c, "response.failed", map[string]any{"response": s.response})
			c.Set("stream_terminal_error_written", true)
		}
		return nil, apiErr
	}
	// Reported partial usage on client cancellation must never emit completion.
	if c.Request.Context().Err() != nil {
		return u, nil
	}
	if err := s.complete(c, u, true); err != nil {
		return nil, geminiResponsesError(err)
	}
	return u, nil
}

// Keep provider grounding separate from text until the complete answer is known.
func (s *geminiResponsesState) captureGrounding(c *gin.Context, data string, stream bool) {
	g := gjson.Get(data, "candidates.0.groundingMetadata")
	if !g.IsObject() {
		return
	}
	s.grounding = json.RawMessage(g.Raw)
	queries := g.Get("webSearchQueries").Array()
	if len(queries) == 0 {
		return
	}
	if _, ok := s.indices["search"]; ok {
		return
	}
	i := s.addBlock(c, map[string]any{"type": "web_search_call", "action": map[string]any{"type": "search", "query": queries[0].String()}}, stream)
	s.indices["search"] = i
	if stream {
		s.emit(c, "response.web_search_call.in_progress", s.fields(i))
		s.emit(c, "response.web_search_call.searching", s.fields(i))
	}
}
func (s *geminiResponsesState) buildAnnotations() {
	s.annotations = []any{}
	g := gjson.ParseBytes(s.grounding)
	chunks := g.Get("groundingChunks").Array()
	for _, support := range g.Get("groundingSupports").Array() {
		start, end := support.Get("segment.startIndex").Int(), support.Get("segment.endIndex").Int()
		if start < 0 || end <= start {
			continue
		}
		for _, index := range support.Get("groundingChunkIndices").Array() {
			i := int(index.Int())
			if i < 0 || i >= len(chunks) {
				continue
			}
			web := chunks[i].Get("web")
			url := web.Get("uri").String()
			if url == "" {
				continue
			}
			s.annotations = append(s.annotations, map[string]any{"type": "url_citation", "start_index": start, "end_index": end, "url": url, "title": web.Get("title").String()})
		}
	}
}
