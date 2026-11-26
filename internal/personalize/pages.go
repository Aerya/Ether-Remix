package personalize

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// Recommendations pages (core.RowPage): one stored document per page, so saving
// Movies never rewrites Home. A page never saved serves its seeded default.

// pagesBackupMeta holds the pre-pages Home layout document for the one-time
// migration (rows-pages-v1, internal/app); migration 5 moved it there.
const pagesBackupMeta = "rows-pages-v1-backup"

func init() {
	// 2 and 4 are the pre-pages Home layout table and the meta table;
	// migration 6 retires the former.
	store.Register("personalize", 2, `CREATE TABLE personalize_home_layout (id INTEGER PRIMARY KEY CHECK(id=1), document TEXT NOT NULL);`)
	store.Register("personalize", 4, `CREATE TABLE personalize_meta (k TEXT PRIMARY KEY, v TEXT NOT NULL DEFAULT '');`)
	store.Register("personalize", 5, `CREATE TABLE personalize_pages (page TEXT PRIMARY KEY, document TEXT NOT NULL, revision INTEGER NOT NULL DEFAULT 1);`)
	// The Home layout document becomes Recommendations pages: keep it for
	// the one-time rows-pages-v1 migration, then drop its table.
	store.Register("personalize", 6, `
INSERT INTO personalize_meta(k, v) SELECT 'rows-pages-v1-backup', document FROM personalize_home_layout WHERE id = 1;
DROP TABLE personalize_home_layout;`)
}

// MigrationState reads a one-time migration's state ("" not run).
func (e *Engine) MigrationState(ctx context.Context, key string) (string, error) {
	var v string
	err := e.store.db.QueryRowContext(ctx, `SELECT v FROM personalize_meta WHERE k=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetMigrationState records a one-time migration's state.
func (e *Engine) SetMigrationState(ctx context.Context, key, value string) error {
	_, err := e.store.db.ExecContext(ctx, `INSERT INTO personalize_meta(k, v) VALUES(?, ?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, key, value)
	return err
}

// ErrPageConflict reports a save over a newer revision.
var ErrPageConflict = errors.New("personalize: page changed since it was loaded")

func (s *sqlStore) loadPages(ctx context.Context) (map[core.PageKey]core.RowPage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT page, document, revision FROM personalize_pages`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[core.PageKey]core.RowPage{}
	for rows.Next() {
		var key, doc string
		var rev int64
		if err := rows.Scan(&key, &doc, &rev); err != nil {
			return nil, err
		}
		var p core.RowPage
		if err := json.Unmarshal([]byte(doc), &p); err != nil {
			return nil, fmt.Errorf("personalize: page %s: %w", key, err)
		}
		p.Key, p.Revision = core.PageKey(key), rev
		out[p.Key] = p
	}
	return out, rows.Err()
}

func (s *sqlStore) savePage(ctx context.Context, p core.RowPage) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO personalize_pages(page, document, revision) VALUES(?,?,?)
ON CONFLICT(page) DO UPDATE SET document=excluded.document, revision=excluded.revision`, string(p.Key), string(b), p.Revision)
	return err
}

func (s *sqlStore) deletePage(ctx context.Context, key core.PageKey) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM personalize_pages WHERE page=?`, string(key))
	return err
}

// LegacyLayoutBackup returns the pre-pages Home layout document (raw JSON)
// for the one-time rows-pages-v1 migration, or "" when there is none.
func (e *Engine) LegacyLayoutBackup(ctx context.Context) (string, error) {
	var v string
	err := e.store.db.QueryRowContext(ctx, `SELECT v FROM personalize_meta WHERE k=?`, pagesBackupMeta).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// NewRowID returns a fresh row id ("r" + 8 base-32 characters).
func NewRowID() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz234567"
	var b [8]byte
	_, _ = rand.Read(b[:])
	out := make([]byte, 9)
	out[0] = 'r'
	for i, v := range b {
		out[i+1] = alphabet[int(v)%len(alphabet)]
	}
	return string(out)
}

// DefaultPage is the page Ether serves before the owner saves one:
// Personalized with Ether's picks, no rows of its own (Home and catalogue
// libraries). Plugin libraries are Curated over their declared shelves;
// that page is built from the library's sources (PluginDefaultPage).
func DefaultPage(key core.PageKey) core.RowPage {
	return core.RowPage{Key: key, Mode: core.PagePersonalized, Picks: true, Rows: []core.Row{}}
}

// Pages returns every saved page.
func (e *Engine) Pages(ctx context.Context) []core.RowPage {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]core.RowPage, 0, len(e.pages))
	for _, p := range e.pages {
		out = append(out, clonePage(p))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Page returns the saved page, or (default, false) when none is saved.
func (e *Engine) Page(ctx context.Context, key core.PageKey) (core.RowPage, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ok := e.pages[key]
	e.mu.Unlock()
	if ok {
		e.mu.Lock()
		return clonePage(p), true
	}
	d := e.defaultPage(ctx, key)
	e.mu.Lock()
	return d, false
}

// SavePage validates and stores a page. Rows without an id get one. The
// page's Revision must match the saved one (0 for a page never saved);
// otherwise ErrPageConflict.
func (e *Engine) SavePage(ctx context.Context, p core.RowPage) (core.RowPage, error) {
	p, err := e.NormalizePage(ctx, p)
	if err != nil {
		return p, err
	}
	e.saveMu.Lock()
	defer e.saveMu.Unlock()
	e.mu.Lock()
	cur, had := e.pages[p.Key]
	e.mu.Unlock()
	if had && cur.Revision != p.Revision || !had && p.Revision != 0 {
		return p, ErrPageConflict
	}
	p.Revision++
	p.UpdatedAt = e.now()
	if err := e.store.savePage(ctx, p); err != nil {
		return p, err
	}
	e.mu.Lock()
	e.pages[p.Key] = clonePage(p)
	e.layoutVer++
	e.mu.Unlock()
	e.notify("layout")
	return clonePage(p), nil
}

// ResetPage deletes a saved page; the seeded default applies again.
func (e *Engine) ResetPage(ctx context.Context, key core.PageKey) error {
	e.saveMu.Lock()
	defer e.saveMu.Unlock()
	if err := e.store.deletePage(ctx, key); err != nil {
		return err
	}
	e.mu.Lock()
	delete(e.pages, key)
	e.layoutVer++
	e.mu.Unlock()
	e.notify("layout")
	return nil
}

// ImportPages stores migrated pages without revision checks (rows-pages-v1).
func (e *Engine) ImportPages(ctx context.Context, pages []core.RowPage) error {
	e.saveMu.Lock()
	defer e.saveMu.Unlock()
	for _, p := range pages {
		p, err := e.NormalizePage(ctx, p)
		if err != nil {
			return fmt.Errorf("personalize: migrate page %s: %w", p.Key, err)
		}
		p.Revision = 1
		p.UpdatedAt = e.now()
		if err := e.store.savePage(ctx, p); err != nil {
			return err
		}
		e.mu.Lock()
		e.pages[p.Key] = clonePage(p)
		e.mu.Unlock()
	}
	e.mu.Lock()
	e.layoutVer++
	e.mu.Unlock()
	e.notify("layout")
	return nil
}

// OfferOnce adds a plugin row to a page the first time its source becomes
// available (a plugin's Home-row surface, a new plugin library's shelves),
// recording it in Page.Offered so an owner's later Hide or removal sticks.
// It returns whether the page changed. Pages never saved get it from their
// default at read time, so they are left alone.
func (e *Engine) OfferOnce(ctx context.Context, key core.PageKey, offerID string, row core.Row) (bool, error) {
	e.mu.Lock()
	p, ok := e.pages[key]
	e.mu.Unlock()
	if !ok || slices.Contains(p.Offered, offerID) {
		return false, nil
	}
	p = clonePage(p)
	p.Offered = append(p.Offered, offerID)
	if row.ID == "" {
		row.ID = NewRowID()
	}
	if row.Placement.Mode == core.PlacementPinned {
		for _, r := range p.Rows {
			if r.Placement.Mode == core.PlacementPinned && r.Placement.Position == row.Placement.Position {
				row.Placement.Mode, row.Placement.Position = core.PlacementShow, 0
				break
			}
		}
	}
	if p.Mode == core.PageCurated && row.Placement.Mode != core.PlacementHide {
		row.Placement.Mode, row.Placement.Position = core.PlacementShow, 0
	}
	p.Rows = append(p.Rows, row)
	_, err := e.SavePage(ctx, p)
	return err == nil, err
}

// NormalizePage validates a page against the row sources and assigns ids.
func (e *Engine) NormalizePage(ctx context.Context, p core.RowPage) (core.RowPage, error) {
	p = clonePage(p)
	for i := range p.Rows {
		if p.Rows[i].ID == "" {
			p.Rows[i].ID = NewRowID()
		}
		if p.Rows[i].Placement.Audience.Mode == "" {
			p.Rows[i].Placement.Audience.Mode = core.AudienceEveryone
		}
	}
	if p.Rows == nil {
		p.Rows = []core.Row{}
	}
	// No two rows that show together share a pinned position, however the
	// page arrived (a stale draft, an import, an app over MCP).
	p.NormalizePins()
	if err := p.ValidateStructure(); err != nil {
		return p, &ValidationError{Problems: []Problem{{"page", err.Error()}}}
	}
	for i, r := range p.Rows {
		src, ok := e.sourceInfo(r.Source.Kind)
		if !ok && strings.HasPrefix(r.Source.Kind, "plugin:") {
			// A plugin's list that isn't loaded (or was uninstalled): kept on
			// the page, served again when the plugin is back.
			continue
		}
		if !ok {
			return p, &ValidationError{Problems: []Problem{{fmt.Sprintf("rows[%d].source", i), fmt.Sprintf("%q is not a row source", r.Source.Kind)}}}
		}
		if err := src.ValidateParams(r.Source.Params); err != nil {
			return p, &ValidationError{Problems: []Problem{{fmt.Sprintf("rows[%d].source", i), err.Error()}}}
		}
		if r.Scope.PerLibrary && !src.Split {
			return p, &ValidationError{Problems: []Problem{{fmt.Sprintf("rows[%d].scope", i), "this row can't be shown as one row per library"}}}
		}
		if src.HomeOnly && p.Key != core.PageHome {
			return p, &ValidationError{Problems: []Problem{{fmt.Sprintf("rows[%d].source", i), "this row only fits Home"}}}
		}
		if err := e.listenLook(ctx, p.Key, r); err != nil {
			return p, &ValidationError{Problems: []Problem{{fmt.Sprintf("rows[%d].look", i), err.Error()}}}
		}
		if p.Key != core.PageHome && len(r.Scope.Libraries) > 0 {
			return p, &ValidationError{Problems: []Problem{{fmt.Sprintf("rows[%d].scope", i), "a library page's rows draw from that library"}}}
		}
	}
	return p, nil
}

func clonePage(p core.RowPage) core.RowPage {
	b, _ := json.Marshal(p)
	var out core.RowPage
	_ = json.Unmarshal(b, &out)
	out.Key, out.Revision, out.UpdatedAt = p.Key, p.Revision, p.UpdatedAt
	if out.Rows == nil {
		out.Rows = []core.Row{}
	}
	return out
}

// pageKeyFor resolves an engine section name into a page key: "home", a
// full "library:<id>/<tab>", or "library:<id>" (the library's first rows
// tab, which Plex, Jellyfin and Emby read as its Recommended page).
func (e *Engine) pageKeyFor(ctx context.Context, section string) core.PageKey {
	if section == "" || section == surfHome {
		return core.PageHome
	}
	if k := core.PageKey(section); k.Valid() {
		return k
	}
	if id, ok := parseLibrarySection(section); ok {
		tab := "recommended"
		if e.opts.LibraryRowsTab != nil {
			if t := e.opts.LibraryRowsTab(ctx, core.LibraryID(id)); t != "" {
				tab = t
			}
		}
		return core.LibraryPage(core.LibraryID(id), tab)
	}
	return core.PageHome
}

// parseLibrarySection parses "library:<id>" (an app's library request).
func parseLibrarySection(s string) (int, bool) {
	raw, ok := strings.CutPrefix(s, "library:")
	if !ok || raw == "" || strings.Contains(raw, "/") {
		return 0, false
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 || strconv.Itoa(id) != raw {
		return 0, false
	}
	return id, true
}

func librarySectionName(id int) string {
	if id <= 0 {
		return ""
	}
	return "library:" + strconv.Itoa(id)
}
