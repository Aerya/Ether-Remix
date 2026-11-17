package collections

import (
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
)

type releaseSourceKind struct{}

func (releaseSourceKind) ID() string { return "releaseSource" }
func (releaseSourceKind) Describe() KindDescribe {
	return KindDescribe{Label: extend.T("Indexed release source"), Hint: extend.T("Titles already observed on this source; no playback or download"), Params: []extend.ParamField{{Name: "label", Kind: "text", Label: extend.T("Entry"), Required: true}}, Defaults: `{}`, MediaTypes: []string{"movie", "show", "mixed"}}
}
func (releaseSourceKind) Validate(ve *ValidationError, path string, src core.CollectionSource) {
	if src.Label == "" {
		ve.add(path, "source identifier required")
	}
}
func (releaseSourceKind) Gather(g *Gather) {
	keys, err := releasedb.LibraryKeys(g.Ctx, g.Svc.db, g.Src.Label)
	if err != nil {
		g.Fail(err)
		return
	}
	g.Res.keys = keys
}
