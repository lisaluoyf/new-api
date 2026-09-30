package claude

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func decodeClaudeResponsesTurn(encrypted string) ([]dto.ClaudeMediaMessage, error) {
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(encrypted, "anthropic_turn_v1:"))
	if err != nil {
		return nil, responsesBadRequest("invalid Claude turn envelope")
	}
	var raw []json.RawMessage
	if err := common.Unmarshal(data, &raw); err != nil || len(raw) == 0 {
		return nil, responsesBadRequest("invalid Claude turn envelope")
	}
	blocks := make([]dto.ClaudeMediaMessage, 0, len(raw))
	for _, item := range raw {
		var b dto.ClaudeMediaMessage
		if err := common.Unmarshal(item, &b); err != nil {
			return nil, responsesBadRequest("invalid Claude turn block")
		}
		switch b.Type {
		case "text":
			if b.Text == nil {
				return nil, responsesBadRequest("invalid Claude turn text")
			}
		case "thinking":
			if b.Signature == "" || b.Thinking == nil {
				return nil, responsesBadRequest("invalid signed Claude turn thinking")
			}
		case "redacted_thinking":
			if len(b.Data) == 0 {
				return nil, responsesBadRequest("invalid redacted Claude turn thinking")
			}
		case "tool_use", "server_tool_use":
			if b.Id == "" || b.Name == "" {
				return nil, responsesBadRequest("invalid Claude turn tool")
			}
			if b.Type == "server_tool_use" && b.Name != "web_search" {
				return nil, responsesBadRequest("unsupported Claude server tool")
			}
			b.Input = json.RawMessage(gjson.GetBytes(item, "input").Raw)
		case "web_search_tool_result":
			if b.ToolUseId == "" {
				return nil, responsesBadRequest("invalid Claude search result")
			}
			b.Content = json.RawMessage(gjson.GetBytes(item, "content").Raw)
		default:
			return nil, responsesBadRequest("unsupported Claude turn block %s", b.Type)
		}
		blocks = append(blocks, b)
	}
	return blocks, nil
}

func (s *claudeResponsesState) annotations(b *claudeResponsesBlock) []any {
	out := make([]any, 0)
	var citations []map[string]any
	_ = common.Unmarshal(b.native.Citations, &citations)
	for _, citation := range citations {
		u, ok := citation["url"].(string)
		if !ok || u == "" {
			continue
		}
		title, _ := citation["title"].(string)
		out = append(out, map[string]any{"type": "url_citation", "url": u, "title": title, "start_index": 0, "end_index": utf8.RuneCountInString(b.text.String())})
	}
	return out
}
func (s *claudeResponsesState) searchResults(c *gin.Context, b *claudeResponsesBlock, stream bool) error {
	for i, call := range s.blocks {
		if call.native.Type != "server_tool_use" || call.native.Id != b.native.ToolUseId {
			continue
		}
		data, err := common.Marshal(b.native.Content)
		if err != nil {
			return err
		}
		var results []struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		}
		failed := false
		if gjson.GetBytes(data, "type").String() == "web_search_tool_result_error" {
			failed = true
		} else if err := common.Unmarshal(data, &results); err != nil {
			return fmt.Errorf("invalid Claude web search result")
		}
		sources := make([]any, 0, len(results))
		for _, r := range results {
			if r.URL != "" {
				sources = append(sources, map[string]any{"type": "url", "url": r.URL, "title": r.Title})
			}
		}
		action := call.item["action"].(map[string]any)
		action["sources"] = sources
		call.item["status"] = "completed"
		if failed {
			call.item["status"] = "failed"
		}
		if stream {
			typ := "response.web_search_call.completed"
			if failed {
				typ = "response.web_search_call.failed"
			}
			_ = s.emit(c, typ, s.fields(i))
			f := s.fields(i)
			f["item"] = call.item
			_ = s.emit(c, "response.output_item.done", f)
		}
		return nil
	}
	return fmt.Errorf("Claude web search result has no matching call")
}
func (s *claudeResponsesState) appendSearchTurn(c *gin.Context, stream bool, output []any) ([]any, error) {
	if !s.hasSearch {
		return output, nil
	}
	native := make([]dto.ClaudeMediaMessage, 0, len(s.blocks))
	for _, b := range s.blocks {
		native = append(native, b.native)
	}
	data, err := common.Marshal(native)
	if err != nil {
		return nil, err
	}
	item := map[string]any{"type": "reasoning", "id": fmt.Sprintf("%s_search_state", s.response["id"]), "summary": []any{}, "encrypted_content": "anthropic_turn_v1:" + base64.StdEncoding.EncodeToString(data)}
	if stream {
		_ = s.emit(c, "response.output_item.added", map[string]any{"output_index": len(output), "item": map[string]any{"type": "reasoning", "id": item["id"], "summary": []any{}}})
		_ = s.emit(c, "response.output_item.done", map[string]any{"output_index": len(output), "item": item})
	}
	return append(output, item), nil
}
