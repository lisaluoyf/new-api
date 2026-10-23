package service

// IsVeo31VideoModel identifies Google per-second video models on APIMart routes.
func IsVeo31VideoModel(name string) bool {
	return name == "gemini-omni-1.1-flash" || name == "veo-3.1-generate-preview" || name == "veo-3.1-fast-generate-preview" || name == "veo-3.1-lite-generate-preview"
}
