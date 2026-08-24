package deepseek

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModelListIncludesV4VisionVariants(t *testing.T) {
	require.Contains(t, ModelList, "deepseek-v4-flash-vision-exp")
	require.Contains(t, ModelList, "deepseek-v4-flash-vision-exp-none")
	require.Contains(t, ModelList, "deepseek-v4-flash-vision-exp-max")
}
