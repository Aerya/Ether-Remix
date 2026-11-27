package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/braedonsaunders/ether/internal/config"
)

const packPrefix = "cache/vault/packs/"
const maxPackSize = 64 << 20
const packIndex = "vault-index.json"
const packOverhead = 28 // AES-GCM tag and random nonce
const packDigest = "ether.vault.sha256"

var packName = regexp.MustCompile("^[0-9]{6}/[0-9a-f]{32}\\.pack$")
var errMissingVaultPack = errors.New("incomplete backup bundle")

func packRoot(conf []byte, dataDir string) (string, error) {
	var c config.Config
	if err := json.Unmarshal(conf, &c); err != nil {
		return "", err
	}
	dir := c.EffectiveCache().Dir
	if dir == "" {
		dir = filepath.Join(dataDir, "cache")
	}
	return filepath.Join(dir, "vault", "packs"), nil
}

func copyPack(src io.Reader, dst string, size int64, space *stageSpace) (string, error) {
	if e := space.check(size); e != nil {
		return "", e
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return "", err
	}
	f, e := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return "", e
	}
	h := sha256.New()
	_, e = io.CopyN(io.MultiWriter(space.writer(f), h), src, size)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), ce
}

func readManifest(tr *tar.Reader) (Manifest, error) {
	var m Manifest
	h, err := tr.Next()
	if err != nil {
		return m, err
	}
	err = decodeManifest(h, tr, &m)
	return m, err
}

func decodeManifest(h *tar.Header, r io.Reader, m *Manifest) error {
	if h.Name != "manifest.json" || h.Typeflag != tar.TypeReg || h.Size > 16<<20 {
		return errors.New("invalid snapshot manifest")
	}
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(m); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("invalid snapshot manifest")
	}
	if m.VaultPackCount < 0 {
		return errors.New("invalid snapshot manifest")
	}
	return nil
}

func writeManifest(tw *tar.Writer, h *tar.Header, m Manifest) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	h.Size = int64(len(raw))
	if err := tw.WriteHeader(h); err != nil {
		return err
	}
	_, err = tw.Write(raw)
	return err
}

// A companion contains ciphertext only. Its random key stays in the
// local archive's vault index alongside the database manifest. Reusing the ciphertext by hard link
// does not give another backup (or a copied pack alone) access to the record.
type packRef struct {
	Name   string `json:"name"`
	Digest string `json:"sha256"`
	Key    []byte `json:"key"`
	Size   int64  `json:"size"`
}

func packCipher(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}
func sealPack(f *os.File, dst string, size int64, space *stageSpace) (packRef, error) {
	ref := packRef{Size: size, Key: make([]byte, 32)}
	if _, err := rand.Read(ref.Key); err != nil {
		return ref, err
	}
	a, err := packCipher(ref.Key)
	if err != nil {
		return ref, err
	}
	b := make([]byte, size, size+int64(a.Overhead()))
	if _, err = io.ReadFull(f, b); err != nil {
		return ref, err
	}
	digest := sha256.Sum256(b)
	ref.Digest = hex.EncodeToString(digest[:])
	sealed := a.Seal(b[:0], nil, b, nil)
	_, err = copyPack(bytes.NewReader(sealed), dst, int64(len(sealed)), space)
	return ref, err
}
func unsealPack(path string, ref packRef) ([]byte, error) {
	if ref.Size < 0 || ref.Size > maxPackSize {
		return nil, errors.New("invalid vault pack size")
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errMissingVaultPack
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	a, err := packCipher(ref.Key)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() != ref.Size+int64(a.Overhead()) {
		return nil, errors.New("invalid sealed pack size")
	}
	b := make([]byte, st.Size())
	if _, err := io.ReadFull(f, b); err != nil {
		return nil, err
	}
	plain, err := a.Open(b[:0], nil, b, nil)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(plain)
	if hex.EncodeToString(digest[:]) != ref.Digest {
		return nil, errors.New("vault checksum mismatch")
	}
	return plain, nil
}

// walkPackIndex streams one reference at a time. The local index contains
// keys and is never included in portable backups.
func walkPackIndex(r io.Reader, count int, visit func(packRef) error) error {
	dec := json.NewDecoder(r)
	for range count {
		var ref packRef
		if err := dec.Decode(&ref); err != nil {
			return err
		}
		if !packName.MatchString(ref.Name) || !validDigest(ref.Digest) ||
			len(ref.Key) != 32 || ref.Size <= 0 || ref.Size > maxPackSize {
			return errors.New("invalid vault manifest")
		}
		if err := visit(ref); err != nil {
			return err
		}
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("invalid vault manifest")
	}
	return nil
}

func priorPacks(path string, visit func(packRef) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	m, err := readManifest(tr)
	if err != nil || m.VaultPackCount == 0 {
		return err
	}
	h, err := tr.Next()
	if err != nil || h.Name != packIndex || h.Typeflag != tar.TypeReg {
		return errors.New("invalid vault manifest")
	}
	return walkPackIndex(tr, m.VaultPackCount, visit)
}

// Each snapshot owns links to its packs. Pruning an older snapshot therefore
// cannot strand a newer one. Live writer packs are append-only.
func snapshotPacks(ctx context.Context, src, stage, dir string, shared bool, index io.Writer, space *stageSpace) (int, error) {
	snaps, err := listDir(dir)
	if err != nil {
		return 0, err
	}
	type packVersion struct {
		name string
		size int64
	}
	type companion struct {
		ref packRef
		dir string
	}
	refs := make(map[packVersion]companion)
	for _, snap := range snaps {
		path := filepath.Join(dir, snap.Name)
		_ = priorPacks(path, func(ref packRef) error {
			key := packVersion{ref.Name, ref.Size}
			if _, ok := refs[key]; !ok {
				refs[key] = companion{ref: ref, dir: path + ".packs"}
			}
			return nil
		})
		// A shared backup directory can alternate between nodes with their
		// own packs. Look back through retention there to reuse their bytes.
		if !shared && len(refs) != 0 {
			break
		}
	}
	enc := json.NewEncoder(space.writer(index))
	count := 0
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, e error) error {
		if errors.Is(e, fs.ErrNotExist) && path == src {
			return nil
		}
		if e != nil {
			return e
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, e := filepath.Rel(src, path)
		if e != nil {
			return e
		}
		name := filepath.ToSlash(rel)
		if !d.Type().IsRegular() || !packName.MatchString(name) {
			return nil
		}
		st, e := d.Info()
		if e != nil {
			return e
		}
		size := st.Size()
		if size == 0 {
			return nil
		}
		if size > maxPackSize {
			return errors.New("vault snapshot exceeds pack limits")
		}
		dst := filepath.Join(stage, rel)
		if e = space.check(0); e != nil {
			return e
		}
		if e = os.MkdirAll(filepath.Dir(dst), 0700); e != nil {
			return e
		}
		prior, reused := refs[packVersion{name, size}]
		ref := prior.ref
		if reused {
			from := filepath.Join(prior.dir, rel)
			st, err := os.Lstat(from)
			reused = err == nil && st.Mode().IsRegular() &&
				st.Size() == size+packOverhead && os.Link(from, dst) == nil
		}
		if !reused {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			ref, err = sealPack(f, dst, size, space)
			if err != nil {
				return err
			}
		}
		ref.Name = name
		if err := enc.Encode(ref); err != nil {
			return err
		}
		count++
		return nil
	})
	return count, err
}

// Protected local snapshot companions travel only inside the encrypted bundle.
func exportSnapshot(ctx context.Context, f *os.File, w io.Writer) error {
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	h, err := tr.Next()
	if err != nil {
		return err
	}
	var m Manifest
	if err := decodeManifest(h, tr, &m); err != nil {
		return err
	}
	if m.VaultPackCount == 0 {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		_, err := io.Copy(w, f)
		return err
	}
	out := gzip.NewWriter(w)
	tw := tar.NewWriter(out)
	if err := writeManifest(tw, h, m); err != nil {
		return err
	}
	count := m.VaultPackCount
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch h.Name {
		case packIndex:
			if count == 0 || h.Typeflag != tar.TypeReg {
				return errors.New("invalid vault manifest")
			}
			err := walkPackIndex(tr, count, func(ref packRef) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				payload, err := unsealPack(filepath.Join(f.Name()+".packs", filepath.FromSlash(ref.Name)), ref)
				if err != nil {
					return err
				}
				if err := tw.WriteHeader(&tar.Header{
					Name: packPrefix + ref.Name, Size: ref.Size, Mode: 0600,
					PAXRecords: map[string]string{packDigest: ref.Digest},
				}); err != nil {
					return err
				}
				_, err = tw.Write(payload)
				return err
			})
			if err != nil {
				return err
			}
			count = 0
		default:
			if err := tw.WriteHeader(h); err != nil {
				return err
			}
			if _, err := io.Copy(tw, tr); err != nil {
				return err
			}
		}
	}
	if count != 0 {
		return errors.New("invalid vault manifest")
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return out.Close()
}

func restoreLocalPacks(ctx context.Context, tr io.Reader, bundle, stage string, count int, space *stageSpace) error {
	return walkPackIndex(tr, count, func(ref packRef) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		rel := filepath.FromSlash(ref.Name)
		payload, err := unsealPack(filepath.Join(bundle+".packs", rel), ref)
		if err != nil {
			return err
		}
		_, err = copyPack(bytes.NewReader(payload), filepath.Join(stage, packPrefix, rel), ref.Size, space)
		return err
	})
}

func installVault(staged, dataDir string) error {
	src := filepath.Join(staged, packPrefix)
	if _, e := os.Stat(src); errors.Is(e, fs.ErrNotExist) {
		return nil
	}
	conf, e := os.ReadFile(filepath.Join(dataDir, "config.json"))
	if e != nil {
		return e
	}
	root, e := packRoot(conf, dataDir)
	if e != nil {
		return e
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(src, path)
		if e != nil {
			return e
		}
		if !d.Type().IsRegular() || !packName.MatchString(filepath.ToSlash(rel)) {
			return nil
		}
		dst := filepath.Join(root, rel)
		st, e := d.Info()
		if e != nil {
			return e
		}
		if old, e := os.Stat(dst); e == nil && old.Size() >= st.Size() {
			return nil
		}
		if e = os.MkdirAll(filepath.Dir(dst), 0700); e != nil {
			return e
		}
		// Staging is normally on the same filesystem as the vault.
		// Linking avoids reserving a second full copy during restore.
		if os.Link(path, dst) == nil {
			syncDir(filepath.Dir(dst))
			return nil
		}
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		defer f.Close()
		tmp, e := os.CreateTemp(filepath.Dir(dst), ".restore-*")
		if e != nil {
			return e
		}
		defer os.Remove(tmp.Name())
		_, e = io.Copy(tmp, f)
		if e == nil {
			e = tmp.Sync()
		}
		ce := tmp.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
		if e = os.Rename(tmp.Name(), dst); e != nil {
			return e
		}
		syncDir(filepath.Dir(dst))
		return nil
	})
}

func extractPack(tr *tar.Reader, h *tar.Header, stage string, space *stageSpace) error {
	name := strings.TrimPrefix(h.Name, packPrefix)
	want := h.PAXRecords[packDigest]
	if !packName.MatchString(name) || h.Typeflag != tar.TypeReg || h.Size <= 0 || h.Size > maxPackSize || !validDigest(want) {
		return errors.New("invalid vault pack entry")
	}
	digest, err := copyPack(tr, filepath.Join(stage, packPrefix, filepath.FromSlash(name)), h.Size, space)
	if err == nil && digest != want {
		return errors.New("vault checksum mismatch")
	}
	return err
}
