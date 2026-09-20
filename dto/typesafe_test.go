package dto

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestTypeSafeRequest(t *testing.T) {
	valid := `{"model":"jev-latest","state":{"amount":0,"active":false},"questions":{"a":{"type":"noul","instructions":"Active?","speculative":false},"b":{"type":"choice","instructions":"Choose","criteria":{"yes":null,"no":"Inactive"}},"c":{"type":"score","instructions":"Rate","criteria":["low","high"]}}}`
	var r TypeSafeRequest
	require.NoError(t, common.Unmarshal([]byte(valid), &r))
	require.NoError(t, r.Validate())
	data, err := common.Marshal(r)
	require.NoError(t, err)
	require.JSONEq(t, valid, string(data))
	require.Contains(t, r.GetTokenCountMeta().CombineText, `"amount":0`)
	for _, raw := range []string{
		`{}`, `{"model":"jev-latest","state":null,"questions":{}}`,
		`{"model":"jev-latest","state":"x","stream":true,"questions":{"a":{"type":"noul","instructions":"x"}}}`,
		`{"model":"jev-latest","state":"x","questions":{"a":{"type":"chat","instructions":"x"}}}`,
		`{"model":"jev-latest","state":"x","questions":{"a":{"type":"score","instructions":"x","criteria":["only"]}}}`,
	} {
		r = TypeSafeRequest{}
		require.NoError(t, common.Unmarshal([]byte(raw), &r))
		require.Error(t, r.Validate())
	}
}
