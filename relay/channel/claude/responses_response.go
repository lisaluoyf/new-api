package claude

import (
	"encoding/base64"
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

// Billing receives Anthropic semantics; only the public Responses usage includes
// cache reads/writes in input_tokens. Thinking is part of output_tokens already.
func claudeResponsesUsage(u *dto.Usage) map[string]any {
	wire := buildOpenAIStyleUsageFromClaudeUsage(u)
	return map[string]any{"input_tokens": wire.InputTokens, "output_tokens": wire.CompletionTokens, "total_tokens": wire.TotalTokens,
		"input_tokens_details":             map[string]any{"cached_tokens": wire.PromptTokensDetails.CachedTokens, "cached_creation_tokens": cacheCreationTokensForOpenAIUsage(&wire)},
		"output_tokens_details":            map[string]any{"reasoning_tokens": wire.CompletionTokenDetails.ReasoningTokens},
		"claude_cache_creation_5_m_tokens": wire.ClaudeCacheCreation5mTokens, "claude_cache_creation_1_h_tokens": wire.ClaudeCacheCreation1hTokens}
}

type claudeResponsesBlock struct {
	native    dto.ClaudeMediaMessage
	item      map[string]any
	text      strings.Builder
	arguments strings.Builder
	stopped   bool
}
type claudeResponsesState struct {
	response                               map[string]any
	blocks                                 []*claudeResponsesBlock
	indices                                map[int]int
	usage                                  *ClaudeResponseInfo
	sequence                               int
	started, delta, done, hasUsage, usable bool
	stopReason                             string
}

func newClaudeResponsesState(info *relaycommon.RelayInfo) *claudeResponsesState {
	response := map[string]any{"id": "resp_" + common.GetUUID(), "object": "response", "created_at": common.GetTimestamp(), "model": info.OriginModelName, "status": "in_progress", "output": []any{}, "error": nil, "incomplete_details": nil, "usage": nil, "store": false, "background": false, "parallel_tool_calls": true, "metadata": map[string]any{}, "tool_choice": "auto", "tools": []any{}, "instructions": nil, "previous_response_id": nil}
	if r, ok := info.Request.(*dto.OpenAIResponsesRequest); ok {
		for k, v := range map[string]json.RawMessage{"metadata": r.Metadata, "instructions": r.Instructions, "tools": r.Tools, "tool_choice": r.ToolChoice, "parallel_tool_calls": r.ParallelToolCalls, "text": r.Text} {
			if len(v) > 0 {
				response[k] = v
			}
		}
		if r.MaxOutputTokens != nil {
			response["max_output_tokens"] = *r.MaxOutputTokens
		}
		if r.Reasoning != nil {
			response["reasoning"] = r.Reasoning
		}
	}
	return &claudeResponsesState{response: response, indices: map[int]int{}, usage: &ClaudeResponseInfo{Usage: &dto.Usage{}}}
}
func (s *claudeResponsesState) emit(c *gin.Context, typ string, fields map[string]any) error {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["type"] = typ
	fields["sequence_number"] = s.sequence
	s.sequence++
	data, err := common.Marshal(fields)
	if err != nil {
		return err
	}
	c.Render(-1, common.CustomEvent{Data: fmt.Sprintf("event: %s\n", typ)})
	c.Render(-1, common.CustomEvent{Data: "data: " + string(data)})
	// The scanner continues to the provider terminal frame on disconnect; this
	// lets its settlement rules distinguish completed work from cancellation.
	return helper.FlushWriter(c)
}
func (s *claudeResponsesState) fields(i int) map[string]any {
	return map[string]any{"item_id": s.blocks[i].item["id"], "output_index": i}
}
func (s *claudeResponsesState) startBlock(c *gin.Context, index int, b dto.ClaudeMediaMessage, stream bool) error {
	if _, ok := s.indices[index]; ok {
		return fmt.Errorf("duplicate Claude content block index")
	}
	i := len(s.blocks)
	item := map[string]any{"id": fmt.Sprintf("%s_%d", s.response["id"], i)}
	switch b.Type {
	case "text":
		item["type"] = "message"
		item["role"] = "assistant"
		item["status"] = "in_progress"
		item["content"] = []any{}
	case "tool_use":
		if b.Id == "" || b.Name == "" {
			return fmt.Errorf("invalid Claude tool_use")
		}
		item["type"] = "function_call"
		item["call_id"] = b.Id
		item["name"] = b.Name
		item["arguments"] = ""
		item["status"] = "in_progress"
	case "thinking", "redacted_thinking":
		item["type"] = "reasoning"
		item["summary"] = []any{}
	default:
		return fmt.Errorf("unsupported Claude output block %q", b.Type)
	}
	s.indices[index] = i
	s.blocks = append(s.blocks, &claudeResponsesBlock{item: item, native: b})
	if stream {
		f := s.fields(i)
		f["item"] = item
		_ = s.emit(c, "response.output_item.added", f)
		f = s.fields(i)
		switch b.Type {
		case "text":
			f["content_index"] = 0
			f["part"] = map[string]any{"type": "output_text", "text": "", "annotations": []any{}}
			_ = s.emit(c, "response.content_part.added", f)
		case "thinking":
			f["summary_index"] = 0
			f["part"] = map[string]any{"type": "summary_text", "text": ""}
			_ = s.emit(c, "response.reasoning_summary_part.added", f)
		}
	}
	if b.Text != nil {
		if err := s.addDelta(c, index, dto.ClaudeMediaMessage{Type: "text_delta", Text: b.Text}, stream); err != nil {
			return err
		}
	}
	if b.Thinking != nil {
		if err := s.addDelta(c, index, dto.ClaudeMediaMessage{Type: "thinking_delta", Thinking: b.Thinking}, stream); err != nil {
			return err
		}
	}
	if b.Type == "tool_use" && b.Input != nil {
		encoded, err := common.Marshal(b.Input)
		if err != nil {
			return err
		}
		if !stream || string(encoded) != "{}" {
			args := string(encoded)
			return s.addDelta(c, index, dto.ClaudeMediaMessage{Type: "input_json_delta", PartialJson: &args}, stream)
		}
	}
	return nil
}
func (s *claudeResponsesState) addDelta(c *gin.Context, index int, d dto.ClaudeMediaMessage, stream bool) error {
	i, ok := s.indices[index]
	if !ok || s.blocks[i].stopped {
		return fmt.Errorf("Claude delta without an open content block")
	}
	b := s.blocks[i]
	f := s.fields(i)
	typ := ""
	value := ""
	switch d.Type {
	case "text_delta":
		if b.item["type"] != "message" || d.Text == nil {
			return fmt.Errorf("invalid Claude text delta")
		}
		value = *d.Text
		b.text.WriteString(value)
		f["content_index"] = 0
		typ = "response.output_text.delta"
	case "thinking_delta":
		if b.item["type"] != "reasoning" || d.Thinking == nil {
			return fmt.Errorf("invalid Claude thinking delta")
		}
		value = *d.Thinking
		b.text.WriteString(value)
		f["summary_index"] = 0
		typ = "response.reasoning_summary_text.delta"
	case "input_json_delta":
		if b.item["type"] != "function_call" || d.PartialJson == nil {
			return fmt.Errorf("invalid Claude tool delta")
		}
		value = *d.PartialJson
		b.arguments.WriteString(value)
		typ = "response.function_call_arguments.delta"
	case "signature_delta":
		if b.item["type"] != "reasoning" {
			return fmt.Errorf("signature outside reasoning block")
		}
		b.native.Signature += d.Signature
		return nil
	default:
		return fmt.Errorf("unsupported Claude delta %q", d.Type)
	}
	if value != "" {
		s.usable = true
	}
	if stream {
		f["delta"] = value
		_ = s.emit(c, typ, f)
	}
	return nil
}
func (s *claudeResponsesState) stopBlock(c *gin.Context, index int, stream bool) error {
	i, ok := s.indices[index]
	if !ok || s.blocks[i].stopped {
		return fmt.Errorf("Claude stop without an open content block")
	}
	b := s.blocks[i]
	b.stopped = true
	f := s.fields(i)
	switch b.item["type"] {
	case "message":
		part := map[string]any{"type": "output_text", "text": b.text.String(), "annotations": []any{}}
		b.item["content"] = []any{part}
		b.item["status"] = "completed"
		if stream {
			f["content_index"] = 0
			f["text"] = b.text.String()
			_ = s.emit(c, "response.output_text.done", f)
			f = s.fields(i)
			f["content_index"] = 0
			f["part"] = part
			_ = s.emit(c, "response.content_part.done", f)
		}
	case "function_call":
		args := b.arguments.String()
		if args == "" {
			args = "{}"
		}
		var obj map[string]any
		if err := common.UnmarshalJsonStr(args, &obj); err != nil || obj == nil {
			return fmt.Errorf("Claude returned invalid function arguments")
		}
		b.item["arguments"] = args
		b.item["status"] = "completed"
		s.usable = true
		if stream {
			f["name"] = b.item["name"]
			f["arguments"] = args
			_ = s.emit(c, "response.function_call_arguments.done", f)
		}
	case "reasoning":
		if b.native.Type == "thinking" {
			thinking := b.text.String()
			b.native.Thinking = &thinking
		}
		if b.native.Signature != "" || len(b.native.Data) > 0 {
			encoded, err := common.Marshal(b.native)
			if err != nil {
				return err
			}
			b.item["encrypted_content"] = "anthropic_v1:" + base64.StdEncoding.EncodeToString(encoded)
		}
		if b.text.Len() > 0 {
			part := map[string]any{"type": "summary_text", "text": b.text.String()}
			b.item["summary"] = []any{part}
			if stream {
				f["summary_index"] = 0
				f["text"] = b.text.String()
				_ = s.emit(c, "response.reasoning_summary_text.done", f)
				f = s.fields(i)
				f["summary_index"] = 0
				f["part"] = part
				_ = s.emit(c, "response.reasoning_summary_part.done", f)
			}
		}
	}
	if stream {
		f = s.fields(i)
		f["item"] = b.item
		_ = s.emit(c, "response.output_item.done", f)
	}
	return nil
}
func (s *claudeResponsesState) finish() error {
	if !s.hasUsage {
		return fmt.Errorf("Claude response is missing terminal usage")
	}
	for _, b := range s.blocks {
		if !b.stopped {
			return fmt.Errorf("Claude response has unfinished content blocks")
		}
	}
	status := "completed"
	switch s.stopReason {
	case "max_tokens":
		status = "incomplete"
		s.response["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
	case "end_turn", "stop_sequence", "tool_use", "refusal":
	default:
		return fmt.Errorf("unsupported Claude stop reason %q", s.stopReason)
	}
	if !s.usable && s.stopReason != "refusal" && s.stopReason != "max_tokens" {
		return fmt.Errorf("Claude returned no usable output")
	}
	output := make([]any, 0, len(s.blocks))
	for _, b := range s.blocks {
		output = append(output, b.item)
	}
	u := s.usage.Usage
	if u.PromptTokens < 0 || u.CompletionTokens < 0 || u.PromptTokensDetails.CachedTokens < 0 || u.PromptTokensDetails.CachedCreationTokens < 0 || u.ClaudeCacheCreation5mTokens < 0 || u.ClaudeCacheCreation1hTokens < 0 || u.CompletionTokenDetails.ReasoningTokens < 0 || u.CompletionTokenDetails.ReasoningTokens > u.CompletionTokens {
		return fmt.Errorf("invalid Claude usage counters")
	}
	// Some providers only supply split cache creation counters.
	if split := u.ClaudeCacheCreation5mTokens + u.ClaudeCacheCreation1hTokens; split > u.PromptTokensDetails.CachedCreationTokens {
		u.PromptTokensDetails.CachedCreationTokens = split
	}
	s.usage.Usage.TotalTokens = s.usage.Usage.PromptTokens + s.usage.Usage.CompletionTokens
	s.response["status"] = status
	s.response["output"] = output
	s.response["usage"] = claudeResponsesUsage(s.usage.Usage)
	s.done = true
	return nil
}
func claudeResponsesUpstreamError(err error) *types.NewAPIError {
	return types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusBadGateway)
}

func ClaudeResponsesHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	s := newClaudeResponsesState(info)
	if !info.IsStream {
		defer service.CloseResponseBodyGracefully(resp)
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, claudeResponsesUpstreamError(err)
		}
		var r dto.ClaudeResponse
		if err := common.Unmarshal(data, &r); err != nil {
			return nil, claudeResponsesUpstreamError(err)
		}
		if e := r.GetClaudeError(); e != nil {
			return nil, types.WithClaudeError(*e, http.StatusBadGateway)
		}
		if r.Type != "message" || r.Role != "assistant" {
			return nil, claudeResponsesUpstreamError(fmt.Errorf("invalid Claude message response"))
		}
		FormatClaudeResponseInfo(&dto.ClaudeResponse{Type: "message_start", Message: &dto.ClaudeMediaMessage{Usage: r.Usage}}, nil, s.usage)
		s.hasUsage = r.Usage != nil && gjson.GetBytes(data, "usage.input_tokens").Exists() && gjson.GetBytes(data, "usage.output_tokens").Exists()
		s.stopReason = r.StopReason
		maybeMarkClaudeRefusal(c, r.StopReason)
		for i, b := range r.Content {
			if b.Type == "tool_use" {
				b.Input = json.RawMessage(gjson.GetBytes(data, fmt.Sprintf("content.%d.input", i)).Raw)
			}
			if err := s.startBlock(c, i, b, false); err != nil {
				return nil, claudeResponsesUpstreamError(err)
			}
			if err := s.stopBlock(c, i, false); err != nil {
				return nil, claudeResponsesUpstreamError(err)
			}
		}
		if err := s.finish(); err != nil {
			return nil, claudeResponsesUpstreamError(err)
		}
		encoded, err := common.Marshal(s.response)
		if err != nil {
			return nil, claudeResponsesUpstreamError(err)
		}
		service.IOCopyBytesGracefully(c, resp, encoded)
		return s.usage.Usage, nil
	}
	var failure *types.NewAPIError
	streamStatus := helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		var r dto.ClaudeResponse
		fail := func(err error) { failure = claudeResponsesUpstreamError(err); sr.Stop(failure) }
		if err := common.UnmarshalJsonStr(data, &r); err != nil {
			fail(err)
			return
		}
		if e := r.GetClaudeError(); e != nil {
			failure = types.WithClaudeError(*e, http.StatusBadGateway)
			sr.Stop(failure)
			return
		}
		FormatClaudeResponseInfo(&r, nil, s.usage)
		switch r.Type {
		case "ping":
			return
		case "message_start":
			if s.started || r.Message == nil || r.Message.Usage == nil || !gjson.Get(data, "message.usage.input_tokens").Exists() {
				fail(fmt.Errorf("invalid Claude message_start"))
				return
			}
			s.started = true
			sr.ObserveResponseID(r.Message.Id)
			_ = s.emit(c, "response.created", map[string]any{"response": s.response})
			_ = s.emit(c, "response.in_progress", map[string]any{"response": s.response})
		case "content_block_start":
			if !s.started || s.delta || r.ContentBlock == nil || r.Index == nil {
				fail(fmt.Errorf("invalid Claude content_block_start"))
				return
			}
			if r.ContentBlock.Type == "tool_use" {
				r.ContentBlock.Input = json.RawMessage(gjson.Get(data, "content_block.input").Raw)
			}
			if err := s.startBlock(c, *r.Index, *r.ContentBlock, true); err != nil {
				fail(err)
			}
		case "content_block_delta":
			if s.delta || r.Delta == nil || r.Index == nil {
				fail(fmt.Errorf("invalid Claude content_block_delta"))
				return
			}
			if err := s.addDelta(c, *r.Index, *r.Delta, true); err != nil {
				fail(err)
			}
		case "content_block_stop":
			if r.Index == nil {
				fail(fmt.Errorf("invalid Claude content_block_stop"))
				return
			}
			if err := s.stopBlock(c, *r.Index, true); err != nil {
				fail(err)
			}
		case "message_delta":
			if !s.started || s.delta || r.Delta == nil || r.Delta.StopReason == nil {
				fail(fmt.Errorf("invalid Claude message_delta"))
				return
			}
			s.delta = true
			s.stopReason = *r.Delta.StopReason
			s.hasUsage = r.Usage != nil && gjson.Get(data, "usage.output_tokens").Exists()
			if s.hasUsage {
				s.usage.Usage.CompletionTokens = r.Usage.OutputTokens
			}
			maybeMarkClaudeRefusal(c, s.stopReason)
		case "message_stop":
			if !s.delta {
				fail(fmt.Errorf("Claude message_stop without message_delta"))
				return
			}
			if err := s.finish(); err != nil {
				fail(err)
				return
			}
			// Record billable completion before writing the terminal frame, which can
			// itself cause a client cancellation. Stop reading without awaiting EOF.
			sr.CompleteWithUsage("message_stop", fmt.Sprint(s.response["id"]))
			_ = s.emit(c, "response."+fmt.Sprint(s.response["status"]), map[string]any{"response": s.response})
		default:
			fail(fmt.Errorf("unsupported Claude stream event %q", r.Type))
		}
	})
	if failure != nil {
		if s.started && c.Request.Context().Err() == nil {
			s.response["status"] = "failed"
			s.response["error"] = map[string]any{"code": string(failure.GetErrorCode()), "message": "Upstream stream ended unexpectedly"}
			_ = s.emit(c, "response.failed", map[string]any{"response": s.response})
			c.Set("stream_terminal_error_written", true)
		}
		return nil, failure
	}
	if err := service.ValidateRelayStreamEnd(c, info, streamStatus, s.done, s.usable || s.stopReason == "refusal" || s.stopReason == "max_tokens"); err != nil {
		return nil, err
	}
	return s.usage.Usage, nil
}
