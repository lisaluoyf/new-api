package service

// IsVeo31VideoModel recognizes the public Google model IDs on both APIMart routes.
func IsVeo31VideoModel(name string) bool {
	return name == "veo-3.1-generate-preview" || name == "veo-3.1-fast-generate-preview"
}
