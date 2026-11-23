package debrid

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nzb"
)

func TestSeasonPackEpisodeSizeOutliers(t *testing.T) {
	const gb = int64(1 << 30)
	const mb = int64(1 << 20)

	// Torrent season pack: median episode size is ~2 GiB.
	// - S01E03 has only a 50 MiB teaser clip (< 15% of median) -> must be rejected.
	// - S01E04 has both a 10 GiB full-season recap (> 400% of median) and the real 2.1 GiB episode -> must pick 2.1 GiB.
	// - S01E05 is a 3.8 GiB double-length finale (190% of median) -> must be accepted.
	// - S01E06E07 is a 4.2 GiB combined file (2.1 GiB per episode) -> must be accepted.
	files := []File{
		{ID: "e1", Path: "Show.S01/Show.S01E01.1080p.mkv", Size: 2 * gb},
		{ID: "e2", Path: "Show.S01/Show.S01E02.1080p.mkv", Size: 2 * gb},
		{ID: "e3-tiny", Path: "Show.S01/Show.S01E03.1080p.mkv", Size: 50 * mb},
		{ID: "e4-huge", Path: "Show.S01/Show.S01E04.Season.Compilation.1080p.mkv", Size: 10 * gb},
		{ID: "e4-real", Path: "Show.S01/Show.S01E04.1080p.mkv", Size: 2100 * mb},
		{ID: "e5-finale", Path: "Show.S01/Show.S01E05.1080p.mkv", Size: 3800 * mb},
		{ID: "e6e7", Path: "Show.S01/Show.S01E06E07.1080p.mkv", Size: 4200 * mb},
	}

	if _, err := ChooseFile(files, &core.ReleaseTarget{Season: 1, Episode: 3}); !errors.Is(err, core.ErrWrongContent) {
		t.Fatalf("tiny S01E03 outlier (<15%% median): err=%v, want ErrWrongContent", err)
	}
	got4, err := ChooseFile(files, &core.ReleaseTarget{Season: 1, Episode: 4})
	if err != nil || got4.ID != "e4-real" {
		t.Fatalf("S01E04 with >400%% outlier present: got %+v err=%v, want e4-real", got4, err)
	}
	got5, err := ChooseFile(files, &core.ReleaseTarget{Season: 1, Episode: 5})
	if err != nil || got5.ID != "e5-finale" {
		t.Fatalf("S01E05 double-length finale: got %+v err=%v, want e5-finale", got5, err)
	}
	got6, err := ChooseFile(files, &core.ReleaseTarget{Season: 1, Episode: 6})
	if err != nil || got6.ID != "e6e7" {
		t.Fatalf("S01E06E07 combined episode: got %+v err=%v, want e6e7", got6, err)
	}

	// Usenet season pack: verify <15% and >400% outliers are also rejected in EpisodeNZB.
	var sb strings.Builder
	sb.WriteString(`<nzb>`)
	addFile := func(id int, name string, bytes int64) {
		fmt.Fprintf(&sb, `<file poster="p" date="1" subject="&quot;%s&quot; yEnc (1/1)"><groups><group>a.b</group></groups><segments><segment bytes="%d" number="1">id%d@x</segment></segments></file>`, name, bytes, id)
	}
	addFile(1, "Show.S01E01.1080p.mkv", 2_000_000_000)
	addFile(2, "Show.S01E02.1080p.mkv", 2_000_000_000)
	addFile(3, "Show.S01E03.1080p.mkv", 40_000_000)     // 2% of median -> reject
	addFile(4, "Show.S01E04.1080p.mkv", 10_000_000_000) // 500% of median -> reject
	addFile(5, "Show.S01E05.1080p.mkv", 2_100_000_000)
	sb.WriteString(`</nzb>`)
	nzbData := []byte(sb.String())

	if _, err := EpisodeNZB(nzbData, &core.ReleaseTarget{Season: 1, Episode: 3}); !errors.Is(err, core.ErrNotAvailable) {
		t.Fatalf("Usenet tiny S01E03 outlier: err=%v, want ErrNotAvailable", err)
	}
	if _, err := EpisodeNZB(nzbData, &core.ReleaseTarget{Season: 1, Episode: 4}); !errors.Is(err, core.ErrNotAvailable) {
		t.Fatalf("Usenet huge S01E04 outlier: err=%v, want ErrNotAvailable", err)
	}
	out5, err := EpisodeNZB(nzbData, &core.ReleaseTarget{Season: 1, Episode: 5})
	if err != nil {
		t.Fatalf("Usenet normal S01E05: %v", err)
	}
	doc, err := nzb.Parse(out5)
	if err != nil || len(doc.Files) != 1 || doc.Files[0].Name != "Show.S01E05.1080p.mkv" {
		t.Fatalf("Usenet S01E05 files=%+v err=%v", doc, err)
	}
}
