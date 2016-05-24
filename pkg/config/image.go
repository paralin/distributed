package config

type TargetImage struct {
	Image    string   "image"
	Versions []string "versions"
}

type ImageSyncConfig struct {
}
