package frontend

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
)

// DuplicateGroup is one recording with several releases in a library, and
// the viewer's preferred release when they chose one.
type DuplicateGroup struct {
	RecordingID string
	Count       int
	Titles      []Title
	Preferred   core.RatingKey
}

// DuplicatesPage pages a library's duplicate groups.
type DuplicatesPage struct {
	Groups []DuplicateGroup
	Total  int
	Next   string
}

// Duplicates answers duplicate review for a viewer.
type Duplicates interface {
	LibraryDuplicates(ctx context.Context, viewer *core.Viewer, library core.LibraryID, cursor string, limit int) (DuplicatesPage, error)
	TitleRecording(ctx context.Context, viewer *core.Viewer, key core.RatingKey) (DuplicateGroup, error)
	PreferRecording(ctx context.Context, viewer *core.Viewer, library core.LibraryID, recording string, preferred core.RatingKey, hide []core.RatingKey) (DuplicateGroup, error)
}
