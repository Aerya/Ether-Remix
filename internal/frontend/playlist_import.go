package frontend

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
)

// PlaylistImportRequest starts an import into a library: a playlist link,
// or a playlist file (format m3u or csv) with its text.
type PlaylistImportRequest struct {
	Library    core.LibraryID
	URL        string
	FileName   string
	FileFormat string
	FileText   string
}

// ImportMatched is a source track matched to a library title.
type ImportMatched struct {
	Index  int
	Source core.ImportTrack
	Title  Title
	By     string // isrc | title
}

// ImportUnmatched is a source track left out, with why and, when several
// releases fit, the candidates.
type ImportUnmatched struct {
	Index      int
	Source     core.ImportTrack
	Reason     string // not_found | ambiguous
	Candidates []Title
}

// PlaylistImport is an import's progress and result.
type PlaylistImport struct {
	ID          string
	State       string // reading | matching | done | failed
	Name        string
	Description string
	Total, Done int
	Matched     []ImportMatched
	Unmatched   []ImportUnmatched
	Error       string
	// Truncated: the link's source shared fewer tracks than the playlist
	// has (SourceTotal when the source said how many).
	Truncated   bool
	SourceTotal int
}

// PlaylistImports imports playlists from links and files.
type PlaylistImports interface {
	PlaylistImportSupport(ctx context.Context) (links []string, files bool)
	StartPlaylistImport(ctx context.Context, viewer *core.Viewer, req PlaylistImportRequest) (string, error)
	PlaylistImport(ctx context.Context, viewer *core.Viewer, id string) (PlaylistImport, error)
	SavePlaylistImport(ctx context.Context, viewer *core.Viewer, id, name string, exclude []int, choose map[int]core.RatingKey) (*core.Playlist, error)
}

// ErrUnsupportedImport means the link or file cannot be imported.
var ErrUnsupportedImport = core.ErrUnsupportedImport

// ErrInvalidImportChoice means a choice is not one of a track's candidates.
var ErrInvalidImportChoice = core.ErrInvalidImportChoice

// ImportTrack re-exports core.ImportTrack.
type ImportTrack = core.ImportTrack
