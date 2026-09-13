package types

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWithOpenAIErrorModelNotFoundType(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		kind string
		code any
		want ErrorCode
	}{
		{"missing code", "model_not_found", nil, ErrorCodeModelNotFound},
		{"empty code", "model_not_found", "", ErrorCodeModelNotFound},
		{"explicit code wins", "model_not_found", "invalid_request", ErrorCodeInvalidRequest},
		{"unrelated type", "invalid_request_error", nil, "unknown_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := WithOpenAIError(OpenAIError{Message: "missing model", Type: tc.kind, Code: tc.code}, http.StatusNotFound)
			require.Equal(t, tc.want, err.GetErrorCode())
		})
	}
}
