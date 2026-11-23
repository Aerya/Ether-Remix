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
const maxVaultBytes int64 = 64 << 30

// MaxUploadBytes allows the vault and database independent restore budgets.
const MaxUploadBytes = maxBundleFile + maxVaultBytes + 32<<20

var packName = regexp.MustCompile("^[0-9]{6}/[0-9a-f]{32}\\.pack$")

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

func copyPack(src io.Reader, dst string, size int64) (string, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return "", err
	}
	f, e := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return "", e
	}
	h := sha256.New()
	_, e = io.CopyN(io.MultiWriter(f, h), src, size)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), ce
}

func readManifest(f *os.File) (Manifest, error) {
	var m Manifest
	gz, e := gzip.NewReader(f)
	if e != nil {
		return m, e
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	h, e := tr.Next()
	if e != nil {
		return m, e
	}
	if h.Name != "manifest.json" || h.Size > 16<<20 {
		return m, errors.New("invalid snapshot manifest")
	}
	e = json.NewDecoder(tr).Decode(&m)
	return m, e
}

// A companion contains ciphertext only. Its random key stays in the same
// protected manifest as the DB checksums. Reusing the ciphertext by hard link
// does not give another backup (or a copied pack alone) access to the record.
type packRef struct {
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
func sealPack(f *os.File, dst string, size int64) (packRef, error) {
	ref := packRef{Size: size, Key: make([]byte, 32)}
	if _, err := rand.Read(ref.Key); err != nil {
		return ref, err
	}
	a, err := packCipher(ref.Key)
	if err != nil {
		return ref, err
	}
	b := make([]byte, size)
	if _, err = io.ReadFull(f, b); err != nil {
		return ref, err
	}
	digest := sha256.Sum256(b)
	ref.Digest = hex.EncodeToString(digest[:])
	sealed := a.Seal(nil, nil, b, nil)
	_, err = copyPack(bytes.NewReader(sealed), dst, int64(len(sealed)))
	return ref, err
}
func unsealPack(path string, ref packRef) ([]byte, error) {
	if ref.Size < 0 || ref.Size > maxPackSize {
		return nil, errors.New("invalid vault pack size")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	a, err := packCipher(ref.Key)
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(f, ref.Size+int64(a.Overhead())+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) != ref.Size+int64(a.Overhead()) {
		return nil, errors.New("invalid sealed pack size")
	}
	plain, err := a.Open(nil, nil, b, nil)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(plain)
	if hex.EncodeToString(digest[:]) != ref.Digest {
		return nil, errors.New("vault checksum mismatch")
	}
	return plain, nil
}

// Each snapshot owns links to its packs. Pruning an older snapshot therefore
// cannot strand a newer one. Open writer packs are append-only until sealed.
func snapshotPacks(ctx context.Context, src, stage, dir string, m *Manifest) error {
	snaps, e := listDir(dir)
	if e != nil {
		return e
	}
	type saved struct {
		ref  packRef
		path string
	}
	byName := make(map[string][]saved)
	for _, snap := range snaps {
		path := filepath.Join(dir, snap.Name)
		if _, err := os.Stat(path + ".packs"); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		old, err := readManifest(f)
		f.Close()
		if err != nil {
			continue
		}
		for name, ref := range old.VaultPacks {
			if packName.MatchString(name) {
				byName[name] = append(byName[name], saved{ref, filepath.Join(path+".packs", filepath.FromSlash(name))})
			}
		}
	}
	var total int64
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, e error) error {
		if errors.Is(e, fs.ErrNotExist) {
			return nil
		}
		if e != nil {
			return e
		}
		if ctx.Err() != nil {
			return ctx.Err()
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
		total += size
		if size > maxPackSize || total > maxVaultBytes {
			return errors.New("vault snapshot exceeds pack limits")
		}
		dst := filepath.Join(stage, rel)
		if e = os.MkdirAll(filepath.Dir(dst), 0700); e != nil {
			return e
		}
		var ref packRef
		for _, candidate := range byName[name] {
			if candidate.ref.Size != size || !validDigest(candidate.ref.Digest) {
				continue
			}
			st, err := os.Lstat(candidate.path)
			if err == nil && st.Mode().IsRegular() && os.Link(candidate.path, dst) == nil {
				ref = candidate.ref
				break
			}
		}
		if ref.Digest == "" {
			f, e := os.Open(path)
			if errors.Is(e, fs.ErrNotExist) {
				return nil
			}
			if e != nil {
				return e
			}
			defer f.Close()
			ref, e = sealPack(f, dst, size)
			if e != nil {
				return e
			}
		}
		if m.VaultPacks == nil {
			m.VaultPacks = map[string]packRef{}
		}
		m.VaultPacks[name] = ref
		return nil
	})
}

// Protected local snapshot companions travel only in the encrypted DB bundle.
func exportSnapshot(f *os.File, w io.Writer) error {
	m, e := readManifest(f)
	if e != nil {
		return e
	}
	if _, e = f.Seek(0, 0); e != nil {
		return e
	}
	if len(m.VaultPacks) == 0 {
		_, e = io.Copy(w, f)
		return e
	}
	gz, e := gzip.NewReader(f)
	if e != nil {
		return e
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	out := gzip.NewWriter(w)
	tw := tar.NewWriter(out)
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		if e = tw.WriteHeader(h); e != nil {
			return e
		}
		if _, e = io.Copy(tw, tr); e != nil {
			return e
		}
	}
	for name, ref := range m.VaultPacks {
		if !packName.MatchString(name) {
			return errors.New("invalid vault pack name")
		}
		payload, e := unsealPack(filepath.Join(f.Name()+".packs", filepath.FromSlash(name)), ref)
		if errors.Is(e, fs.ErrNotExist) {
			continue
		}
		if e != nil {
			return e
		}
		e = tw.WriteHeader(&tar.Header{Name: packPrefix + name, Size: int64(len(payload)), Mode: 0600})
		if e == nil {
			_, e = tw.Write(payload)
		}
		if e != nil {
			return e
		}
	}
	if e = tw.Close(); e != nil {
		return e
	}
	return out.Close()
}

func restoreLocalPacks(bundle, stage string, m Manifest) error {
	for name, ref := range m.VaultPacks {
		if !packName.MatchString(name) || !validDigest(ref.Digest) {
			return errors.New("invalid vault manifest")
		}
		payload, e := unsealPack(filepath.Join(bundle+".packs", filepath.FromSlash(name)), ref)
		if errors.Is(e, fs.ErrNotExist) {
			continue
		}
		if e != nil {
			return e
		}
		dst := filepath.Join(stage, packPrefix, filepath.FromSlash(name))
		_, e = copyPack(bytes.NewReader(payload), dst, int64(len(payload)))
		if e != nil {
			return e
		}
	}
	return nil
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
		return os.Rename(tmp.Name(), dst)
	})
}

func extractPack(tr *tar.Reader, h *tar.Header, stage string, packs map[string]string, total *int64) error {
	name := strings.TrimPrefix(h.Name, packPrefix)
	*total += h.Size
	if !packName.MatchString(name) || h.Typeflag != tar.TypeReg || h.Size < 0 || h.Size > maxPackSize || *total > maxVaultBytes {
		return errors.New("invalid vault pack entry")
	}
	digest, e := copyPack(tr, filepath.Join(stage, packPrefix, filepath.FromSlash(name)), h.Size)
	if e == nil {
		packs[name] = digest
	}
	return e
}
