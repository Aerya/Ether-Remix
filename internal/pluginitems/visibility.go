package pluginitems

import (
	"context"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/libraryfilter"
	"github.com/braedonsaunders/ether/internal/plugins"
)

// RuleFields exposes only rule facts backed by indexed storage.
func RuleFields(decl plugins.MediaKindMeta) map[string]string {
	return plugins.KindRuleFields(decl.Fields)
}

func (s *Service) policySQL(ctx context.Context, libraryID core.LibraryID, policy *libraryfilter.Filter) (string, []any, error) {
	return s.policySQLForViewer(ctx, libraryID, policy, true, core.CatalogViewerID(ctx))
}

func (s *Service) policySQLForViewer(ctx context.Context, libraryID core.LibraryID, policy *libraryfilter.Filter, hidden bool, viewerID int64) (string, []any, error) {
	if policy.Passive() {
		if hidden {
			return "0=1", nil, nil
		}
		return "1=1", nil, nil
	}
	rec, err := s.GetKindByLibrary(ctx, libraryID)
	if err != nil {
		return "", nil, err
	}
	typ := core.TypeMovie
	if len(rec.Decl.Levels) > 1 {
		typ = core.TypeShow
	}
	if hidden {
		q, args := policy.PluginSQLHiddenForViewer(typ, RuleFields(rec.Decl), viewerID)
		return q, args, nil
	}
	q, args := policy.PluginSQLShownForViewer(typ, RuleFields(rec.Decl), viewerID)
	return q, args, nil
}

func (s *Service) appendVisibility(ctx context.Context, where *strings.Builder, args *[]any, lib core.LibraryID, policy *libraryfilter.Filter, hidden bool) error {
	return s.appendVisibilityForViewer(ctx, where, args, lib, policy, hidden, core.CatalogViewerID(ctx))
}

func (s *Service) appendVisibilityForViewer(ctx context.Context, where *strings.Builder, args *[]any, lib core.LibraryID, policy *libraryfilter.Filter, hidden bool, viewerID int64) error {
	if policy.Passive() && !hidden {
		return nil
	}
	q, a, err := s.policySQLForViewer(ctx, lib, policy, hidden, viewerID)
	if err != nil {
		return err
	}
	where.WriteString(" AND (")
	where.WriteString(q)
	where.WriteString(")")
	*args = append(*args, a...)
	return nil
}

// PreviewVisibility aggregates in the indexed item store and fetches at most
// twelve examples per side. It never projects or loads the whole library.
func (s *Service) PreviewVisibility(ctx context.Context, lib core.LibraryID, specs []core.LibraryExclusion, fallback string) (any, error) {
	keptSpecs, keptIndices := libraryfilter.PreviewableRules(specs)
	policy, err := s.CompilePolicy(ctx, lib, keptSpecs, fallback)
	if err != nil {
		return nil, err
	}
	rec, err := s.GetKindByLibrary(ctx, lib)
	if err != nil {
		return nil, err
	}
	level := ""
	if len(rec.Decl.Levels) > 0 {
		level = rec.Decl.Levels[0].Key
	}
	hidden, err := s.Browse(ctx, BrowseQuery{LibraryID: lib, Level: level, Policy: policy, Hidden: true, Limit: 12})
	if err != nil {
		return nil, err
	}
	shown, err := s.Browse(ctx, BrowseQuery{LibraryID: lib, Level: level, Policy: policy, Limit: 12})
	if err != nil {
		return nil, err
	}
	project := func(items []core.KindItem) []core.Title {
		out := make([]core.Title, 0, len(items))
		for _, it := range items {
			out = append(out, Project(it, rec.Decl, rec.Decl.Present))
		}
		return out
	}
	typ := core.TypeMovie
	if len(rec.Decl.Levels) > 1 {
		typ = core.TypeShow
	}
	counts := make([]map[string]int, len(specs))
	for i := range counts {
		counts[i] = map[string]int{"movies": 0, "shows": 0}
	}
	if columns, args := policy.PluginCatchCountsSQLForViewer(typ, RuleFields(rec.Decl), core.CatalogViewerID(ctx)); columns != "" {
		args = append(args, int64(lib), level)
		values := make([]int, len(keptSpecs)*2)
		dest := make([]any, len(values))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM plugin_items i WHERE i.library_id=? AND i.level=?", args...).Scan(dest...); err != nil {
			return nil, err
		}
		for i, orig := range keptIndices {
			counts[orig] = map[string]int{"movies": values[i*2], "shows": values[i*2+1]}
		}
	}
	movies, shows := hidden.Total, 0
	if typ == core.TypeShow {
		movies, shows = 0, hidden.Total
	}
	return map[string]any{"total": hidden.Total + shown.Total, "hidden": hidden.Total, "visible": shown.Total, "movies": movies, "shows": shows, "rules": counts, "items": project(hidden.Items), "shown": project(shown.Items)}, nil
}

func (s *Service) LibraryPolicy(ctx context.Context, lib core.Library) (*libraryfilter.Filter, error) {
	if len(lib.Rules) == 0 && lib.VisibilityDefault != "hide" {
		return nil, nil
	}
	f, err := s.CompilePolicy(ctx, lib.ID, lib.Rules, lib.VisibilityDefault)
	if err == nil && core.ExplicitSearch(ctx) {
		f = f.ForSearch()
	}
	return f, err
}

func (s *Service) ItemHidden(ctx context.Context, lib core.Library, ref core.ItemRef) (bool, error) {
	policy, err := s.LibraryPolicy(ctx, lib)
	if err != nil {
		return false, err
	}
	if policy.Passive() {
		return false, nil
	}
	q, args, err := s.policySQLForViewer(ctx, lib.ID, policy, true, core.CatalogViewerID(ctx))
	if err != nil {
		return false, err
	}
	args = append(args, int64(lib.ID), string(ref))
	var hidden int
	err = s.db.QueryRowContext(ctx, `SELECT CASE WHEN (`+q+`) THEN 1 ELSE 0 END FROM plugin_items i WHERE i.library_id=? AND i.item_ref=?`, args...).Scan(&hidden)
	return hidden != 0, err
}

// CompilePolicy resolves the declaration for this library before validating rules.
func (s *Service) CompilePolicy(ctx context.Context, lib core.LibraryID, specs []core.LibraryExclusion, fallback string) (*libraryfilter.Filter, error) {
	rec, err := s.GetKindByLibrary(ctx, lib)
	if err != nil {
		return nil, err
	}
	declared := map[string]string{}
	for _, field := range rec.Decl.Fields {
		if field.Indexed || field.Type == "people" {
			declared[field.Key] = field.Type
		}
	}
	return libraryfilter.CompilePluginPolicy(specs, fallback, declared)
}
