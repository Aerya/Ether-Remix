package libraryfilter

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// PluginSQLHidden compiles the same ordered policy against plugin_items AS i.
// fields maps the rule vocabulary onto declared, indexed plugin field keys.
func (f *Filter) PluginSQLHidden(typ core.MediaType, fields map[string]string) (string, []any) {
	return f.PluginSQLHiddenForViewer(typ, fields, 0)
}

// PluginSQLHiddenForViewer compiles the ordered policy against plugin_items AS i,
// binding viewer-state conditions whose userId is 0 to viewerID.
func (f *Filter) PluginSQLHiddenForViewer(typ core.MediaType, fields map[string]string, viewerID int64) (string, []any) {
	if f == nil {
		return "0=1", nil
	}
	bound := f.bindPluginViewer(viewerID)
	q, args := bound.SQLHiddenForType(typ)
	return pluginSQL(q, typ, fields), args
}

// PluginSQLShownForViewer compiles the visible predicate against plugin_items AS i,
// keeping single allow-rule policies (such as Smart View tab filters) sargable
// without a wrapping CASE expression.
func (f *Filter) PluginSQLShownForViewer(typ core.MediaType, fields map[string]string, viewerID int64) (string, []any) {
	if f == nil {
		return "1=1", nil
	}
	bound := f.bindPluginViewer(viewerID)
	q, args := bound.SQLShownForType(typ)
	return pluginSQL(q, typ, fields), args
}

// HasPositiveViewerFilter reports whether f is a single allow-rule filter whose
// top-level AND clauses include a positive viewer-state semi-join (so SQLite
// should drive from the viewer's small state/favorites/watchlist table via
// plugin_items(seq) instead of scanning the library).
func (f *Filter) HasPositiveViewerFilter() bool {
	if f == nil || f.movieFallback != "hide" || f.showFallback != "hide" || len(f.rules) != 1 || f.rules[0].action != "show" {
		return false
	}
	r := f.rules[0]
	if r.match == "any" && len(r.rules) > 1 {
		return false
	}
	for _, c := range r.rules {
		if conditionHasPositiveViewer(c) {
			return true
		}
	}
	return false
}

func conditionHasPositiveViewer(c condition) bool {
	if c.field == "group" {
		if c.op == "is" {
			for _, child := range c.children {
				if conditionHasPositiveViewer(child) {
					return true
				}
			}
		}
		return false
	}
	if c.field != "viewer" || c.op != "is" || c.predicate == nil || c.predicate.Group == nil {
		return false
	}
	g := c.predicate.Group
	return (g.Watched != nil && *g.Watched) ||
		(g.Unwatched != nil && !*g.Unwatched) ||
		(g.InProgress != nil && *g.InProgress) ||
		(g.History != nil && *g.History) ||
		(g.Favorite != nil && *g.Favorite) ||
		(g.Watchlist != nil && *g.Watchlist) ||
		(g.OnWatchlist != nil && *g.OnWatchlist) ||
		g.LastViewedAfter > 0 || g.LastViewedBefore > 0
}

func (f *Filter) bindPluginViewer(viewerID int64) *Filter {
	if f == nil || !f.HasViewerState() {
		return f
	}
	out := *f
	out.rules = append([]rule(nil), f.rules...)
	var bind func(condition) condition
	bind = func(c condition) condition {
		if c.field == "viewer" && c.predicate != nil && c.predicate.Group != nil {
			q, args := compilePluginViewerGroup(c.predicate.Group, viewerID)
			c.predicateSQL = q
			c.predicateArgs = args
		}
		if len(c.children) > 0 {
			c.children = append([]condition(nil), c.children...)
			for i, child := range c.children {
				c.children[i] = bind(child)
			}
		}
		return c
	}
	for i, g := range out.rules {
		g.rules = append([]condition(nil), g.rules...)
		for j, c := range g.rules {
			g.rules[j] = bind(c)
		}
		out.rules[i] = g
	}
	return &out
}

func compilePluginViewerGroup(g *core.FilterGroup, viewerID int64) (string, []any) {
	if g == nil {
		return "1=0", nil
	}
	uid := g.UserID
	if uid <= 0 {
		uid = viewerID
	}
	keyBase := core.PluginKeyOffset
	keyEnd := core.PluginKeyOffset + core.PluginKeyMaxSeq
	keyMinStr := strconv.FormatInt(keyBase+1, 10)
	keyEndStr := strconv.FormatInt(keyEnd, 10)

	var parts []string
	var args []any

	addWatch := func(cond string, want bool) {
		if uid <= 0 {
			if want {
				parts = append(parts, "1=0")
			} else {
				parts = append(parts, "1=1")
			}
			return
		}
		if want {
			parts = append(parts, fmt.Sprintf("i.seq IN (SELECT w.rating_key-%d FROM state_watch w WHERE w.user_id=? AND w.rating_key>%d AND w.rating_key<%d AND %s)", keyBase, keyBase, keyEnd, cond))
		} else {
			parts = append(parts, fmt.Sprintf("NOT EXISTS (SELECT 1 FROM state_watch w WHERE w.user_id=? AND w.rating_key=%d+i.seq AND %s)", keyBase, cond))
		}
		args = append(args, uid)
	}

	if g.Watched != nil {
		addWatch("w.view_count>0", *g.Watched)
	}
	if g.Unwatched != nil {
		addWatch("w.view_count>0", !*g.Unwatched)
	}
	if g.InProgress != nil {
		addWatch("w.view_offset_ns>0", *g.InProgress)
	}
	if g.History != nil {
		if uid <= 0 {
			if *g.History {
				parts = append(parts, "1=0")
			} else {
				parts = append(parts, "1=1")
			}
		} else {
			// Roll descendant watch activity up through parent_ref (up to the
			// 5-level plugin hierarchy) so container shelves (for example
			// "Jump back in" albums when tracks were played) match without
			// scanning plugin_items: every branch drives from state_watch and
			// walks plugin_items by seq/item_ref PK.
			watchCond := fmt.Sprintf("w.user_id=? AND w.rating_key>%d AND w.rating_key<%d AND (w.last_viewed_at>0 OR w.view_count>0 OR w.view_offset_ns>0)", keyBase, keyEnd)
			rolledSubq := fmt.Sprintf(
				`SELECT p0.item_ref FROM state_watch w JOIN plugin_items p0 ON p0.seq = w.rating_key - %d WHERE %s
UNION
SELECT p0.parent_ref FROM state_watch w JOIN plugin_items p0 ON p0.seq = w.rating_key - %d WHERE %s AND p0.parent_ref <> ''
UNION
SELECT p1.parent_ref FROM state_watch w JOIN plugin_items p0 ON p0.seq = w.rating_key - %d JOIN plugin_items p1 ON p1.item_ref = p0.parent_ref WHERE %s AND p0.parent_ref <> '' AND p1.parent_ref <> ''
UNION
SELECT p2.parent_ref FROM state_watch w JOIN plugin_items p0 ON p0.seq = w.rating_key - %d JOIN plugin_items p1 ON p1.item_ref = p0.parent_ref JOIN plugin_items p2 ON p2.item_ref = p1.parent_ref WHERE %s AND p0.parent_ref <> '' AND p1.parent_ref <> '' AND p2.parent_ref <> ''
UNION
SELECT p3.parent_ref FROM state_watch w JOIN plugin_items p0 ON p0.seq = w.rating_key - %d JOIN plugin_items p1 ON p1.item_ref = p0.parent_ref JOIN plugin_items p2 ON p2.item_ref = p1.parent_ref JOIN plugin_items p3 ON p3.item_ref = p2.parent_ref WHERE %s AND p0.parent_ref <> '' AND p1.parent_ref <> '' AND p2.parent_ref <> '' AND p3.parent_ref <> ''`,
				keyBase, watchCond,
				keyBase, watchCond,
				keyBase, watchCond,
				keyBase, watchCond,
				keyBase, watchCond,
			)
			if *g.History {
				parts = append(parts, "i.item_ref IN ("+rolledSubq+")")
			} else {
				parts = append(parts, "i.item_ref NOT IN ("+rolledSubq+")")
			}
			args = append(args, uid, uid, uid, uid, uid)
		}
	}
	if g.Favorite != nil {
		if uid <= 0 {
			if *g.Favorite {
				parts = append(parts, "1=0")
			} else {
				parts = append(parts, "1=1")
			}
		} else if *g.Favorite {
			parts = append(parts, fmt.Sprintf("i.seq IN (SELECT CAST(fav.subject_id AS BIGINT)-%d FROM favorites_items fav WHERE fav.viewer_id=? AND fav.kind='title' AND fav.subject_id>='%s' AND fav.subject_id<'%s')", keyBase, keyMinStr, keyEndStr))
			args = append(args, uid)
		} else {
			parts = append(parts, fmt.Sprintf("NOT EXISTS (SELECT 1 FROM favorites_items fav WHERE fav.viewer_id=? AND fav.kind='title' AND fav.subject_id=CAST((%d+i.seq) AS TEXT))", keyBase))
			args = append(args, uid)
		}
	}
	addWatchlist := func(want bool) {
		if uid <= 0 {
			if want {
				parts = append(parts, "1=0")
			} else {
				parts = append(parts, "1=1")
			}
			return
		}
		if want {
			parts = append(parts, fmt.Sprintf("i.seq IN (SELECT w.item_key-%d FROM watchlist_items w WHERE w.user_id=? AND w.item_key>%d AND w.item_key<%d)", keyBase, keyBase, keyEnd))
		} else {
			parts = append(parts, fmt.Sprintf("NOT EXISTS (SELECT 1 FROM watchlist_items w WHERE w.user_id=? AND w.item_key=%d+i.seq)", keyBase))
		}
		args = append(args, uid)
	}
	if g.OnWatchlist != nil {
		addWatchlist(*g.OnWatchlist)
	}
	if g.Watchlist != nil {
		addWatchlist(*g.Watchlist)
	}
	if g.LastViewedAfter > 0 || g.LastViewedBefore > 0 {
		if uid <= 0 {
			parts = append(parts, "1=0")
		} else {
			const maxSeconds = int64(9223372036)
			p := fmt.Sprintf("i.seq IN (SELECT w.rating_key-%d FROM state_watch w WHERE w.user_id=? AND w.rating_key>%d AND w.rating_key<%d AND w.last_viewed_at>0", keyBase, keyBase, keyEnd)
			args = append(args, uid)
			if g.LastViewedAfter > 0 {
				if g.LastViewedAfter > maxSeconds {
					p += " AND 1=0"
				} else {
					p += " AND w.last_viewed_at>=?"
					args = append(args, g.LastViewedAfter*1_000_000_000)
				}
			}
			if g.LastViewedBefore > 0 && g.LastViewedBefore < maxSeconds {
				p += " AND w.last_viewed_at<?"
				args = append(args, (g.LastViewedBefore+1)*1_000_000_000)
			}
			parts = append(parts, p+")")
		}
	}
	if g.StateField != "" {
		if uid <= 0 {
			parts = append(parts, "1=0")
		} else {
			col, scale := pluginViewerStateColumn(g.StateField)
			op := sqlStateOperator(g.StateOperator)
			if col == "" || op == "" {
				parts = append(parts, "1=0")
			} else if g.StateField == "viewOffset" || g.StateField == "lastViewedAt" || g.StateField == "lastRatedAt" {
				parts = append(parts, fmt.Sprintf("COALESCE((SELECT %s FROM state_watch w WHERE w.user_id=? AND w.rating_key=%d+i.seq),0)%s?", col, keyBase, op))
				args = append(args, uid, g.StateValue*scale)
			} else {
				parts = append(parts, fmt.Sprintf("EXISTS (SELECT 1 FROM state_watch w WHERE w.user_id=? AND w.rating_key=%d+i.seq AND %s%s?)", keyBase, col, op))
				args = append(args, uid, g.StateValue)
			}
		}
	}
	if len(parts) == 0 {
		return "1=0", nil
	}
	return strings.Join(parts, " AND "), args
}

func pluginViewerStateColumn(field string) (string, int64) {
	switch field {
	case "viewCount":
		return "w.view_count", 1
	case "viewOffset":
		return "w.view_offset_ns", 1_000_000
	case "lastViewedAt":
		return "w.last_viewed_at/1000000000", 1
	case "lastRatedAt":
		return "w.rating_at/1000000000", 1
	default:
		return "", 0
	}
}

func sqlStateOperator(op string) string {
	switch op {
	case "=", "==":
		return "="
	case "!=", "!==":
		return "<>"
	case "<<", "<":
		return "<"
	case "<<=", "<=":
		return "<="
	case ">>", ">":
		return ">"
	case ">>=", ">=":
		return ">="
	default:
		return ""
	}
}

func pluginSQL(q string, typ core.MediaType, fields map[string]string) string {
	q = strings.ReplaceAll(q, "t.type", "'"+string(typ)+"'")
	for _, pair := range [][2]string{{"year", "year"}, {"rating", "rating"}} {
		q = strings.ReplaceAll(q, "t."+pair[0], "i."+pair[1])
	}
	for _, pair := range [][2]string{{"votes", "vote_count"}, {"popularity", "popularity"}, {"runtime", "runtime_sec"}, {"language", "language"}, {"contentRating", "content_rating_rank"}} {
		expr := "0"
		column := "v_int + pf.v_real"
		if pair[0] == "language" {
			expr = "''"
			column = "v_text"
		}
		if key := fields[pair[0]]; key != "" {
			expr = "COALESCE((SELECT pf." + column + " FROM plugin_item_fields pf WHERE pf.item_ref=i.item_ref AND pf.field='" + strings.ReplaceAll(key, "'", "''") + "' LIMIT 1)," + expr + ")"
		}
		q = strings.ReplaceAll(q, "t."+pair[1], expr)
	}
	for _, pair := range [][2]string{{"genre", "genres"}, {"studio", "studios"}, {"country", "countries"}, {"keyword", "keywords"}} {
		key := strings.ReplaceAll(fields[pair[0]], "'", "''")
		for _, alias := range []string{"lf", "co"} {
			q = strings.ReplaceAll(q, "FROM catalog_"+pair[1]+" "+alias+" WHERE "+alias+".key=t.key", "FROM plugin_item_fields "+alias+" WHERE "+alias+".item_ref=i.item_ref AND "+alias+".field='"+key+"'")
			q = strings.ReplaceAll(q, alias+"."+pair[0], alias+".v_text")
		}
	}
	return q
}

// PluginCatchCountsSQL counts the first matching rule for each plugin item.
func (f *Filter) PluginCatchCountsSQL(typ core.MediaType, fields map[string]string) (string, []any) {
	return f.PluginCatchCountsSQLForViewer(typ, fields, 0)
}

// PluginCatchCountsSQLForViewer counts the first matching rule for each plugin
// item, binding viewer-state conditions whose userId is 0 to viewerID.
func (f *Filter) PluginCatchCountsSQLForViewer(typ core.MediaType, fields map[string]string, viewerID int64) (string, []any) {
	if f == nil {
		return "", nil
	}
	bound := f.bindPluginViewer(viewerID)
	q, args := bound.CatchCountsSQL()
	return pluginSQL(q, typ, fields), args
}

// CompilePluginPolicy rejects catalogue-only conditions instead of silently
// treating an unsupported plugin condition as a non-match. declared maps the
// kind's indexed field keys to their declared types; nil means the library's
// kind is unknown, so only built-in conditions compile. It takes plain types,
// not the plugin declaration, so this package never imports internal/plugins.
func CompilePluginPolicy(specs []core.LibraryExclusion, fallback string, declared map[string]string) (*Filter, error) {
	var browse BrowseCompiler
	if declared != nil {
		types := declared
		browse = func(key string, raw json.RawMessage) (string, []any, error) {
			if _, ok := types[key]; !ok && !envelopeFacet(key) {
				return "", nil, fmt.Errorf("unknown browse condition %q", key)
			}
			values, err := textValues(raw)
			if err != nil { // Facet values travel as text, including numeric/bool fields.
				var scalar any
				if json.Unmarshal(raw, &scalar) != nil || scalar == nil {
					return "", nil, fmt.Errorf("%s needs a facet value", key)
				}
				switch scalar.(type) {
				case float64, bool:
					values = []string{fmt.Sprint(scalar)}
				default:
					return "", nil, err
				}
			}
			if len(values) == 0 || len(values) > 100 {
				return "", nil, fmt.Errorf("%s needs 1–100 values", key)
			}
			for _, value := range values {
				if strings.TrimSpace(value) == "" {
					return "", nil, fmt.Errorf("%s needs a facet value", key)
				}
			}
			var where strings.Builder
			var args []any
			AppendPluginFacetClauses(&where, &args, map[string][]string{key: values}, types)
			return strings.TrimPrefix(where.String(), " AND "), args, nil
		}
	}
	f, err := compilePolicy(specs, fallback, browse)
	if err != nil {
		return nil, MarkInvalidFilter(err)
	}
	var validate func(condition) error
	validate = func(c condition) error {
		if c.field == "viewer" {
			if c.predicate == nil || c.predicate.Group == nil {
				return fmt.Errorf("invalid viewer condition")
			}
			if sf := c.predicate.Group.StateField; sf != "" {
				if col, _ := pluginViewerStateColumn(sf); col == "" {
					return fmt.Errorf("viewer state field %q is only available for catalogue sources", sf)
				}
			}
		} else if c.field == "title" || (c.predicate != nil && c.predicateSQL == "") {
			return fmt.Errorf("condition %q is only available for catalogue sources", c.field)
		}
		for _, child := range c.children {
			if err := validate(child); err != nil {
				return err
			}
		}
		return nil
	}
	for _, g := range f.rules {
		for _, c := range g.rules {
			if err := validate(c); err != nil {
				return nil, MarkInvalidFilter(err)
			}
		}
	}
	return f, nil
}

// ValidatePluginManifestPolicy validates a manifest-declared view tab filter
// against the kind's declared indexed fields (declared) and rule-field aliases
// (ruleFields), plus viewer-state conditions. Unlike owner rules, manifest tabs
// cannot reference built-in fields (such as genre or studio) that are not
// backed by a declared indexed field on the kind.
func ValidatePluginManifestPolicy(specs []core.LibraryExclusion, fallback string, declared map[string]string, ruleFields map[string]string) error {
	f, err := CompilePluginPolicy(specs, fallback, declared)
	if err != nil {
		return err
	}
	var check func(condition) error
	check = func(c condition) error {
		switch c.field {
		case "group":
			for _, child := range c.children {
				if err := check(child); err != nil {
					return err
				}
			}
		case "viewer", "year", "rating":
		default:
			if strings.HasPrefix(c.field, "browse:") {
				return nil
			}
			if ruleFields[c.field] == "" {
				return fmt.Errorf("field %q is not a declared indexed field on this kind", c.field)
			}
		}
		return nil
	}
	for _, g := range f.rules {
		for _, c := range g.rules {
			if err := check(c); err != nil {
				return MarkInvalidFilter(err)
			}
		}
	}
	return nil
}
