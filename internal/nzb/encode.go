package nzb

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// Subset returns the NZB restricted to the files keep accepts, keeping the
// document metadata (such as the password). Parsing the result gives the same
// file names, segments and repost alternates as the originals. ok is false
// when no file is kept.
func (n *NZB) Subset(keep func(*File) bool) (data []byte, ok bool) {
	return n.SubsetMeta(keep, nil)
}

// SubsetMeta is Subset with extra <meta> values added (type -> value). The
// extra values make the subset a different document, with its own digest.
func (n *NZB) SubsetMeta(keep func(*File) bool, extra map[string]string) (data []byte, ok bool) {
	var files []*File
	for _, f := range n.Files {
		if keep(f) {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return nil, false
	}
	subjects := make([]string, len(files))
	var subjectNames map[int]string
	for i, f := range files {
		subj := subjectFor(f)
		subjects[i] = subj
		if orig := f.OriginalName(); orig != "" && NameFromSubject(subj) != orig {
			if subjectNames == nil {
				subjectNames = make(map[int]string)
			}
			subjectNames[i] = orig
		}
	}
	metaExtra := extra
	if len(subjectNames) > 0 {
		if encoded, err := json.Marshal(subjectNames); err == nil {
			metaExtra = maps.Clone(extra)
			if metaExtra == nil {
				metaExtra = make(map[string]string, 1)
			}
			metaExtra[MetaSubjectNames] = string(encoded)
		}
	}
	var keptMetaKeys []string
	for _, typ := range slices.Sorted(maps.Keys(n.Meta)) {
		if typ == MetaSubjectNames {
			continue
		}
		if _, replaced := metaExtra[typ]; replaced {
			continue
		}
		keptMetaKeys = append(keptMetaKeys, typ)
	}
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n<nzb xmlns=\"http://www.newzbin.com/DTD/2003/nzb\">\n")
	if len(keptMetaKeys) > 0 || len(metaExtra) > 0 {
		b.WriteString("<head>\n")
		// Sorted, so one input always gives the same bytes: the stream
		// engine keys release state, plans and cached segments by the
		// digest of the document.
		for _, typ := range keptMetaKeys {
			for _, v := range n.Meta[typ] {
				b.WriteString(`<meta type="` + escAttr(typ) + `">` + escText(v) + "</meta>\n")
			}
		}
		for _, typ := range slices.Sorted(maps.Keys(metaExtra)) {
			b.WriteString(`<meta type="` + escAttr(typ) + `">` + escText(metaExtra[typ]) + "</meta>\n")
		}
		b.WriteString("</head>\n")
	}
	for i, f := range files {
		b.WriteString(`<file poster="` + escAttr(f.Poster) + `" subject="` + escAttr(subjects[i]) + `"`)
		if !f.Date.IsZero() {
			b.WriteString(` date="` + strconv.FormatInt(f.Date.Unix(), 10) + `"`)
		}
		b.WriteString(">\n<groups>")
		for _, g := range f.Groups {
			b.WriteString("<group>" + escText(g) + "</group>")
		}
		b.WriteString("</groups>\n<segments>")
		for _, s := range f.Segments {
			for _, id := range append([]string{s.MessageID}, s.Alternates...) {
				b.WriteString(`<segment bytes="` + strconv.FormatInt(s.Bytes, 10) + `" number="` + strconv.Itoa(s.Number) + `">` + escText(id) + "</segment>")
			}
		}
		b.WriteString("</segments>\n</file>\n")
	}
	b.WriteString("</nzb>\n")
	return b.Bytes(), true
}

func escText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func escAttr(s string) string { return escText(s) }

// subjectFor is the subject to write for f. A name resolved after parsing
// (from PAR2 or a yEnc header) is written into the subject, so parsing the
// subset gives that name without the original probing.
func subjectFor(f *File) string {
	if f.Name == "" || f.Name == NameFromSubject(f.Subject) {
		return f.Subject
	}
	return `"` + f.Name + `" yEnc (1/` + strconv.Itoa(f.SegmentCount()) + `)`
}
