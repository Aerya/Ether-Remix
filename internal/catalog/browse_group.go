package catalog

import (
	"fmt"
	"github.com/braedonsaunders/ether/internal/core"
	"strings"
)

func (c *Catalog) compileBrowseGroup(g *core.FilterGroup, alias string, kind core.KeyKind, rootType core.MediaType) (string, []any, error) {
	if g.ParentOnly && kind != 0 {
		copy := *g
		copy.ParentOnly = false
		return c.compileBrowseGroup(&copy, "t", 0, core.TypeShow)
	}
	if g.OnWatchlist != nil || g.Watchlist != nil || g.Favorite != nil || g.StateField != "" ||
		g.Watched != nil || g.Unwatched != nil || g.InProgress != nil || g.History != nil ||
		g.LastViewedAfter > 0 || g.LastViewedBefore > 0 {
		var parts []string
		var args []any

		addWatch := func(cond string, want bool) {
			if g.UserID <= 0 {
				if want {
					parts = append(parts, "1=0")
				} else {
					parts = append(parts, "1=1")
				}
				return
			}
			if want {
				parts = append(parts, alias+".key IN (SELECT w.rating_key FROM state_watch w WHERE w.user_id=? AND "+cond+")")
			} else {
				parts = append(parts, "NOT EXISTS (SELECT 1 FROM state_watch w WHERE w.user_id=? AND w.rating_key="+alias+".key AND "+cond+")")
			}
			args = append(args, g.UserID)
		}

		addWatchlist := func(want bool) {
			if g.UserID <= 0 {
				if want {
					parts = append(parts, "1=0")
				} else {
					parts = append(parts, "1=1")
				}
				return
			}
			if want {
				parts = append(parts, alias+".key IN (SELECT w.item_key FROM watchlist_items w WHERE w.user_id=?)")
			} else {
				parts = append(parts, "NOT EXISTS (SELECT 1 FROM watchlist_items w WHERE w.user_id=? AND w.item_key="+alias+".key)")
			}
			args = append(args, g.UserID)
		}

		if g.OnWatchlist != nil {
			addWatchlist(*g.OnWatchlist)
		}
		if g.Watchlist != nil {
			addWatchlist(*g.Watchlist)
		}
		if g.Favorite != nil {
			if g.UserID <= 0 {
				if *g.Favorite {
					parts = append(parts, "1=0")
				} else {
					parts = append(parts, "1=1")
				}
			} else if *g.Favorite {
				parts = append(parts, alias+".key IN (SELECT CAST(fav.subject_id AS BIGINT) FROM favorites_items fav WHERE fav.viewer_id=? AND fav.kind='title')")
				args = append(args, g.UserID)
			} else {
				parts = append(parts, "NOT EXISTS (SELECT 1 FROM favorites_items fav WHERE fav.viewer_id=? AND fav.kind='title' AND fav.subject_id=CAST("+alias+".key AS TEXT))")
				args = append(args, g.UserID)
			}
		}
		addWatched := func(want bool) {
			if g.UserID <= 0 {
				if want {
					parts = append(parts, "1=0")
				} else {
					parts = append(parts, "1=1")
				}
				return
			}
			clause, wArgs := c.watchedPredicateSQL(alias, kind, rootType, g.UserID, want)
			parts = append(parts, clause)
			args = append(args, wArgs...)
		}
		if g.Watched != nil {
			addWatched(*g.Watched)
		}
		if g.Unwatched != nil {
			addWatched(!*g.Unwatched)
		}
		if g.InProgress != nil {
			addWatch("w.view_offset_ns>0", *g.InProgress)
		}
		if g.History != nil {
			addWatch("(w.last_viewed_at>0 OR w.view_count>0 OR w.view_offset_ns>0)", *g.History)
		}
		if g.LastViewedAfter > 0 || g.LastViewedBefore > 0 {
			if g.UserID <= 0 {
				parts = append(parts, "1=0")
			} else {
				p := alias + ".key IN (SELECT w.rating_key FROM state_watch w WHERE w.user_id=? AND w.last_viewed_at>0"
				args = append(args, g.UserID)
				// state_watch timestamps are nanoseconds; Plex filter bounds
				// and unixOrZero use whole seconds.
				const maxSeconds = int64(9223372036)
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
			if g.StateField == "viewedLeafCount" || g.StateField == "viewedOrInProgressLeafCount" || g.StateField == "unviewedLeafCount" || g.StateField == "unwatchedLeaves" {
				op := ""
				switch g.StateOperator {
				case "=", "==":
					op = "="
				case "!=", "!==":
					op = "<>"
				case "<<", "<":
					op = "<"
				case "<<=", "<=":
					op = "<="
				case ">>", ">":
					op = ">"
				case ">>=", ">=":
					op = ">="
				default:
					return "", nil, fmt.Errorf("invalid leaf operator")
				}
				predicate := "COALESCE(w.view_count,0)>0"
				if g.StateField == "viewedOrInProgressLeafCount" {
					predicate = "(COALESCE(w.view_count,0)>0 OR COALESCE(w.view_offset_ns,0)>0)"
				}
				if g.StateField == "unviewedLeafCount" || g.StateField == "unwatchedLeaves" {
					predicate = "COALESCE(w.view_count,0)=0"
				}
				seasonClause := " AND (" + alias + ".type IN ('show','episode') OR e.season=" + alias + ".season)"
				if g.StateField == "unwatchedLeaves" {
					seasonClause = ""
				}
				col := "(SELECT COUNT(*) FROM catalog_titles e LEFT JOIN state_watch w ON w.rating_key=e.key AND w.user_id=? WHERE e.show_key=CASE WHEN " + alias + ".type='show' THEN " + alias + ".key ELSE " + alias + ".show_key END AND e.type='episode' AND e.season>0" + seasonClause + " AND e.adult=0 AND e.release_known=1 AND e.released_ts<=? AND " + predicate + ")"
				value := g.StateValue
				if g.StateField == "unwatchedLeaves" {
					if value == 1 {
						op = ">"
					} else {
						op = "="
					}
					value = 0
				}
				parts = append(parts, col+op+"?")
				args = append(args, g.UserID, c.now().Unix(), value)
			} else {
				column := ""
				scale := int64(1)
				switch g.StateField {
				case "viewCount":
					column = "w.view_count"
				case "skipCount":
					column = "0"
				case "viewOffset":
					column = "w.view_offset_ns"
					scale = 1000000
				case "lastViewedAt":
					column = "w.last_viewed_at/1000000000"
				case "lastRatedAt":
					column = "w.rating_at/1000000000"
				case "lastSkippedAt":
					column = "0"
				default:
					return "", nil, fmt.Errorf("invalid state field")
				}
				op := ""
				switch g.StateOperator {
				case "=", "==":
					op = "="
				case "!=", "!==":
					op = "<>"
				case "<<", "<":
					op = "<"
				case "<<=", "<=":
					op = "<="
				case ">>", ">":
					op = ">"
				case ">>=", ">=":
					op = ">="
				default:
					return "", nil, fmt.Errorf("invalid state operator")
				}
				if g.StateField == "viewOffset" || g.StateField == "lastViewedAt" || g.StateField == "lastRatedAt" || g.StateField == "lastSkippedAt" {
					parts = append(parts, "COALESCE((SELECT "+column+" FROM state_watch w WHERE w.user_id=? AND w.rating_key="+alias+".key),0)"+op+"?")
					args = append(args, g.UserID, g.StateValue*scale)
				} else {
					parts = append(parts, "EXISTS (SELECT 1 FROM state_watch w WHERE w.user_id=? AND w.rating_key="+alias+".key AND "+column+op+"?)")
					args = append(args, g.UserID, g.StateValue)
				}
			}
		}
		return strings.Join(parts, " AND "), args, nil
	}
	if g.Filter != nil {
		f := *g.Filter
		if kind == 0 {
			return c.browseWhere(f)
		}
		show, excluded := splitStoredFilter(f, kind)
		sql, args, err := c.browseWhere(show)
		if err != nil {
			return "", nil, err
		}
		child, vals := c.childFilterConds(alias, f, excluded)
		if len(child) > 0 {
			sql += " AND " + strings.Join(child, " AND ")
			args = append(args, vals...)
		}
		return sql, args, nil
	}
	if g.Operator != "and" && g.Operator != "or" {
		return "", nil, fmt.Errorf("catalog: invalid filter group operator %q", g.Operator)
	}
	parts := make([]string, 0, len(g.Children))
	var args []any
	for i := range g.Children {
		sql, vals, err := c.compileBrowseGroup(&g.Children[i], alias, kind, rootType)
		if err != nil {
			return "", nil, err
		}
		parts = append(parts, "("+sql+")")
		args = append(args, vals...)
	}
	if len(parts) == 0 {
		return "1=1", nil, nil
	}
	op := " AND "
	if g.Operator == "or" {
		op = " OR "
	}
	return strings.Join(parts, op), args, nil
}
