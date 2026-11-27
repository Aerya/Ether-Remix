package plugins

import (
	"errors"
	"fmt"
	"sort"

	"github.com/braedonsaunders/ether/internal/plugins/screendoc"
)

// ScreenDocuments are a plugin's compiled screen and surface documents.
type ScreenDocuments struct {
	// Screens holds each screen's pages, each page's variants ordered by
	// renderer, highest first.
	Screens map[string]map[string][]*screendoc.Compiled
	// Surfaces holds each surface's variants, highest renderer first.
	Surfaces map[string][]*screendoc.Compiled
}

// CompileScreenDocuments compiles every document the manifest names from the
// package files. It is the install-time check and the serving source, so a
// document that installs is exactly the document clients get.
func CompileScreenDocuments(man Manifest, files map[string][]byte, english func(key string) (string, bool)) (ScreenDocuments, error) {
	out := ScreenDocuments{Screens: map[string]map[string][]*screendoc.Compiled{}, Surfaces: map[string][]*screendoc.Compiled{}}
	if man.Ether == nil || !ScreensUseDocuments(man) {
		return out, nil
	}
	c := man.Ether.Contributes
	opts := screendoc.Options{
		Asset: func(p string) bool { _, ok := files[p]; return ok },
		Text:  english,
	}
	for _, f := range c.LiveFeeds {
		opts.Feeds = append(opts.Feeds, f.ID)
	}
	for _, k := range c.MediaKinds {
		opts.Kinds = append(opts.Kinds, k.Key)
	}
	var fields []FieldError
	compile := func(field string, paths DocumentPaths, presentation string) []*screendoc.Compiled {
		var docs []*screendoc.Compiled
		renderers := map[int]bool{}
		for _, path := range paths {
			raw, ok := files[path]
			if !ok {
				fields = append(fields, FieldError{field, fmt.Sprintf("the document %s is missing from the package", path)})
				continue
			}
			o := opts
			o.Presentation = presentation
			doc, err := screendoc.Compile(path, raw, o)
			var docErr *screendoc.Error
			if errors.As(err, &docErr) {
				for _, fe := range docErr.Fields {
					fields = append(fields, FieldError{path + "#" + fe.Pointer, fe.Message})
				}
				continue
			} else if err != nil {
				fields = append(fields, FieldError{path, err.Error()})
				continue
			}
			if renderers[doc.Renderer] {
				fields = append(fields, FieldError{field, fmt.Sprintf("two variants need renderer %d; variants need different renderers", doc.Renderer)})
				continue
			}
			renderers[doc.Renderer] = true
			docs = append(docs, doc)
		}
		sort.Slice(docs, func(i, j int) bool { return docs[i].Renderer > docs[j].Renderer })
		return docs
	}
	for i, sc := range c.Screens {
		if len(sc.Pages) == 0 {
			continue
		}
		pages := map[string][]*screendoc.Compiled{}
		for page, paths := range sc.Pages {
			presentation := screendoc.PresentPage
			if page == "index" {
				presentation = screendoc.PresentSection
			}
			pages[page] = compile(fmt.Sprintf("ether.contributes.screens[%d].pages.%s", i, page), paths, presentation)
		}
		out.Screens[sc.ID] = pages
	}
	for i, sf := range c.Surfaces {
		out.Surfaces[sf.ID] = compile(fmt.Sprintf("ether.contributes.surfaces[%d].document", i), sf.Document, sf.Presentation)
	}
	if len(fields) > 0 {
		return ScreenDocuments{}, &ValidationError{fields}
	}
	return out, nil
}
