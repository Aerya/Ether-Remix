package releaseparse

import (
	"path"
	"strings"
)

// MovieFileMatches requires an exact parsed title, never a prefix that can
// admit sequels. A year carried by the file or its nearest titled folder must
// agree too. Missing years still undergo playback's probed runtime check.
func MovieFileMatches(name string, titles []string, year int) bool {
	name = strings.ReplaceAll(name, `\`, "/")
	base := path.Base(name)
	info := Parse(strings.TrimSuffix(base, path.Ext(base)))
	matches := func(title string) bool {
		for _, want := range titles {
			if want != "" && NormalizeTitle(title) == NormalizeTitle(want) {
				return true
			}
		}
		return false
	}
	if !matches(info.Title) {
		return false
	}
	if info.Year == 0 {
		for dir := path.Dir(name); dir != "." && dir != "/"; dir = path.Dir(dir) {
			parent := Parse(path.Base(dir))
			if matches(parent.Title) && parent.Year != 0 {
				info.Year = parent.Year
				break
			}
		}
	}
	return year == 0 || info.Year == 0 || info.Year == year
}
