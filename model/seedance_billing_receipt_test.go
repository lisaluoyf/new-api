package model

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSeedanceReceiptJSONHandlesPostgresTextAndByteDrivers(t *testing.T) {
	for _, value := range []any{`{"status":"settled"}`, []byte(`{"status":"settled"}`)} {
		var data SeedanceReceiptJSON
		require.NoError(t, data.Scan(value))
		require.Equal(t, `{"status":"settled"}`, string(data))
		stored, err := data.Value()
		require.NoError(t, err)
		require.Equal(t, `{"status":"settled"}`, stored)
		var restored SeedanceReceiptJSON
		require.NoError(t, restored.Scan(stored))
		require.Equal(t, data, restored)
	}
	var data SeedanceReceiptJSON
	require.NoError(t, data.Scan(nil))
	require.Nil(t, data)
	require.Error(t, data.Scan(123))
}
