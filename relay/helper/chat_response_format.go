package helper

import (
	"bufio"
	"io"
	"net/http"
	"strings"
)

type bufferedChatResponseBody struct {
	io.Reader
	io.Closer
}

// NormalizeChatResponseFormat tolerates providers that label a non-streaming
// JSON completion as text/event-stream. Sniff only a bounded prefix, retain all
// bytes and keep Close attached to the original transport body. Actual streams
// are never read to completion before being dispatched.
func NormalizeChatResponseFormat(resp *http.Response, requestedStream bool) (bool, error) {
	if resp == nil {
		return requestedStream, nil
	}
	stream := strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")
	if requestedStream || !stream || resp.Body == nil {
		return requestedStream || stream, nil
	}

	body := resp.Body
	reader := bufio.NewReader(body)
	resp.Body = &bufferedChatResponseBody{Reader: reader, Closer: body}
	for n := 1; n <= 512; n++ {
		prefix, err := reader.Peek(n)
		if err != nil {
			if err == io.EOF {
				return stream, nil
			}
			return false, err
		}
		switch prefix[n-1] {
		case ' ', '\t', '\r', '\n':
			continue
		case '{', '[':
			resp.Header.Set("Content-Type", "application/json")
			return false, nil
		default:
			return true, nil
		}
	}
	return stream, nil
}
