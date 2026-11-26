package config

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
)

var ruleIDEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewRuleID returns a fresh opaque rule id: "r_" plus 8 lowercase base32
// characters (40 bits of randomness).
func NewRuleID() string {
	var raw [5]byte
	_, _ = rand.Read(raw[:])
	return "r_" + strings.ToLower(ruleIDEncoding.EncodeToString(raw[:]))
}

// AssignRuleIDs assigns a fresh server id to every rule in q whose ID is empty,
// avoiding collisions with existing ids, and ensures Rules is non-nil.
func AssignRuleIDs(q *Quality) {
	if q == nil {
		return
	}
	if q.Rules == nil {
		q.Rules = []QualityRule{}
		return
	}
	seen := make(map[string]bool, len(q.Rules))
	for _, r := range q.Rules {
		if id := strings.TrimSpace(r.ID); id != "" {
			seen[id] = true
		}
	}
	for i := range q.Rules {
		q.Rules[i].ID = strings.TrimSpace(q.Rules[i].ID)
		if q.Rules[i].ID != "" {
			continue
		}
		for {
			id := NewRuleID()
			if !seen[id] {
				q.Rules[i].ID = id
				seen[id] = true
				break
			}
		}
	}
}

// NormalizedProfileDatabase is the canonical form of a custom Profilarr
// database source accepted by validation and the database check endpoint.
type NormalizedProfileDatabase struct {
	ID         string
	Name       string
	Repo       string
	Branch     string
	ArchiveURL string
}

// NormalizeProfileDatabase validates and normalises a custom Profilarr database
// repository URL and branch:
//   - https://github.com/<owner>/<repo>[.git][/tree/<branch>]
//     strips .git, trailing /, and /tree/<branch> (using that branch when
//     branch is empty, else defaulting to "v2"), and derives the codeload
//     tarball URL;
//   - an https URL ending in .tar.gz or .tgz (or containing /tarball/).
func NormalizeProfileDatabase(repo, branch string) (NormalizedProfileDatabase, error) {
	raw := strings.TrimSpace(repo)
	branch = strings.TrimSpace(branch)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return NormalizedProfileDatabase{}, errors.New("enter https://github.com/<owner>/<repo> or an https .tar.gz URL")
	}
	if strings.EqualFold(u.Host, "github.com") {
		trimmed := strings.Trim(u.Path, "/")
		parts := strings.Split(trimmed, "/")
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			return NormalizedProfileDatabase{}, errors.New("enter https://github.com/<owner>/<repo>")
		}
		owner := parts[0]
		repoName := strings.TrimSuffix(parts[1], ".git")
		if repoName == "" {
			return NormalizedProfileDatabase{}, errors.New("enter https://github.com/<owner>/<repo>")
		}
		switch {
		case len(parts) == 2:
		case len(parts) >= 4 && parts[2] == "tree":
			treeBranch := strings.Join(parts[3:], "/")
			if branch == "" && treeBranch != "" {
				branch = treeBranch
			}
		default:
			return NormalizedProfileDatabase{}, errors.New("enter https://github.com/<owner>/<repo>")
		}
		if branch == "" {
			branch = "v2"
		}
		if strings.ContainsAny(branch, " \t\r\n?#") || strings.Contains(branch, "..") {
			return NormalizedProfileDatabase{}, errors.New("branch name is not valid")
		}
		normRepo := fmt.Sprintf("https://github.com/%s/%s", owner, repoName)
		archiveURL := fmt.Sprintf("https://codeload.github.com/%s/%s/tar.gz/refs/heads/%s",
			url.PathEscape(owner), url.PathEscape(repoName), escapeBranchPath(branch))
		return NormalizedProfileDatabase{
			ID:         slugDatabaseID(owner + "-" + repoName),
			Name:       repoName,
			Repo:       normRepo,
			Branch:     branch,
			ArchiveURL: archiveURL,
		}, nil
	}
	lowerPath := strings.ToLower(u.Path)
	if !strings.HasSuffix(lowerPath, ".tar.gz") && !strings.HasSuffix(lowerPath, ".tgz") && !strings.Contains(lowerPath, "/tarball/") {
		return NormalizedProfileDatabase{}, errors.New("enter https://github.com/<owner>/<repo> or an https .tar.gz URL")
	}
	base := path.Base(u.Path)
	name := strings.TrimSuffix(strings.TrimSuffix(base, ".tar.gz"), ".tgz")
	if name == "" || name == "." || name == "/" {
		name = u.Hostname()
	}
	return NormalizedProfileDatabase{
		ID:         slugDatabaseID(u.Hostname() + "-" + name),
		Name:       name,
		Repo:       raw,
		Branch:     branch,
		ArchiveURL: raw,
	}, nil
}

func escapeBranchPath(branch string) string {
	parts := strings.Split(branch, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func slugDatabaseID(raw string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash && b.Len() > 0 {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// RemoveLibraryFromRules drops libID from every rule's Libraries list, removing
// any rule whose Libraries list becomes empty because it was only for that
// library (§3.4 N16). Returns the IDs of removed rules and whether q changed.
func RemoveLibraryFromRules(q *Quality, libID int64) (removed []string, changed bool) {
	if q == nil || libID <= 0 || len(q.Rules) == 0 {
		return nil, false
	}
	kept := q.Rules[:0]
	for _, r := range q.Rules {
		if len(r.Libraries) == 0 {
			kept = append(kept, r)
			continue
		}
		var next []int64
		hit := false
		for _, id := range r.Libraries {
			if id == libID {
				hit = true
				continue
			}
			next = append(next, id)
		}
		if !hit {
			kept = append(kept, r)
			continue
		}
		changed = true
		if len(next) == 0 {
			removed = append(removed, r.ID)
			continue
		}
		r.Libraries = next
		kept = append(kept, r)
	}
	if changed {
		q.Rules = append([]QualityRule{}, kept...)
	}
	return removed, changed
}

// RemoveViewerFromRules drops viewerID from every rule's Viewers list, removing
// any rule whose Viewers list becomes empty because it was only for that viewer
// (§3.4 N16). Returns the IDs of removed rules and whether q changed.
func RemoveViewerFromRules(q *Quality, viewerID int64) (removed []string, changed bool) {
	if q == nil || viewerID <= 0 || len(q.Rules) == 0 {
		return nil, false
	}
	kept := q.Rules[:0]
	for _, r := range q.Rules {
		if len(r.Viewers) == 0 {
			kept = append(kept, r)
			continue
		}
		var next []int64
		hit := false
		for _, id := range r.Viewers {
			if id == viewerID {
				hit = true
				continue
			}
			next = append(next, id)
		}
		if !hit {
			kept = append(kept, r)
			continue
		}
		changed = true
		if len(next) == 0 {
			removed = append(removed, r.ID)
			continue
		}
		r.Viewers = next
		kept = append(kept, r)
	}
	if changed {
		q.Rules = append([]QualityRule{}, kept...)
	}
	return removed, changed
}

// ReplaceViewerInRules replaces fromID with toID in every rule's Viewers list
// (deduplicating if toID is already listed), so merging viewers never leaves a
// dangling viewer ID in quality rules.
func ReplaceViewerInRules(q *Quality, fromID, toID int64) bool {
	if q == nil || fromID <= 0 || toID <= 0 || fromID == toID || len(q.Rules) == 0 {
		return false
	}
	changed := false
	for i := range q.Rules {
		vs := q.Rules[i].Viewers
		if len(vs) == 0 {
			continue
		}
		hasFrom, hasTo := false, false
		for _, id := range vs {
			if id == fromID {
				hasFrom = true
			}
			if id == toID {
				hasTo = true
			}
		}
		if !hasFrom {
			continue
		}
		changed = true
		next := make([]int64, 0, len(vs))
		for _, id := range vs {
			if id == fromID {
				if !hasTo {
					next = append(next, toID)
					hasTo = true
				}
				continue
			}
			next = append(next, id)
		}
		q.Rules[i].Viewers = next
	}
	return changed
}
