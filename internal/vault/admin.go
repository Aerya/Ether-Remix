package vault

import (
	"context"
	"errors"
	"github.com/braedonsaunders/ether/internal/nzb"
)

// Lister pages the release index in ascending key order, after cursor.
type Lister interface {
	List(ctx context.Context, q string, kind Kind, health *Health, limit int, cursor string) ([]Entry, error)
}

// VerifyRecord samples the first, middle and last article of the largest file.
func VerifyRecord(ctx context.Context, r *Record, v Verifier) (bool, error) {
	var largest *nzb.File
	var size int64
	if r != nil && r.NZB != nil {
		for _, f := range r.NZB.Files {
			if f != nil && len(f.Segments) > 0 {
				if n := f.Bytes(); largest == nil || n > size {
					largest, size = f, n
				}
			}
		}
	}
	if largest == nil || v == nil {
		return false, errors.New("vault: article verification unavailable")
	}
	seg := largest.Segments
	var ids []string
	for _, i := range []int{0, len(seg) / 2, len(seg) - 1} {
		if len(ids) == 0 || ids[len(ids)-1] != seg[i].MessageID {
			ids = append(ids, seg[i].MessageID)
		}
	}
	return v.VerifyArticles(ctx, ids, largest.Groups)
}
