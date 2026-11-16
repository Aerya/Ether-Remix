package app

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/localmedia"
)

type localDestination struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	MediaType string `json:"mediaType"`
	Linked    bool   `json:"linked"`
}
type localDestinationEdit struct {
	RootID string `json:"rootId"`
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Linked bool   `json:"linked"`
}

func editableLocalDestination(sp core.CollectionSpec, viewer int64) bool {
	return sp.ReleaseMode != "upcoming" && sp.ReleaseMode != "mixed" && !sp.BuiltIn && !strings.HasPrefix(sp.TemplateID, "remix-local-") && (sp.OwnerUserID == 0 || sp.OwnerUserID == viewer)
}
func hasLocalSource(sp core.CollectionSpec, root string) bool {
	return slices.ContainsFunc(sp.Sources, func(src core.CollectionSource) bool { return src.Kind == "localFiles" && src.Label == root })
}
func (a *App) localDestinations(ctx context.Context, id string) ([]localDestination, error) {
	out := []localDestination{}
	err := pseRuntimeOf(a).local.Configure(ctx, id, func(root localmedia.Root) error {
		if a.collections == nil || a.pseThemes() == nil {
			return errors.New("collections indisponibles")
		}
		specs, err := a.collections.ListSpecsLight(ctx)
		if err != nil {
			return err
		}
		own := ""
		for _, sp := range specs {
			if sp.TemplateID == "remix-local-"+root.ID {
				own = sp.ID
			}
			if editableLocalDestination(sp, core.CatalogViewerID(ctx)) {
				out = append(out, localDestination{sp.ID, sp.Name, "collection", sp.MediaType, hasLocalSource(sp, root.ID)})
			}
		}
		if own == "" {
			return errors.New("analyse initiale du dossier requise")
		}
		for _, lib := range a.pseThemes().Libraries() {
			out = append(out, localDestination{strconv.FormatInt(lib.ID, 10), lib.Name, "library", lib.Type, slices.Contains(lib.Collections, own)})
		}
		return nil
	})
	return out, err
}
func (a *App) editLocalDestination(ctx context.Context, root localmedia.Root, edit localDestinationEdit) error {
	if a.collections == nil || a.pseThemes() == nil {
		return errors.New("collections indisponibles")
	}
	specs, err := a.collections.ListSpecsLight(ctx)
	if err != nil {
		return err
	}
	switch edit.Kind {
	case "collection":
		for _, sp := range specs {
			if sp.ID != edit.ID || !editableLocalDestination(sp, core.CatalogViewerID(ctx)) {
				continue
			}
			if hasLocalSource(sp, root.ID) == edit.Linked {
				return nil
			}
			if edit.Linked {
				sp.Sources = append(sp.Sources, core.CollectionSource{Kind: "localFiles", Label: root.ID})
			} else {
				sp.Sources = slices.DeleteFunc(sp.Sources, func(src core.CollectionSource) bool { return src.Kind == "localFiles" && src.Label == root.ID })
				if len(sp.Sources) == 0 {
					return errors.New("cette collection dépend uniquement de ce dossier ; modifiez ses sources dans Collections avant de le détacher")
				}
			}
			if _, err = a.collections.UpdateSpec(ctx, sp.ID, sp); err != nil {
				return err
			}
			return a.pseThemes().Reload(ctx)
		}
	case "library":
		own := ""
		for _, sp := range specs {
			if sp.TemplateID == "remix-local-"+root.ID {
				own = sp.ID
				break
			}
		}
		if own == "" {
			return errors.New("analyse initiale du dossier requise")
		}
		for _, lib := range a.pseThemes().Libraries() {
			if strconv.FormatInt(lib.ID, 10) != edit.ID {
				continue
			}
			if slices.Contains(lib.Collections, own) != edit.Linked {
				if edit.Linked {
					lib.Collections = append(lib.Collections, own)
				} else {
					lib.Collections = slices.DeleteFunc(lib.Collections, func(id string) bool { return id == own })
				}
				// Remember an explicit choice before saving: future scans must not
				// silently recreate a dedicated library after the final unlink.
				if _, err = a.db.ExecContext(ctx, "INSERT INTO pse_local_destinations(root_id) VALUES(?) ON CONFLICT DO NOTHING", root.ID); err != nil {
					return err
				}
				if len(lib.Collections) == 0 {
					return a.pseThemes().Delete(ctx, lib.ID)
				}
				if _, err = a.pseThemes().Update(ctx, lib); err != nil {
					return err
				}
			}
			return nil
		}
	}
	return errors.New("destination introuvable ou non modifiable")
}
