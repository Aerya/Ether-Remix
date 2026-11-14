package xdcc

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"unicode"
)

// findPartial trouve le fichier partiel d'un paquet dans le dossier
// temporaire de Grabscher et renvoie sa taille finale exacte (le suffixe du
// nom). La taille connue de Remix peut être l'arrondi annoncé par le bot :
// le nom exact est essayé d'abord, puis le seul fichier au même préfixe.
func findPartial(dir, name string, size int64) (string, int64) {
	exact := path.Join(dir, TmpName(name, size))
	if localSize(exact) >= 0 {
		return exact, size
	}
	prefix := strings.TrimSuffix(TmpName(name, 0), "0")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", 0
	}
	found, total := "", int64(0)
	for _, e := range entries {
		n, ok := strings.CutPrefix(e.Name(), prefix)
		if !ok || !e.Type().IsRegular() {
			continue
		}
		v, err := strconv.ParseInt(n, 10, 64)
		if err != nil || v <= 0 || (size > 0 && !sameSize(v, size)) {
			continue
		}
		if found != "" {
			return "", 0 // ambigu : aucun choix au hasard
		}
		found, total = path.Join(dir, e.Name()), v
	}
	return found, total
}

// TmpName est le nom du fichier partiel de Grabscher (XG.Model.Domain.
// Helper.ShrinkFileName) : nom en minuscules réduit aux lettres a-z et aux
// chiffres, puis « .<taille> ».
func TmpName(name string, size int64) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String() + "." + strconv.FormatInt(size, 10)
}

// videoExt indique un fichier vidéo lisible tel quel (pas une archive).
func videoExt(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".mkv", ".mp4", ".m4v", ".webm", ".ts", ".m2ts", ".avi", ".mov":
		return true
	}
	return false
}

// contentType du fichier d'après son extension.
func contentType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".mkv":
		return "video/x-matroska"
	case ".webm":
		return "video/webm"
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".ts", ".m2ts":
		return "video/mp2t"
	case ".avi":
		return "video/x-msvideo"
	}
	return "application/octet-stream"
}

// Streamability décrit si un fichier incomplet peut être lu depuis le début.
type Streamability int

const (
	// StreamUnknown : pas encore assez d'octets pour décider.
	StreamUnknown Streamability = iota
	// StreamOK : Matroska/WebM, MPEG-TS, ou MP4 dont l'index (moov) précède
	// les données.
	StreamOK
	// StreamNo : MP4 sans « faststart », AVI ou format inconnu : l'index est
	// en fin de fichier, la lecture attend le fichier complet.
	StreamNo
)

// Sniff lit l'en-tête d'un fichier en cours et décide s'il est lisible en
// progressif.
func Sniff(f io.ReaderAt, available int64) Streamability {
	head := make([]byte, min(available, 64<<10))
	n, _ := f.ReadAt(head, 0)
	head = head[:n]
	if len(head) < 12 {
		return StreamUnknown
	}
	switch {
	case bytes.HasPrefix(head, []byte{0x1A, 0x45, 0xDF, 0xA3}):
		return StreamOK // EBML : Matroska, WebM
	case head[0] == 0x47 && (len(head) < 189 || head[188] == 0x47):
		if len(head) < 189 {
			return StreamUnknown
		}
		return StreamOK // MPEG-TS
	case bytes.HasPrefix(head, []byte("RIFF")) && bytes.Equal(head[8:12], []byte("AVI ")):
		return StreamNo
	case bytes.Equal(head[4:8], []byte("ftyp")):
		return sniffMP4(f, available)
	}
	return StreamNo
}

// sniffMP4 parcourt les boîtes de premier niveau : moov avant mdat suffit.
func sniffMP4(f io.ReaderAt, available int64) Streamability {
	var off int64
	hdr := make([]byte, 16)
	for i := 0; i < 64; i++ {
		if off+8 > available {
			return StreamUnknown
		}
		if n, _ := f.ReadAt(hdr[:8], off); n < 8 {
			return StreamUnknown
		}
		size := int64(binary.BigEndian.Uint32(hdr[:4]))
		kind := string(hdr[4:8])
		switch kind {
		case "moov":
			return StreamOK
		case "mdat":
			return StreamNo
		}
		if size == 1 {
			if off+16 > available {
				return StreamUnknown
			}
			if n, _ := f.ReadAt(hdr[8:16], off+8); n < 8 {
				return StreamUnknown
			}
			size = int64(binary.BigEndian.Uint64(hdr[8:16]))
		}
		if size < 8 {
			return StreamNo
		}
		off += size
	}
	return StreamNo
}

// localSize renvoie la taille d'un fichier visible par Remix, -1 sinon.
func localSize(p string) int64 {
	st, err := os.Stat(p)
	if err != nil || !st.Mode().IsRegular() {
		return -1
	}
	return st.Size()
}

// within indique si p est dans dir (ou égal).
func within(p, dir string) bool {
	p, dir = path.Clean(p), path.Clean(dir)
	return p == dir || strings.HasPrefix(p, strings.TrimRight(dir, "/")+"/")
}

// searchText prépare une requête texte pour Grabscher : il ignore les
// accents et sépare les mots sur points, tirets et soulignés. Les autres
// signes deviennent des espaces ; un « - » en tête de mot exclurait ce mot,
// il est donc retiré.
func searchText(parts ...string) string {
	var words []string
	for _, p := range parts {
		var b strings.Builder
		for _, r := range p {
			switch {
			case unicode.IsLetter(r) || unicode.IsDigit(r):
				b.WriteRune(r)
			default:
				// Les releases écrivent « L.Odyssee » : l'apostrophe sépare.
				b.WriteRune(' ')
			}
		}
		words = append(words, strings.Fields(b.String())...)
	}
	return strings.Join(words, " ")
}
