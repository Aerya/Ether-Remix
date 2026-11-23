package frontend

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/braedonsaunders/ether/internal/core"
)

// SubsetParents maps each rule/catalogue/collection subset video library view ID
// to the view ID of the broader parent library of the same media type present in
// views. Built-in libraries (Movies/TV Shows) and plugin libraries never have a
// parent. Two sibling rule subsets without a broader parent are not linked.
func SubsetParents(views []View) map[string]string {
	builtinParent := map[core.MediaType]string{}
	unscopedParent := map[core.MediaType]string{}
	for _, v := range views {
		if !isCatalogueVideoView(v) {
			continue
		}
		libID := viewLibraryID(v)
		if core.IsBuiltinLibrary(libID) {
			if builtinParent[v.Type] == "" {
				builtinParent[v.Type] = v.ID
			}
			continue
		}
		if !v.Subset && unscopedParent[v.Type] == "" {
			unscopedParent[v.Type] = v.ID
		}
	}
	var parents map[string]string
	for _, v := range views {
		if !isCatalogueVideoView(v) {
			continue
		}
		libID := viewLibraryID(v)
		if core.IsBuiltinLibrary(libID) {
			continue
		}
		parentID := builtinParent[v.Type]
		if parentID == "" && v.Subset {
			parentID = unscopedParent[v.Type]
		}
		if parentID == "" || parentID == v.ID {
			continue
		}
		if parents == nil {
			parents = map[string]string{}
		}
		parents[v.ID] = parentID
	}
	return parents
}

func isCatalogueVideoView(v View) bool {
	return v.ID != "" && v.PluginID == "" && v.KindKey == "" && (v.Type == core.TypeMovie || v.Type == core.TypeShow)
}

func viewLibraryID(v View) core.LibraryID {
	if v.LibraryID > 0 {
		return v.LibraryID
	}
	n, err := strconv.ParseInt(v.ID, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return core.LibraryID(n)
}

// SubsetRowDistinct reports whether a subset library's row differs meaningfully
// from its parent library's Home rows. A row differs meaningfully when it is
// non-empty and strictly more than half of its unique items are absent from both
// the parent's matching row (parentSameRow) and the parent's overall shown Home
// items (parentAll).
func SubsetRowDistinct[K comparable](subsetKeys []K, parentSameRow, parentAll map[K]bool) bool {
	if len(subsetKeys) == 0 {
		return false
	}
	seen := make(map[K]struct{}, len(subsetKeys))
	total := 0
	sameOverlap := 0
	allOverlap := 0
	for _, k := range subsetKeys {
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		total++
		if parentSameRow[k] {
			sameOverlap++
		}
		if parentAll[k] {
			allOverlap++
		}
	}
	if total == 0 {
		return false
	}
	overlap := max(sameOverlap, allOverlap)
	distinct := total - overlap
	return distinct*2 > total
}

// QualifyLibraryRowTitle distinguishes a row belonging to a secondary library on
// a multi-library Home screen by appending " · <libraryName>", unless the row
// title already names that library.
func QualifyLibraryRowTitle(title, libraryName string) string {
	title = strings.TrimSpace(title)
	libraryName = strings.TrimSpace(libraryName)
	if title == "" || libraryName == "" {
		return title
	}
	if TitleMentionsLibrary(title, libraryName) {
		return title
	}
	return title + " · " + libraryName
}

// TitleMentionsLibrary reports whether title already contains libraryName as a
// case-insensitive standalone phrase (bounded by non-alphanumeric characters or
// string edges).
func TitleMentionsLibrary(title, libraryName string) bool {
	lt := []rune(strings.ToLower(strings.TrimSpace(title)))
	ln := []rune(strings.ToLower(strings.TrimSpace(libraryName)))
	if len(lt) == 0 || len(ln) == 0 || len(ln) > len(lt) {
		return false
	}
	isWord := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
	for i := 0; i+len(ln) <= len(lt); i++ {
		if i > 0 && isWord(lt[i-1]) {
			continue
		}
		if i+len(ln) < len(lt) && isWord(lt[i+len(ln)]) {
			continue
		}
		match := true
		for j := range ln {
			if lt[i+j] != ln[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// AllocateRowBudget distributes budget fairly across sections with available
// row counts lengths using max-min water-filling, favoring earlier sections on
// remainders. Each cap satisfies 0 <= caps[i] <= lengths[i] and sum(caps) <= budget.
func AllocateRowBudget(lengths []int, budget int) []int {
	caps := make([]int, len(lengths))
	if budget <= 0 || len(lengths) == 0 {
		return caps
	}
	remaining := budget
	for remaining > 0 {
		progressed := false
		for i, n := range lengths {
			if remaining == 0 {
				break
			}
			if caps[i] < n {
				caps[i]++
				remaining--
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}
	return caps
}
