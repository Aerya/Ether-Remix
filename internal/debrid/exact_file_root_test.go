package debrid

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// One provider lists a torrent without its root folder and with a leading
// slash, another with the root folder: an ExactFile recorded from either must
// select the same file from the other.
func TestChooseFileExactFileAcrossProviderRootFolders(t *testing.T) {
	torbox := []File{
		{ID: "1", Path: "System Of A Down - Hypnotize/01 - Attack.flac", Size: 30},
		{ID: "2", Path: "System Of A Down - Hypnotize/02 - Dreaming.flac", Size: 31},
	}
	realDebrid := []File{
		{ID: "1", Path: "/01 - Attack.flac", Size: 30},
		{ID: "2", Path: "/02 - Dreaming.flac", Size: 31},
	}
	for _, tc := range []struct {
		name  string
		files []File
		want  string
	}{
		{"rd target on torbox list", torbox, "/02 - Dreaming.flac"},
		{"torbox target on rd list", realDebrid, "System Of A Down - Hypnotize/02 - Dreaming.flac"},
	} {
		got, err := ChooseFile(tc.files, &core.ReleaseTarget{AllowNonVideo: true, ExactFile: tc.want})
		if err != nil || got.ID != "2" {
			t.Fatalf("%s: got %+v, %v; want file 2", tc.name, got, err)
		}
	}
}
