package plugins

import (
	"context"
	"fmt"
	"io"
)

// OpenFrontendArchiveMember shares the data API's bounded archive parser and
// opens any non-directory member by index without using its name as a path.
// The caller owns source and must close it after reader.
func OpenFrontendArchiveMember(ctx context.Context, source io.ReadSeeker, index int, limits RuntimeLimits) (io.ReadCloser, int64, error) {
	archive, err := openDataArchive(ctx, source, limits)
	if err != nil {
		return nil, 0, err
	}
	if index < 0 || index >= len(archive.members) {
		return nil, 0, fmt.Errorf("archive member index outside range")
	}
	member := archive.members[index]
	reader, err := archive.open(index)
	return reader, int64(member.Size), err
}
