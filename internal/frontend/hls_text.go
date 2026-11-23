package frontend

import (
	"fmt"
	"strings"
)

// HLSTextRenditionGroup is the SUBTITLES group of a session's embedded text
// renditions.
const HLSTextRenditionGroup = "subs"

// HLSTextRenditionName is the NAME of an embedded text track's rendition in
// the session's HLS master, unique within the group. A client that selects a
// rendition by name must use exactly this value.
func HLSTextRenditionName(title, language string, index int) string {
	name := title
	if name == "" {
		name = language
	}
	if name == "" {
		name = "Subtitle"
	}
	// All values in quoted HLS attributes must fit one line.
	name = strings.NewReplacer("\"", "'", "\r", " ", "\n", " ").Replace(name)
	return fmt.Sprintf("%s (%d)", name, index)
}
