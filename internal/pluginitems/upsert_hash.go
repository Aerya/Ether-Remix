package pluginitems

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"slices"
	"strconv"

	"github.com/braedonsaunders/ether/internal/core"
)

// UpsertStats is what one upsert batch actually did. Accepted counts the
// items the caller sent (before in-batch deduplication, as KindUpsert has
// always reported); Added, Updated and Unchanged count the deduplicated
// validated items.
type UpsertStats struct {
	Accepted  int
	Added     int
	Updated   int
	Unchanged int
}

// preparedItem is one validated item with every stored representation and
// its content hash computed before any write transaction opens (#646):
// hashing and JSON encoding must not extend the writer's hold.
type preparedItem struct {
	validated      ValidatedItem
	parentRef      string
	hintsStr       string
	fieldsStr      string
	unavailableStr string
	playableInt    int
	hash           string
}

func prepareUpsertItem(pluginID, kindKey, declHash string, v ValidatedItem) (preparedItem, error) {
	it := v.Item
	p := preparedItem{validated: v, hintsStr: "{}", fieldsStr: "{}"}
	if it.ParentID != "" {
		p.parentRef = string(core.NewItemRef(pluginID, kindKey, it.ParentID))
	}
	if len(it.Hints) > 0 {
		p.hintsStr = string(it.Hints)
	}
	if len(it.Fields) > 0 {
		if b, err := json.Marshal(it.Fields); err == nil {
			p.fieldsStr = string(b)
		}
	}
	if it.Unavailable != nil {
		b, err := json.Marshal(it.Unavailable)
		if err != nil {
			return preparedItem{}, err
		}
		p.unavailableStr = string(b)
	}
	if it.Playable {
		p.playableInt = 1
	}
	p.hash = itemContentHash(declHash, p)
	return p, nil
}

// itemContentHash digests exactly what an upsert would store for the item:
// the plugin_items columns, the typed field rows and the art rows, under
// the kind declaration that produced them. Two upserts with the same hash
// would rewrite identical rows, FTS text (derived from title and the
// fields), and tallies, so the second is skipped. Anything ambiguous
// (hints byte form, art order) is hashed as sent, so a difference costs a
// rewrite, never a stale skip.
func itemContentHash(declHash string, p preparedItem) string {
	it := p.validated.Item
	h := sha256.New()
	putStr := func(s string) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	putInt := func(v int64) { putStr(strconv.FormatInt(v, 10)) }
	putStr(declHash)
	putStr(p.parentRef)
	putStr(it.Level)
	putInt(int64(it.Index))
	putStr(it.Title)
	putStr(it.SortTitle)
	putStr(it.Summary)
	putInt(int64(it.Year))
	putInt(it.Released)
	putStr(strconv.FormatUint(math.Float64bits(it.Rating), 10))
	putInt(int64(p.playableInt))
	putInt(int64(it.ChildCount))
	putInt(int64(it.LeafCount))
	putStr(p.unavailableStr)
	putStr(p.hintsStr)
	putStr(p.fieldsStr)
	putStr(it.ETag)
	// Indexed fields are gathered through a map, so their order varies
	// between calls; sort a copy so the hash does not.
	fields := slices.Clone(p.validated.IndexedFields)
	slices.SortFunc(fields, func(a, b IndexedFieldValue) int {
		if a.Field != b.Field {
			return compareStr(a.Field, b.Field)
		}
		if a.Text != b.Text {
			return compareStr(a.Text, b.Text)
		}
		if a.Int != b.Int {
			return compareInt(a.Int, b.Int)
		}
		if a.Real != b.Real {
			return compareInt(int64(math.Float64bits(a.Real)), int64(math.Float64bits(b.Real)))
		}
		return compareStr(a.Fold, b.Fold)
	})
	for _, fv := range fields {
		putStr(fv.Field)
		putStr(fv.Text)
		putInt(fv.Int)
		putStr(strconv.FormatUint(math.Float64bits(fv.Real), 10))
		putStr(fv.Fold)
	}
	for _, a := range it.Art {
		putStr(a.Role)
		putStr(a.URL)
		putInt(int64(a.Width))
		putInt(int64(a.Height))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func compareStr(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func compareInt(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
