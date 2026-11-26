// Package releasenotes makes the tagged Markdown available to release builds.
package releasenotes

import (
	"embed"
	"strings"
)

//go:embed v*.md
var files embed.FS

// ForVersion looks up the exact tag on disk. An unversioned build has no notes.
func ForVersion(version string) string {
	filename := "v" + strings.TrimPrefix(version, "v") + ".md"
	if strings.ContainsAny(filename, "/\\") {
		return ""
	}
	b, _ := files.ReadFile(filename)
	return string(b)
}
