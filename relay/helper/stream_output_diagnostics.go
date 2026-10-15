package helper

// ContextKeyStreamOutputDiagnostics contains only frame kinds and counts, never
// generated text, tool arguments, request data or upstream identifiers.
const ContextKeyStreamOutputDiagnostics = "stream_output_diagnostics"

type StreamOutputDiagnostics struct {
	Protocol           string `json:"protocol"`
	Frames             int    `json:"frames"`
	FirstFrameKind     string `json:"first_frame_kind"`
	LastFrameKind      string `json:"last_frame_kind"`
	FirstUsableKind    string `json:"first_usable_kind,omitempty"`
	ContentChars       int    `json:"content_chars"`
	ReasoningChars     int    `json:"reasoning_chars"`
	ToolValueChars     int    `json:"tool_value_chars"`
	RefusalChars       int    `json:"refusal_chars"`
	BufferedFrames     int    `json:"buffered_frames"`
	BufferedBytes      int    `json:"buffered_bytes"`
	BufferLimitReached bool   `json:"buffer_limit_reached,omitempty"`
}
