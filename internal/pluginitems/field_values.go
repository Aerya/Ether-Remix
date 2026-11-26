package pluginitems

import (
	"context"
	"fmt"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

const (
	defaultFieldValuesLimit = 40
	maxFieldValuesLimit     = 200
)

// FieldValuesQuery requests a paged slice of distinct values for one plugin
// field from the field/person tally tables, with optional folded word-prefix
// search and optional inclusion of declared zero-count enum/strings values.
type FieldValuesQuery struct {
	LibraryID       core.LibraryID
	Level           string
	FieldKey        string
	Query           string
	Offset          int
	Limit           int
	Order           string // "count" (default) | "name"
	IncludeDeclared bool
}

// FieldValuesPage is one page of field values returned for the rules picker.
type FieldValuesPage struct {
	Values []FacetCount `json:"values"`
	More   bool         `json:"more,omitempty"`
}

// FieldValues pages over a plugin field's distinct values directly from the
// covering tally indexes (`plugin_item_field_tally` / `plugin_item_person_tally`)
// without scanning `plugin_items` or `plugin_item_fields`.
func (s *Service) FieldValues(ctx context.Context, q FieldValuesQuery) (FieldValuesPage, error) {
	if s == nil || s.db == nil {
		return FieldValuesPage{}, fmt.Errorf("pluginitems: nil service")
	}
	fieldKey := strings.TrimSpace(q.FieldKey)
	if q.LibraryID <= 0 || fieldKey == "" {
		return FieldValuesPage{}, fmt.Errorf("pluginitems: library_id and field_key are required")
	}
	limit := q.Limit
	if limit <= 0 {
		limit = defaultFieldValuesLimit
	}
	if limit > maxFieldValuesLimit {
		limit = maxFieldValuesLimit
	}
	offset := max(q.Offset, 0)
	level := strings.TrimSpace(q.Level)
	orderByName := strings.EqualFold(strings.TrimSpace(q.Order), "name")

	var (
		isPeople bool
		declared []string
	)
	if rec, err := s.GetKindByLibrary(ctx, q.LibraryID); err == nil {
		for _, fd := range rec.Decl.Fields {
			if fd.Key != fieldKey {
				continue
			}
			isPeople = fd.Type == "people"
			if q.IncludeDeclared {
				switch fd.Type {
				case "enum":
					declared = fd.Enum
				case "strings":
					declared = fd.Values
				}
			}
			break
		}
	}

	trimmedQ := strings.TrimSpace(q.Query)
	terms := foldQueryTerms(trimmedQ)

	isPG := store.DialectOf(s.db) == store.Postgres
	table := "plugin_item_field_tally"
	valCol, lowerCol, foldCol := "v_text", "v_lower", "v_fold"
	orderBy := `cnt DESC, v_lower ASC, v_text ASC`
	if isPG {
		orderBy = `cnt DESC, left(v_lower, 256) ASC, v_lower ASC, v_text ASC`
	}
	if orderByName {
		orderBy = `v_lower ASC, v_text ASC`
		if isPG {
			orderBy = `left(v_lower, 256) ASC, v_lower ASC, v_text ASC`
		}
	}
	if isPeople {
		table = "plugin_item_person_tally"
		valCol, lowerCol, foldCol = "label", "label_lower", "label_fold"
		orderBy = `cnt DESC, label_lower ASC, label ASC, v_int ASC`
		if isPG {
			orderBy = `cnt DESC, left(label_lower, 256) ASC, label_lower ASC, label ASC, v_int ASC`
		}
		if orderByName {
			orderBy = `label_lower ASC, label ASC, v_int ASC`
			if isPG {
				orderBy = `left(label_lower, 256) ASC, label_lower ASC, label ASC, v_int ASC`
			}
		}
	}

	var where strings.Builder
	where.WriteString(`library_id = ? AND level = ? AND field = ? AND cnt > 0`)
	args := []any{int64(q.LibraryID), level, fieldKey}
	if len(terms) > 0 {
		appendFoldedWordPrefixCol(&where, &args, foldCol, terms)
	} else if trimmedQ != "" {
		where.WriteString(` AND ` + lowerCol + ` LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(strings.ToLower(trimmedQ))+"%")
	}

	pageArgs := append(append([]any(nil), args...), limit+1, offset)
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+valCol+`, cnt FROM `+table+` WHERE `+where.String()+` ORDER BY `+orderBy+` LIMIT ? OFFSET ?`,
		pageArgs...)
	if err != nil {
		return FieldValuesPage{}, err
	}
	out := make([]FacetCount, 0, limit+1)
	for rows.Next() {
		var fc FacetCount
		if err := rows.Scan(&fc.Value, &fc.Count); err != nil {
			rows.Close()
			return FieldValuesPage{}, err
		}
		out = append(out, fc)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return FieldValuesPage{}, err
	}
	rows.Close()

	if len(out) > limit {
		return FieldValuesPage{Values: out[:limit], More: true}, nil
	}

	if len(declared) == 0 {
		return FieldValuesPage{Values: out, More: false}, nil
	}

	unpopulated, err := s.unpopulatedDeclaredValues(ctx, q.LibraryID, level, fieldKey, declared, trimmedQ, terms)
	if err != nil {
		return FieldValuesPage{}, err
	}
	if len(unpopulated) == 0 {
		return FieldValuesPage{Values: out, More: false}, nil
	}
	if len(out) == limit {
		return FieldValuesPage{Values: out, More: true}, nil
	}

	unpopStart := 0
	if len(out) == 0 && offset > 0 {
		var tallyCount int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM `+table+` WHERE `+where.String(),
			args...,
		).Scan(&tallyCount); err != nil {
			return FieldValuesPage{}, err
		}
		unpopStart = max(offset-tallyCount, 0)
	}
	if unpopStart >= len(unpopulated) {
		return FieldValuesPage{Values: out, More: false}, nil
	}
	need := limit - len(out)
	end := min(unpopStart+need, len(unpopulated))
	for _, v := range unpopulated[unpopStart:end] {
		out = append(out, FacetCount{Value: v, Count: 0})
	}
	return FieldValuesPage{Values: out, More: end < len(unpopulated)}, nil
}

func (s *Service) unpopulatedDeclaredValues(ctx context.Context, libraryID core.LibraryID, level, fieldKey string, declared []string, trimmedQ string, terms []string) ([]string, error) {
	lowerQ := strings.ToLower(trimmedQ)
	candidates := make([]string, 0, len(declared))
	for _, v := range declared {
		if trimmedQ != "" {
			if len(terms) > 0 {
				if !foldedWordsMatch(v, terms) {
					continue
				}
			} else if !strings.Contains(strings.ToLower(v), lowerQ) {
				continue
			}
		}
		candidates = append(candidates, v)
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	isPG := store.DialectOf(s.db) == store.Postgres
	present := make(map[string]bool, len(candidates))
	const chunkSize = 200
	for start := 0; start < len(candidates); start += chunkSize {
		batch := candidates[start:min(start+chunkSize, len(candidates))]
		var qSQL strings.Builder
		args := make([]any, 0, 2*len(batch)+3)
		qSQL.WriteString(`SELECT v_text FROM plugin_item_field_tally WHERE library_id = ? AND level = ? AND field = ? AND cnt > 0`)
		args = append(args, int64(libraryID), level, fieldKey)
		if isPG {
			appendPGMD5In(&qSQL, &args, "v_text", batch)
		} else {
			placeholders := make([]string, len(batch))
			for i, v := range batch {
				placeholders[i] = "?"
				args = append(args, v)
			}
			qSQL.WriteString(` AND v_text IN (` + strings.Join(placeholders, ",") + `)`)
		}
		rows, err := s.db.QueryContext(ctx, qSQL.String(), args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				rows.Close()
				return nil, err
			}
			present[v] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	out := make([]string, 0, len(candidates))
	for _, v := range candidates {
		if !present[v] {
			out = append(out, v)
		}
	}
	return out, nil
}
