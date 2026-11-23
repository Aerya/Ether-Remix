package veladist

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func hash(b []byte) string { d := sha256.Sum256(b); return hex.EncodeToString(d[:]) }
func descriptor() Release {
	b := false
	z := 0
	r := Release{SchemaVersion: 1, Kind: "vela-release", Channel: "stable", Version: "0.1.42", PublishedAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(time.Hour), SourceRevision: "revision", SourceDirty: &b, MinimumEtherVersion: "0.3.12", Archive: Asset{Bytes: 1, SHA256: hash([]byte("x"))}, Manifest: Asset{Bytes: 1, SHA256: hash([]byte("x"))}}
	r.Protocol.MinInclusive = &z
	r.Protocol.MaxInclusive = &z
	return r
}
func tarball(t *testing.T, headers []*tar.Header, contents []string) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	for i, h := range headers {
		if e := tw.WriteHeader(h); e != nil {
			t.Fatal(e)
		}
		if h.Typeflag == tar.TypeReg {
			if _, e := tw.Write([]byte(contents[i])); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e := tw.Close(); e != nil {
		t.Fatal(e)
	}
	if e := gz.Close(); e != nil {
		t.Fatal(e)
	}
	return out.Bytes()
}
func write(t *testing.T, dir, p, s string) {
	t.Helper()
	p = filepath.Join(dir, p)
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(s), 0644); e != nil {
		t.Fatal(e)
	}
}
func TestDistribution(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	t.Run("descriptor", func(t *testing.T) {
		for _, name := range []string{"good", "bad-signature", "expired", "dirty", "protocol", "downgrade", "rotation"} {
			t.Run(name, func(t *testing.T) {
				r := descriptor()
				installed := "0.1.41"
				keys := []ed25519.PublicKey{pub}
				switch name {
				case "expired":
					r.ExpiresAt = time.Now().Add(-time.Minute)
				case "dirty":
					b := true
					r.SourceDirty = &b
				case "protocol":
					n := 1
					r.Protocol.MinInclusive = &n
				case "downgrade":
					installed = "0.1.43"
				case "rotation":
					other, _, _ := ed25519.GenerateKey(rand.Reader)
					keys = []ed25519.PublicKey{other, pub}
				}
				raw, _ := json.Marshal(r)
				sig := ed25519.Sign(priv, raw)
				if name == "bad-signature" {
					raw = append(raw, ' ')
				}
				_, e := Verify(raw, []byte(base64.StdEncoding.EncodeToString(sig)), keys, "0.3.13", installed, time.Now())
				want := name == "good" || name == "rotation"
				if (e == nil) != want {
					t.Fatalf("valid=%v error=%v", want, e)
				}
			})
		}
	})
	t.Run("tar-safety", func(t *testing.T) {
		for _, h := range []*tar.Header{{Name: "../escape", Typeflag: tar.TypeReg}, {Name: "/absolute", Typeflag: tar.TypeReg}, {Name: "link", Typeflag: tar.TypeSymlink, Linkname: "../../escape"}, {Name: "hard", Typeflag: tar.TypeLink, Linkname: "../../escape"}, {Name: "device", Typeflag: tar.TypeChar}, {Name: "large", Typeflag: tar.TypeReg, Size: 2}} {
			t.Run(h.Name, func(t *testing.T) {
				data := tarball(t, []*tar.Header{h}, []string{strings.Repeat("x", int(h.Size))})
				if e := extract(context.Background(), bytes.NewReader(data), t.TempDir(), 1); e == nil {
					t.Fatal("unsafe tar accepted")
				}
			})
		}
	})
	t.Run("install-transaction", func(t *testing.T) {
		assets := map[string][]byte{}
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, ok := assets[r.URL.Path]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(b)
		}))
		defer srv.Close()
		root := t.TempDir()
		m := New(root, "0.3.13", func() string { return srv.URL + "/channel.json" })
		m.client = srv.Client()
		m.keys = []ed25519.PublicKey{pub}
		write(t, m.live(), "index.html", "old")
		write(t, m.live(), "app.version.json", `{"version":"0.1.41"}`)
		for _, p := range []string{"diagnostic-core-old.js", "core-app-old.html", "universal/key/module.js", "engines/cinematic/old/app.js"} {
			write(t, m.live(), p, "owner")
		}
		publish := func(mismatch bool) {
			content := "new"
			if mismatch {
				content = "bad"
			}
			assets["/archive"] = tarball(t, []*tar.Header{{Name: "./index.html", Typeflag: tar.TypeReg, Size: 3}, {Name: "./root-build.json", Typeflag: tar.TypeReg, Size: 4}}, []string{content, "ship"})
			inv := inventory{SchemaVersion: 1, Version: "0.1.42", Files: []File{{Path: "index.html", Bytes: 3, SHA256: hash([]byte("new"))}, {Path: "root-build.json", Bytes: 4, SHA256: hash([]byte("ship"))}}}
			assets["/manifest"], _ = json.Marshal(inv)
			r := descriptor()
			r.Archive = Asset{URL: srv.URL + "/archive", Bytes: int64(len(assets["/archive"])), SHA256: hash(assets["/archive"])}
			r.Manifest = Asset{URL: srv.URL + "/manifest", Bytes: int64(len(assets["/manifest"])), SHA256: hash(assets["/manifest"])}
			assets["/channel.json"], _ = json.Marshal(r)
			assets["/channel.json.sig"] = []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, assets["/channel.json"])))
		}
		publish(true)
		if e := m.Check(context.Background()); e != nil {
			t.Fatal(e)
		}
		if e := m.Install(context.Background()); e == nil {
			t.Fatal("mismatch installed")
		}
		b, _ := os.ReadFile(filepath.Join(m.live(), "index.html"))
		if string(b) != "old" || len(m.rollbacks()) != 0 {
			t.Fatal("mismatch disturbed live build")
		}
		publish(false)
		if e := m.Check(context.Background()); e != nil {
			t.Fatal(e)
		}
		if e := m.Install(context.Background()); e != nil {
			t.Fatal(e)
		}
		if m.Status().InstalledVersion != "0.1.42" {
			t.Fatal(m.Status())
		}
		for _, p := range []string{"diagnostic-core-old.js", "core-app-old.html", "universal/key/module.js", "engines/cinematic/old/app.js"} {
			b, e := os.ReadFile(filepath.Join(m.live(), p))
			if e != nil || string(b) != "owner" {
				t.Fatalf("additive file %s lost: %v", p, e)
			}
		}
		// These were staged after the previous build became a rollback.
		additions := []string{
			"universal/18273ed7/module.js", "universal/f68fb203/module.js", "CORE-STREAMING-owner.json", "CORE-RESERVE-owner.json",
			"loader-2f530de1c195c7547d26.js",
			"_expo/static/js/web/[id]-437b3b9580e47379267d250fdf53a500.js",
			"assets/fonts/inter.017bc6ba3fc25503e5eb5e53826d48a8.ttf",
			"assets/icons/close.808e1b1b9b53114ec2838071a7e6daa7@2x.png",
			"player/" + strings.Repeat("a", 64) + "/libraries/decoder.wasm",
			"player/" + strings.Repeat("a", 64) + "/capabilities.json",
			"skins/cinematic/1.0.0/font.ttf",
			"skins/assets/" + strings.Repeat("b", 64) + ".svg",
		}
		for _, p := range additions {
			write(t, m.live(), p, "late-owner")
		}
		// Existing asset directories must merge rather than suppress new chunks.
		write(t, m.rollbacks()[0], "_expo/static/js/web/existing.js", "previous-chunk")
		mutable := []string{"sw.js", "_expo/static/runtime.json", "assets/config.json", "skins/catalog.json", "player/current/capabilities.json"}
		for _, p := range mutable {
			write(t, m.live(), p, "outgoing-mutable")
		}
		write(t, m.rollbacks()[0], "CORE-existing.json", "previous-owner")
		write(t, m.live(), "CORE-existing.json", "outgoing-owner")
		if e := m.Rollback(context.Background()); e != nil {
			t.Fatal(e)
		}
		b, _ = os.ReadFile(filepath.Join(m.live(), "index.html"))
		if string(b) != "old" {
			t.Fatal("rollback did not restore old build")
		}
		for _, p := range additions {
			b, e := os.ReadFile(filepath.Join(m.live(), p))
			if e != nil || string(b) != "late-owner" {
				t.Fatalf("rollback lost %s: %v", p, e)
			}
		}
		for _, p := range mutable {
			if _, e := os.Stat(filepath.Join(m.live(), p)); !os.IsNotExist(e) {
				t.Fatalf("rollback carried mutable entry %s", p)
			}
		}
		b, _ = os.ReadFile(filepath.Join(m.live(), "CORE-existing.json"))
		if string(b) != "previous-owner" {
			t.Fatal("rollback overwrote an existing additive item")
		}
		if _, e := os.Stat(filepath.Join(m.live(), "root-build.json")); !os.IsNotExist(e) {
			t.Fatal("rollback copied a shipped JSON file absent from the older build")
		}
		if m.Status().InstalledVersion != "0.1.41" {
			t.Fatal("rollback copied outgoing release metadata")
		}
		if e := m.Check(context.Background()); e != nil {
			t.Fatal(e)
		}
		if e := m.Install(context.Background()); e != nil {
			t.Fatal(e)
		}
		for _, p := range additions {
			b, e := os.ReadFile(filepath.Join(m.live(), p))
			if e != nil || string(b) != "late-owner" {
				t.Fatalf("reinstall lost %s: %v", p, e)
			}
		}
	})
	t.Run("immutable-asset-boundaries", func(t *testing.T) {
		if err := preserve(context.Background(), filepath.Join(t.TempDir(), "not-installed"), t.TempDir()); err != nil {
			t.Fatalf("first installation failed: %v", err)
		}
		asset := "_expo/static/js/web/chunk-437b3b9580e47379267d250fdf53a500.js"
		for _, scenario := range []string{"source-link", "destination-link", "collision", "matching", "cancelled"} {
			t.Run(scenario, func(t *testing.T) {
				old, stage, outside := t.TempDir(), t.TempDir(), t.TempDir()
				write(t, old, asset, "chunk")
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				switch scenario {
				case "source-link":
					if err := os.Symlink(outside, filepath.Join(old, "assets")); err != nil {
						t.Fatal(err)
					}
				case "destination-link":
					if err := os.Symlink(outside, filepath.Join(stage, "_expo")); err != nil {
						t.Fatal(err)
					}
				case "collision":
					write(t, stage, asset, "other")
				case "matching":
					write(t, stage, asset, "chunk")
				case "cancelled":
					cancel()
				}
				err := preserve(ctx, old, stage)
				if (scenario == "matching") != (err == nil) {
					t.Fatalf("preservation result: %v", err)
				}
				if scenario == "collision" {
					b, _ := os.ReadFile(filepath.Join(stage, asset))
					if string(b) != "other" {
						t.Fatal("collision overwrote candidate asset")
					}
				}
				entries, err := os.ReadDir(outside)
				if err != nil || len(entries) != 0 {
					t.Fatal("preservation wrote outside its destination")
				}
			})
		}
	})
	t.Run("rollback-retention", func(t *testing.T) {
		for _, future := range []bool{false, true} {
			t.Run(fmt.Sprint(future), func(t *testing.T) {
				m := New(t.TempDir(), "0.3.13", nil)
				legacy := filepath.Join(m.dir, ".vela-rollback-20261115T074837")
				epoch := filepath.Join(m.dir, ".vela-rollback-01794760000000000000")
				write(t, legacy, "index.html", "legacy")
				write(t, epoch, "index.html", "epoch")
				old := time.Now().Add(-48 * time.Hour)
				newer := old.Add(time.Hour)
				if e := os.Chtimes(legacy, old, old); e != nil {
					t.Fatal(e)
				}
				if e := os.Chtimes(epoch, newer, newer); e != nil {
					t.Fatal(e)
				}
				if got := m.rollbacks(); len(got) != 2 || got[0] != epoch {
					t.Fatalf("name ordering overrode rollback time: %v", got)
				}
				if future {
					stamp := time.Now().Add(24 * time.Hour)
					if e := os.Chtimes(legacy, stamp, stamp); e != nil {
						t.Fatal(e)
					}
				}
				write(t, m.live(), "index.html", "outgoing")
				if e := os.Chtimes(m.live(), old, old); e != nil {
					t.Fatal(e)
				}
				stage := filepath.Join(m.dir, ".vela-staging-test")
				write(t, stage, "index.html", "new")
				if e := m.swap(stage); e != nil {
					t.Fatal(e)
				}
				kept := m.rollbacks()
				if len(kept) != 2 {
					t.Fatalf("retained %d rollbacks", len(kept))
				}
				outgoing := false
				for _, p := range kept {
					b, _ := os.ReadFile(filepath.Join(p, "index.html"))
					if string(b) == "outgoing" {
						outgoing = true
					}
				}
				if !outgoing {
					t.Fatal("newly created rollback was pruned")
				}
				if !future {
					b, _ := os.ReadFile(filepath.Join(kept[0], "index.html"))
					if string(b) != "outgoing" {
						t.Fatal("new rollback did not receive its creation time")
					}
					if _, e := os.Stat(legacy); !os.IsNotExist(e) {
						t.Fatal("old legacy rollback retained instead of newer epoch rollback")
					}
				}
			})
		}
	})

	t.Run("local-file-path", func(t *testing.T) {
		if got := localFilePath("/C:/Users/runner/release.json", "windows"); got != `C:\Users\runner\release.json` {
			t.Fatalf("windows drive path = %q", got)
		}
		if got := localFilePath("/var/lib/ether/release.json", "linux"); got != "/var/lib/ether/release.json" {
			t.Fatalf("unix path = %q", got)
		}
	})
}
