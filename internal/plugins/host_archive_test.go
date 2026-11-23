package plugins

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/archive/archivetest"
)

// These invariants cover format-independent wire ranges, solid dependencies,
// header-only enumeration, and refusal before allocating/decompressing input.
func TestDataArchiveFormats(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{"lzma.7z", "lzma2.7z", "store4.rar", "store5.rar"} {
		t.Run(name, func(t *testing.T) {
			b, e := os.ReadFile("testdata/archives/" + name)
			if e != nil {
				t.Fatal(e)
			}
			h := &dataTestHost{data: b}
			raw, e := CallDataHost(ctx, h, "archive.list", json.RawMessage(`["blob:test"]`))
			if e != nil {
				t.Fatal(e)
			}
			var members []ArchiveMember
			if e = json.Unmarshal(raw, &members); e != nil || len(members) != 2 {
				t.Fatalf("list %s: %v", raw, e)
			}
			for i, m := range members {
				if m.Index != i || m.Name != fmt.Sprintf("%c.txt", 'a'+i) {
					t.Fatalf("members: %+v", members)
				}
				expected := []string{"first member: abcdefghijklmnopqrstuvwxyz", "second member: 0123456789"}[i]
				if strings.HasSuffix(name, ".7z") {
					expected = strings.Repeat(expected, 30)
				}
				for _, r := range []DataRange{{7, 13}, {int64(len(expected) - 4), 20}, {int64(len(expected)), 8}} {
					arg, _ := json.Marshal([]any{"blob:test", i, r})
					raw, e = CallDataHost(ctx, h, "archive.open", arg)
					if e != nil {
						t.Fatal(e)
					}
					var out struct {
						BodyBase64 string
						Size       int
					}
					if e = json.Unmarshal(raw, &out); e != nil {
						t.Fatal(e)
					}
					got, e := base64.StdEncoding.DecodeString(out.BodyBase64)
					if e != nil {
						t.Fatal(e)
					}
					want := expected[r.Offset:min(int64(len(expected)), r.Offset+r.Length)]
					if string(got) != want || out.Size != len(want) {
						t.Fatalf("range %+v: %q != %q", r, got, want)
					}
				}
			}
			if h.opens != h.closes {
				t.Fatal("cache reader leaked")
			}
			for _, limit := range []RuntimeLimits{
				{MemoryBytes: 1 << 19}, {ArchiveMembers: 1}, {ArchiveMemberBytes: 8}, {ArchiveExpandedBytes: 8}, {FetchBytes: 16},
			} {
				l := defaultDataLimits()
				if limit.MemoryBytes > 0 {
					l.MemoryBytes = limit.MemoryBytes
				}
				if limit.ArchiveMembers > 0 {
					l.ArchiveMembers = limit.ArchiveMembers
				}
				if limit.ArchiveMemberBytes > 0 {
					l.ArchiveMemberBytes = limit.ArchiveMemberBytes
				}
				if limit.ArchiveExpandedBytes > 0 {
					l.ArchiveExpandedBytes = limit.ArchiveExpandedBytes
				}
				if limit.FetchBytes > 0 {
					l.FetchBytes = limit.FetchBytes
				}
				if _, e = openDataArchive(ctx, bytes.NewReader(b), l); e == nil {
					t.Fatalf("grant ignored: %+v", limit)
				}
			}
		})
	}
	for _, version := range []int{4, 5} {
		for _, opts := range []archivetest.RAROptions{{Version: version, HeaderEncrypted: true}, {Version: version, VolumeSize: 128}} {
			b, e := archivetest.BuildRAR([]archivetest.RARFile{{Name: "a.txt", Data: bytes.Repeat([]byte{1}, 300)}}, opts)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = openDataArchive(ctx, bytes.NewReader(b[0]), defaultDataLimits()); e == nil {
				t.Fatalf("unsafe RAR options accepted: %+v", opts)
			}
		}
		for _, file := range []archivetest.RARFile{{Name: "a", Encrypted: true, Data: []byte("ok")}, {Name: "a", Data: []byte("x"), UnknownSize: true}} {
			b, e := archivetest.BuildRAR([]archivetest.RARFile{file}, archivetest.RAROptions{Version: version})
			if e != nil {
				t.Fatal(e)
			}
			if _, e = openDataArchive(ctx, bytes.NewReader(b[0]), defaultDataLimits()); e == nil {
				t.Fatalf("unsafe RAR member accepted: %+v", file)
			}
		}
	}
	for _, name := range []string{"enc_header.7z", "enc_content.7z", "store_vol.7z.001"} {
		b, e := os.ReadFile("../archive/testdata/7z/" + name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = openDataArchive(ctx, bytes.NewReader(b), defaultDataLimits()); e == nil {
			t.Fatalf("unsafe 7z %s accepted", name)
		}
	}
	// Correctly checksummed metadata must still refuse special files and bombs.
	for _, attribute := range []uint32{0xa0000000, 0x400} {
		b := archivetest.Build7zCopy([]archivetest.SevenZipFile{{Name: "link", Data: []byte("x")}}, false)
		off := 32 + int(binary.LittleEndian.Uint64(b[12:]))
		hdr := append([]byte(nil), b[off:len(b)-2]...)
		hdr = append(hdr, 0x15, 6, 1, 0)
		hdr = binary.LittleEndian.AppendUint32(hdr, attribute)
		hdr = append(hdr, 0, 0)
		b = append(b[:off], hdr...)
		binary.LittleEndian.PutUint64(b[20:], uint64(len(hdr)))
		binary.LittleEndian.PutUint32(b[28:], crc32.ChecksumIEEE(hdr))
		binary.LittleEndian.PutUint32(b[8:], crc32.ChecksumIEEE(b[12:32]))
		if _, e := openDataArchive(ctx, bytes.NewReader(b), defaultDataLimits()); e == nil {
			t.Fatal("special 7z member accepted")
		}
	}
	for _, version := range []int{4, 5} {
		for _, special := range []bool{false, true} {
			h := archivetest.Header{Name: "a", PackSize: 1, UnpackSize: 100001, Method: 1}
			var b []byte
			if version == 4 {
				b = append([]byte("Rar!\x1a\x07\x00"), archivetest.RAR4MainHeader(0)...)
				header := archivetest.RAR4FileHeader(h)
				if special {
					binary.LittleEndian.PutUint32(header[11:], 1)
					header[15] = 3
					binary.LittleEndian.PutUint32(header[28:], 0xa000)
					binary.LittleEndian.PutUint16(header, uint16(crc32.ChecksumIEEE(header[2:])))
				}
				b = append(b, header...)
				b = append(b, 'x')
				b = append(b, archivetest.RAR4EndHeader(false, -1)...)
			} else {
				b = append([]byte("Rar!\x1a\x07\x01\x00"), archivetest.RAR5MainHeader(false, 0, false)...)
				if special {
					// Flags, size, Unix symlink attributes, compression info, Unix host, name.
					fields := []byte{0, 1}
					fields = append(fields, archivetest.Vint(0xa000)...)
					fields = append(fields, 0, 1, 1, 'a')
					b = append(b, archivetest.RAR5Block(2, 0, fields, nil, true, 1)...)
				} else {
					b = append(b, archivetest.RAR5FileHeader(h)...)
				}
				b = append(b, 'x')
				b = append(b, archivetest.RAR5EndHeader(false)...)
			}
			if _, e := openDataArchive(ctx, bytes.NewReader(b), defaultDataLimits()); e == nil {
				t.Fatalf("unsafe RAR%d accepted (special=%v)", version, special)
			}
		}
	}

	// Existing generated compressed/solid RARs exercise the real decoder too.
	for _, name := range []string{"compressed.rar", "solid.rar"} {
		b, e := os.ReadFile("../archive/testdata/altmount/" + name)
		if e != nil {
			t.Fatal(e)
		}
		a, e := openDataArchive(ctx, bytes.NewReader(b), defaultDataLimits())
		if e != nil {
			t.Fatal(e)
		}
		for i, m := range a.members {
			if m.Size == 0 {
				continue
			}
			r, e := a.open(i)
			if e != nil {
				t.Fatal(e)
			}
			_, e = io.Copy(io.Discard, r)
			r.Close()
			if e != nil {
				t.Fatal(e)
			}
		}
	}
}

// Unsafe name flags survive directory caching as informational warnings, and
// every listed non-directory member (including backslash and traversal names)
// remains readable by index across ZIP, solid 7z, and RAR4/5.
func TestDataArchiveMemberNames(t *testing.T) {
	names := []string{"normal.txt", "../escape", "Some Game (Europe: Special Edition).txt", "after.txt", "..", "/absolute", `dir\escape`, "dir/../escape", "C:/absolute"}
	for _, format := range []string{"zip", "7z", "rar4", "rar5"} {
		t.Run(format, func(t *testing.T) {
			var data []byte
			switch format {
			case "zip":
				var b bytes.Buffer
				z := zip.NewWriter(&b)
				for _, name := range names {
					w, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
					if err != nil {
						t.Fatal(err)
					}
					if _, err = io.WriteString(w, "contents"); err != nil {
						t.Fatal(err)
					}
				}
				if err := z.Close(); err != nil {
					t.Fatal(err)
				}
				data = b.Bytes()
			case "7z":
				var files []archivetest.SevenZipFile
				for _, name := range names {
					files = append(files, archivetest.SevenZipFile{Name: name, Data: []byte("contents")})
				}
				data = archivetest.Build7zCopy(files, true)
			default:
				var files []archivetest.RARFile
				for _, name := range names {
					files = append(files, archivetest.RARFile{Name: name, Data: []byte("contents")})
				}
				version := 4
				if format == "rar5" {
					version = 5
				}
				volumes, err := archivetest.BuildRAR(files, archivetest.RAROptions{Version: version})
				if err != nil {
					t.Fatal(err)
				}
				data = volumes[0]
			}
			h := &directoryCacheTestHost{data: data, directoryOffset: -1}
			ctx := context.Background()
			for pass := 0; pass < 2; pass++ {
				raw, err := CallDataHost(ctx, h, "archive.list", json.RawMessage(`["blob:test"]`))
				if err != nil {
					t.Fatal(err)
				}
				var members []ArchiveMember
				if err = json.Unmarshal(raw, &members); err != nil || len(members) != len(names) {
					t.Fatalf("list: %s, %v", raw, err)
				}
				batch := make([]ArchiveReadItem, 0, len(members))
				for i, m := range members {
					unsafe := i == 1 || i >= 4
					if m.Index != i || m.Name != names[i] || m.Unsafe != unsafe || (m.UnsafeReason != "") != unsafe {
						t.Fatalf("member: %+v", m)
					}
					args, _ := json.Marshal([]any{"blob:test", i, DataRange{Offset: 0, Length: 100}})
					raw, err = CallDataHost(ctx, h, "archive.open", args)
					if err != nil {
						t.Fatalf("archive.open(%d %q): %v", i, names[i], err)
					}
					var out struct{ BodyBase64 string }
					if err = json.Unmarshal(raw, &out); err != nil {
						t.Fatal(err)
					}
					got, err := base64.StdEncoding.DecodeString(out.BodyBase64)
					if err != nil || string(got) != "contents" {
						t.Fatalf("open(%d %q): %q, %v", i, names[i], got, err)
					}
					batch = append(batch, ArchiveReadItem{Member: i, Offset: 0, Length: 100})
				}
				batchArgs, _ := json.Marshal([]any{"blob:test", batch})
				rawBatch, err := CallDataHost(ctx, h, "archive.read", batchArgs)
				if err != nil {
					t.Fatalf("archive.read batch: %v", err)
				}
				var results []ArchiveReadResult
				if err = json.Unmarshal(rawBatch, &results); err != nil || len(results) != len(names) {
					t.Fatalf("archive.read results: %s, %v", rawBatch, err)
				}
				for i, r := range results {
					got, err := base64.StdEncoding.DecodeString(r.BodyBase64)
					if err != nil || r.Member != i || string(got) != "contents" {
						t.Fatalf("batch[%d]: %+v (%q, %v)", i, r, got, err)
					}
				}
				// Frontend streaming by index also accepts backslash-named members.
				fr, fsize, err := OpenFrontendArchiveMember(ctx, bytes.NewReader(data), 6, defaultDataLimits())
				if err != nil {
					t.Fatalf("OpenFrontendArchiveMember(6): %v", err)
				}
				fdata, err := io.ReadAll(fr)
				_ = fr.Close()
				if err != nil || fsize != int64(len("contents")) || string(fdata) != "contents" {
					t.Fatalf("frontend member 6: size=%d data=%q err=%v", fsize, fdata, err)
				}
			}
			if h.cache.lru.Len() != 1 {
				t.Fatal("directory was not cached")
			}
		})
	}
}
