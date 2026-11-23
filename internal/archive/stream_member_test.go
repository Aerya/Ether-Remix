package archive

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	at "github.com/braedonsaunders/ether/internal/archive/archivetest"
)

func matchByName(want string) func(int, Member) bool {
	return func(_ int, m Member) bool {
		return m.Name == want
	}
}

func matchAny(_ int, _ Member) bool { return true }

func TestStream7zNonSolidReadsOnlyTargetFolder(t *testing.T) {
	ctx := context.Background()
	prefixData := randBytes(101, 96*1024)
	targetData := bytes.Repeat([]byte("chapter-two-non-solid-7z-payload-"), 1500)
	tailData := randBytes(102, 8*1024)

	arcBytes := at.Build7zLZMA2([]at.SevenZipFile{
		{Name: "00_prefix.bin", Data: prefixData},
		{Name: "books/target.epub", Data: targetData},
		{Name: "99_tail.bin", Data: tailData},
	}, false)

	members, err := List7z(bytes.NewReader(arcBytes), int64(len(arcBytes)))
	if err != nil {
		t.Fatalf("List7z: %v", err)
	}
	if len(members) != 3 {
		t.Fatalf("got %d members, want 3", len(members))
	}
	by := byName(members)
	target := by["books/target.epub"]
	if target.Solid || target.Stored || target.Method != "lzma2" {
		t.Fatalf("unexpected target member metadata: %+v", target)
	}

	rr := &recReader{r: bytes.NewReader(arcBytes)}
	vols := []Volume{{R: rr, Size: int64(len(arcBytes))}}
	solid, err := Inspect7zMember(ctx, vols, matchByName("books/target.epub"), 64<<20, 64<<20)
	if err != nil {
		t.Fatalf("Inspect7zMember: %v", err)
	}
	if solid {
		t.Fatalf("Inspect7zMember reported solid=true for single-file folder")
	}

	// Folder 0 starts immediately after the 32-byte signature header. Opening and reading
	// folder 1 ("books/target.epub") must never read folder 0's compressed bytes or folder 2's tail.
	rr = &recReader{r: bytes.NewReader(arcBytes)}
	vols = []Volume{{R: rr, Size: int64(len(arcBytes))}}
	rc, gotMember, err := Open7zMember(ctx, vols, matchByName("books/target.epub"), 64<<20, 64<<20)
	if err != nil {
		t.Fatalf("Open7zMember: %v", err)
	}
	if gotMember.Name != "books/target.epub" {
		t.Fatalf("got member %q, want books/target.epub", gotMember.Name)
	}
	got, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatalf("ReadAll(Open7zMember): %v", err)
	}
	if !bytes.Equal(got, targetData) {
		t.Fatalf("decoded non-solid 7z member mismatch (%d vs %d bytes)", len(got), len(targetData))
	}
	// Prefix data is 96 KiB random -> ~96 KiB packed starting at offset 32.
	rr.assertNoDataReads(t, 32, 64*1024, "00_prefix.bin")

	// Plan folder 1's exact pack offset and flip a byte inside folder 1's packed stream;
	// Open7zMember / ReadAll must fail with ErrCorrupt.
	plan, err := plan7zMember(ctx, bytes.NewReader(arcBytes), int64(len(arcBytes)), matchByName("books/target.epub"), 64<<20, 64<<20)
	if err != nil {
		t.Fatalf("plan7zMember: %v", err)
	}
	corrupted := bytes.Clone(arcBytes)
	corrupted[plan.start+plan.packed/2] ^= 0xff
	rcBad, _, err := Open7zMember(ctx, memVols(corrupted), matchByName("books/target.epub"), 64<<20, 64<<20)
	if err == nil {
		_, err = io.ReadAll(rcBad)
		_ = rcBad.Close()
	}
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("corrupted 7z member error = %v, want ErrCorrupt", err)
	}
}

func TestStream7zSolidLZMAAndLZMA2Fixtures(t *testing.T) {
	ctx := context.Background()

	// Real 7zz solid LZMA2 fixture with two files: rand_b.bin (33333 B) and sub/rand_c.bin (5000 B).
	b := readFixture(t, "7z", "compressed.7z")
	vols := memVols(b)
	for _, tc := range []struct {
		name    string
		fixture string
	}{
		{name: "rand_b.bin", fixture: "rand_b.bin"},
		{name: "sub/rand_c.bin", fixture: "rand_c.bin"},
	} {
		solid, err := Inspect7zMember(ctx, vols, matchByName(tc.name), 64<<20, 64<<20)
		if err != nil {
			t.Fatalf("Inspect7zMember(%s): %v", tc.name, err)
		}
		if !solid {
			t.Fatalf("Inspect7zMember(%s) = solid false, want true for multi-file solid folder", tc.name)
		}
		rc, _, err := Open7zMember(ctx, vols, matchByName(tc.name), 64<<20, 64<<20)
		if err != nil {
			t.Fatalf("Open7zMember(%s): %v", tc.name, err)
		}
		got, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("ReadAll(%s): %v", tc.name, err)
		}
		want := readFixture(t, "7z", tc.fixture)
		if !bytes.Equal(got, want) {
			t.Fatalf("%s payload mismatch (%d vs %d)", tc.name, len(got), len(want))
		}
	}

	// Expanded-bytes cap rejects solid prefix + member exceeding maxExpandedBytes.
	if _, err := Inspect7zMember(ctx, vols, matchByName("sub/rand_c.bin"), 64<<20, 10_000); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Inspect7zMember over maxExpandedBytes: got %v, want ErrUnsupported", err)
	}

	// Both LZMA and LZMA2 solid fixtures in internal/plugins/testdata/archives.
	wantA := []byte(strings.Repeat("first member: abcdefghijklmnopqrstuvwxyz", 30))
	wantB := []byte(strings.Repeat("second member: 0123456789", 30))
	for _, arcName := range []string{"lzma.7z", "lzma2.7z"} {
		raw, err := os.ReadFile(filepath.Join("..", "plugins", "testdata", "archives", arcName))
		if err != nil {
			t.Fatalf("read %s: %v", arcName, err)
		}
		arcVols := memVols(raw)
		for name, want := range map[string][]byte{"a.txt": wantA, "b.txt": wantB} {
			rc, _, err := Open7zMember(ctx, arcVols, matchByName(name), 64<<20, 64<<20)
			if err != nil {
				t.Fatalf("%s Open7zMember(%s): %v", arcName, name, err)
			}
			got, err := io.ReadAll(rc)
			_ = rc.Close()
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("%s %s mismatch: err=%v got=%d want=%d", arcName, name, err, len(got), len(want))
			}
		}
	}
}

func TestStream7zRejectsEncryptedAndUnsupportedCoders(t *testing.T) {
	ctx := context.Background()

	// Content-encrypted 7z -> ErrEncrypted.
	encContent := memVols(readFixture(t, "7z", "enc_content.7z"))
	if _, err := Inspect7zMember(ctx, encContent, matchAny, 64<<20, 64<<20); !errors.Is(err, ErrEncrypted) {
		t.Fatalf("Inspect7zMember(enc_content.7z) = %v, want ErrEncrypted", err)
	}
	if _, _, err := Open7zMember(ctx, encContent, matchAny, 64<<20, 64<<20); !errors.Is(err, ErrEncrypted) {
		t.Fatalf("Open7zMember(enc_content.7z) = %v, want ErrEncrypted", err)
	}

	// Header-encrypted 7z -> ErrEncrypted.
	encHeader := memVols(readFixture(t, "7z", "enc_header.7z"))
	if _, err := Inspect7zMember(ctx, encHeader, matchAny, 64<<20, 64<<20); !errors.Is(err, ErrEncrypted) {
		t.Fatalf("Inspect7zMember(enc_header.7z) = %v, want ErrEncrypted", err)
	}

	// Multi-coder BCJ+LZMA filter chain -> ErrUnsupported.
	bcj := memVols(readFixture(t, "7z", "bcj_lzma.7z"))
	if _, err := Inspect7zMember(ctx, bcj, matchAny, 64<<20, 64<<20); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Inspect7zMember(bcj_lzma.7z) = %v, want ErrUnsupported", err)
	}
	if _, _, err := Open7zMember(ctx, bcj, matchAny, 64<<20, 64<<20); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Open7zMember(bcj_lzma.7z) = %v, want ErrUnsupported", err)
	}
}

func TestOpenRARMemberCompressedSolidMultiAndEncrypted(t *testing.T) {
	ctx := context.Background()

	// 1. Single compressed RAR5 member.
	compVols := altVols(t, "compressed.rar")
	rc, compMem, err := OpenRARMember(ctx, compVols, matchByName("release.nzb"), 64<<20, 16<<20)
	if err != nil {
		t.Fatalf("OpenRARMember(compressed.rar): %v", err)
	}
	gotComp, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil || int64(len(gotComp)) != 269 || compMem.Size != 269 || !bytes.Contains(gotComp, []byte("<nzb")) {
		t.Fatalf("compressed.rar read: err=%v len=%d mem=%+v", err, len(gotComp), compMem)
	}

	// 2. Solid RAR5 archive: second member requires decoding solid prefix.
	solidVols := altVols(t, "solid.rar")
	rcSolid, solidMem, err := OpenRARMember(ctx, solidVols, matchByName("release.nzb"), 64<<20, 16<<20)
	if err != nil {
		t.Fatalf("OpenRARMember(solid.rar[1]): %v", err)
	}
	gotSolid, err := io.ReadAll(rcSolid)
	_ = rcSolid.Close()
	if err != nil || int64(len(gotSolid)) != 270 || solidMem.Size != 270 || !bytes.Contains(gotSolid, []byte("<nzb")) {
		t.Fatalf("solid.rar[1] read: err=%v len=%d mem=%+v", err, len(gotSolid), solidMem)
	}
	// Solid prefix + target exceeds tight maxExpandedBytes -> ErrUnsupported.
	rcTight, _, err := OpenRARMember(ctx, solidVols, matchByName("release.nzb"), 64<<20, 600)
	if err == nil {
		_, err = io.ReadAll(rcTight)
		_ = rcTight.Close()
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("solid.rar over maxExpandedBytes = %v, want ErrUnsupported", err)
	}

	// 3. Multi-volume compressed RAR5 (3 parts).
	multiVols := altVols(t, "multi.part1.rar", "multi.part2.rar", "multi.part3.rar")
	rcMulti, multiMem, err := OpenRARMember(ctx, multiVols, matchByName("release.nzb"), 64<<20, 16<<20)
	if err != nil {
		t.Fatalf("OpenRARMember(multi): %v", err)
	}
	gotMulti, err := io.ReadAll(rcMulti)
	_ = rcMulti.Close()
	if err != nil || int64(len(gotMulti)) != 10945 || multiMem.Size != 10945 {
		t.Fatalf("multi RAR read: err=%v len=%d mem=%+v", err, len(gotMulti), multiMem)
	}

	// 4. Non-solid RAR4 and RAR5 seek past preceding member payloads without reading them.
	for _, ver := range []int{4, 5} {
		firstPayload := randBytes(uint64(200+ver), 128*1024)
		secondPayload := bytes.Repeat([]byte("rar-second-member-"), 200)
		built := buildRAR(t, at.RAROptions{Version: ver}, []at.RARFile{
			{Name: "01_big.bin", Data: firstPayload},
			{Name: "02_target.txt", Data: secondPayload},
		})
		rr := &recReader{r: bytes.NewReader(built[0])}
		vols := []Volume{{R: rr, Size: int64(len(built[0]))}}
		ms, err := ListRAR(vols)
		if err != nil {
			t.Fatalf("RARv%d ListRAR: %v", ver, err)
		}
		rr.reads = nil
		rcVer, _, err := OpenRARMember(ctx, vols, matchByName("02_target.txt"), 64<<20, 16<<20)
		if err != nil {
			t.Fatalf("RARv%d OpenRARMember: %v", ver, err)
		}
		gotVer, err := io.ReadAll(rcVer)
		_ = rcVer.Close()
		if err != nil || !bytes.Equal(gotVer, secondPayload) {
			t.Fatalf("RARv%d payload mismatch: err=%v", ver, err)
		}
		// Initial 32 KiB header buffer covers [0, 32768); seeking past 01_big.bin must skip [40960, end-4096).
		rr.assertNoDataReads(t, 40*1024, ms[0].Extents[0].Size-48*1024, "01_big.bin")
	}

	// 5. Encrypted RAR4 and RAR5 fixtures (file-encrypted and header-encrypted) -> ErrEncrypted.
	for _, encFixture := range [][]string{
		{"altmount", "encrypted.rar"},
		{"rar-encrypted", "rar5-psw.rar"},
		{"rar-encrypted", "rar5-hpsw.rar"},
		{"rar-encrypted", "rar4-headersfalse-multifalse.part1.rar"},
		{"rar-encrypted", "rar4-headerstrue-multifalse.part1.rar"},
	} {
		raw := readFixture(t, encFixture[0], encFixture[1])
		vols := memVols(raw)
		rcEnc, _, err := OpenRARMember(ctx, vols, matchAny, 64<<20, 16<<20)
		if err == nil {
			_, err = io.ReadAll(rcEnc)
			_ = rcEnc.Close()
		}
		if !errors.Is(err, ErrEncrypted) {
			t.Fatalf("OpenRARMember(%s/%s) = %v, want ErrEncrypted", encFixture[0], encFixture[1], err)
		}
	}

	// 6. Corrupt compressed payload -> ErrCorrupt.
	badComp := bytes.Clone(readFixture(t, "altmount", "compressed.rar"))
	badComp[len(badComp)-32] ^= 0xff
	rcCorrupt, _, err := OpenRARMember(ctx, memVols(badComp), matchAny, 64<<20, 16<<20)
	if err == nil {
		_, err = io.ReadAll(rcCorrupt)
		_ = rcCorrupt.Close()
	}
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("corrupted compressed.rar = %v, want ErrCorrupt", err)
	}
}

func TestArchiveBackslashMemberLookupAgrees(t *testing.T) {
	ctx := context.Background()
	const (
		storedName = `PlayStation\Game (123).json`
		payload    = `{"id":123,"title":"Game"}`
	)

	checkOpen := func(t *testing.T, label string, open func(match func(int, Member) bool) (io.ReadCloser, Member, error), wantName string, wantIdx int) {
		t.Helper()
		for _, tc := range []struct {
			mode  string
			match func(int, Member) bool
		}{
			{"by-name", matchByName(wantName)},
			{"by-index", func(idx int, _ Member) bool { return idx == wantIdx }},
		} {
			rc, m, err := open(tc.match)
			if err != nil {
				t.Fatalf("%s %s open: %v", label, tc.mode, err)
			}
			got, err := io.ReadAll(rc)
			_ = rc.Close()
			if err != nil {
				t.Fatalf("%s %s read: %v", label, tc.mode, err)
			}
			if m.Name != wantName || string(got) != payload {
				t.Fatalf("%s %s = name=%q body=%q, want name=%q body=%q", label, tc.mode, m.Name, got, wantName, payload)
			}
		}
	}

	t.Run("zip", func(t *testing.T) {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w0, err := zw.CreateHeader(&zip.FileHeader{Name: "readme.txt", Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w0, "lead")
		w1, err := zw.CreateHeader(&zip.FileHeader{Name: storedName, Method: zip.Deflate})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w1, payload)
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		vols := memVols(buf.Bytes())
		members, err := ListZIP(vols)
		if err != nil {
			t.Fatal(err)
		}
		checkOpen(t, "zip", func(m func(int, Member) bool) (io.ReadCloser, Member, error) {
			return OpenZIPMember(vols, m)
		}, members[1].Name, 1)
	})

	t.Run("7z", func(t *testing.T) {
		raw := at.Build7zCopy([]at.SevenZipFile{
			{Name: "readme.txt", Data: []byte("lead")},
			{Name: storedName, Data: []byte(payload)},
		}, false)
		vols := memVols(raw)
		members, err := List7z(bytes.NewReader(raw), int64(len(raw)))
		if err != nil {
			t.Fatal(err)
		}
		checkOpen(t, "7z", func(m func(int, Member) bool) (io.ReadCloser, Member, error) {
			return Open7zMember(ctx, vols, m, 64<<20, 16<<20)
		}, members[1].Name, 1)
	})

	for _, version := range []int{4, 5} {
		label := "rar4"
		if version == 5 {
			label = "rar5"
		}
		t.Run(label, func(t *testing.T) {
			rawVols, err := at.BuildRAR([]at.RARFile{
				{Name: "readme.txt", Data: []byte("lead")},
				{Name: storedName, Data: []byte(payload)},
			}, at.RAROptions{Version: version})
			if err != nil {
				t.Fatal(err)
			}
			v, err := ReadRARVolume(bytes.NewReader(rawVols[0]), int64(len(rawVols[0])), &RAROptions{Context: ctx})
			if err != nil {
				t.Fatal(err)
			}
			if v.Files[1].StoredName != storedName || !v.Files[1].UnsafeName {
				t.Fatalf("%s StoredName=%q UnsafeName=%v", label, v.Files[1].StoredName, v.Files[1].UnsafeName)
			}
			members, err := ComposeRAR([]*RARVolume{v})
			if err != nil {
				t.Fatal(err)
			}
			vols := memVols(rawVols[0])
			checkOpen(t, label, func(m func(int, Member) bool) (io.ReadCloser, Member, error) {
				return OpenRARMember(ctx, vols, m, 64<<20, 16<<20)
			}, members[1].Name, 1)
		})
	}
}
