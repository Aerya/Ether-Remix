package libraryfilter

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// RegisterMediaTypeMigration upgrades component-owned rule documents once on
// both SQLite and PostgreSQL. Arguments are trusted schema identifiers.
func RegisterMediaTypeMigration(component string, version int, table, column, key string) {
	store.Register(component, version, `SELECT 1;`)
	store.RegisterApply(component, version, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, fmt.Sprintf("SELECT %s,%s FROM %s", key, column, table))
		if err != nil {
			return err
		}
		type edit struct {
			id  int64
			raw string
		}
		var edits []edit
		identities := map[string][]string{}
		for rows.Next() {
			var id int64
			var raw string
			if err := rows.Scan(&id, &raw); err != nil {
				rows.Close()
				return err
			}
			if fixed, changed := core.NormalizeRuleDocument([]byte(raw)); changed {
				edits = append(edits, edit{id, string(fixed)})
				if table == "libraries" {
					var before, after []core.LibraryExclusion
					if json.Unmarshal([]byte(raw), &before) == nil && json.Unmarshal(fixed, &after) == nil && len(before) == len(after) {
						identity := func(rule core.LibraryExclusion) string {
							rule.Name = ""
							b, _ := json.Marshal(rule)
							sum := sha256.Sum256(b)
							return hex.EncodeToString(sum[:])
						}
						for i, rule := range before {
							target := identity(after[i])
							for _, media := range []string{rule.MediaType, "mixed", ""} {
								rule.MediaType = media
								old := identity(rule)
								identities[old] = append(identities[old], target)
							}
						}
					}
				}
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, e := range edits {
			extra := ""
			if table == "catalog_viewer_rules" {
				extra = ",revision=revision+1"
			}
			if _, err := tx.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET %s=?%s WHERE %s=?", table, column, extra, key), e.raw, e.id); err != nil {
				return err
			}
		}
		if table == "libraries" && len(identities) > 0 {
			return migrateExemptionIdentities(ctx, tx, identities)
		}
		return nil
	})
}

// Keep viewer exemptions attached to the same rule when canonicalization changes
// its content hash. A legacy unscoped identity can apply to both libraries.
func migrateExemptionIdentities(ctx context.Context, tx *sql.Tx, identities map[string][]string) error {
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE component='catalog_viewer_rules' AND version=1`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return nil
	} // A standalone libraries store has no viewers.
	rows, err := tx.QueryContext(ctx, `SELECT viewer_id,document FROM catalog_viewer_rules`)
	if err != nil {
		return err
	}
	type edit struct {
		id  int64
		raw string
	}
	var edits []edit
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return err
		}
		var doc map[string]json.RawMessage
		if json.Unmarshal([]byte(raw), &doc) != nil {
			continue
		}
		var exemptions []string
		if json.Unmarshal(doc["exemptions"], &exemptions) != nil {
			continue
		}
		changed := false
		seen := map[string]bool{}
		var out []string
		for _, old := range exemptions {
			next, ok := identities[old]
			if !ok {
				next = []string{old}
			} else {
				changed = true
			}
			for _, id := range next {
				if !seen[id] {
					seen[id] = true
					out = append(out, id)
				}
			}
		}
		if changed {
			doc["exemptions"], _ = json.Marshal(out)
			fixed, _ := json.Marshal(doc)
			edits = append(edits, edit{id, string(fixed)})
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, e := range edits {
		if _, err := tx.ExecContext(ctx, `UPDATE catalog_viewer_rules SET document=?,revision=revision+1 WHERE viewer_id=?`, e.raw, e.id); err != nil {
			return err
		}
	}
	return nil
}
