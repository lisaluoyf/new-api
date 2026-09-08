package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateModelGroupRatios(t *testing.T) {
	for _, setting := range []string{
		`{}`, `{"model_group_ratios":{}}`,
		`{"manual_group_ratio":0,"model_group_ratios":{"deepseek-v3.2":0.0001,"gpt-5.6-sol":1.5}}`,
	} {
		ch := Channel{Setting: &setting}
		require.NoError(t, ch.ValidateSettings())
	}
	for _, setting := range []string{
		`{"model_group_ratios":{"a":0}}`, `{"model_group_ratios":{"a":-1}}`,
		`{"model_group_ratios":{"":1}}`, `{"model_group_ratios":{" a ":1}}`,
		`{"model_group_ratios":{"a":"1"}}`, `{"model_group_ratios":{"a":1e999}}`,
		`{"model_group_ratios":[]}`,
	} {
		ch := Channel{Setting: &setting}
		require.Error(t, ch.ValidateSettings(), setting)
	}
}
