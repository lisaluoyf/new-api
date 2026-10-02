package service

import "strings"

// IsTextEmbeddingModel identifies the dense embedding models in our catalog,
// including provider-qualified names used by upstream model mappings.
func IsTextEmbeddingModel(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if index := strings.LastIndexByte(name, '/'); index >= 0 {
		name = name[index+1:]
	}
	return name == "text-embedding-3-small" || name == "bge-m3"
}
