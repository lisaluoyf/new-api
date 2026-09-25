package dto

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestMessageParseContentPreservesJSONCacheControl(t *testing.T) {
	for _, marker := range []string{`{"type":"ephemeral"}`, `{"type":"ephemeral","ttl":"1h"}`} {
		t.Run(marker, func(t *testing.T) {
			var message Message
			err := common.Unmarshal([]byte(`{"role":"user","content":[
				{"type":"text","text":"cached prefix","cache_control":`+marker+`},
				{"type":"image_url","image_url":{"url":"data:image/png;base64,aGVsbG8="},"cache_control":`+marker+`},
				{"type":"file","file":{"filename":"notes.txt","file_data":"aGVsbG8="},"cache_control":`+marker+`},
				{"type":"text","text":"uncached suffix"},
				{"type":"text","text":"null marker","cache_control":null}
			]}`), &message)
			require.NoError(t, err)
			for pass := 0; pass < 2; pass++ {
				blocks := message.ParseContent()
				require.Len(t, blocks, 5)
				for i := 0; i < 3; i++ {
					require.JSONEq(t, marker, string(blocks[i].CacheControl))
				}
				require.Empty(t, blocks[3].CacheControl)
				require.Empty(t, blocks[4].CacheControl)
				encoded, err := common.Marshal(blocks)
				require.NoError(t, err)
				require.Equal(t, "cached prefix", gjson.GetBytes(encoded, "0.text").String())
				require.Equal(t, "data:image/png;base64,aGVsbG8=", gjson.GetBytes(encoded, "1.image_url.url").String())
				require.Equal(t, "notes.txt", gjson.GetBytes(encoded, "2.file.filename").String())
				require.False(t, gjson.GetBytes(encoded, "3.cache_control").Exists())
				require.False(t, gjson.GetBytes(encoded, "4.cache_control").Exists())
			}
		})
	}
}
