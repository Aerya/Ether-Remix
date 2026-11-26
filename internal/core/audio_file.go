package core

import (
	"path/filepath"
	"strings"
)

// AudioFileHint reports the container and, when the extension identifies it,
// the codec of a standalone audio file. These are estimates until probed.
func AudioFileHint(path string) (container, codec string) {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".flac":
		return "flac", "flac"
	case ".mp3":
		return "mp3", "mp3"
	case ".opus":
		return "ogg", "opus"
	case ".ogg", ".oga":
		return "ogg", ""
	case ".m4a", ".m4b":
		return "mp4", ""
	case ".aac":
		return "aac", "aac"
	case ".wav":
		return "wav", ""
	}
	return "", ""
}
