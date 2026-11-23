package plugins

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"testing"
)

func TestFrontendArchiveDelivery(t *testing.T) {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	w, _ := z.Create("firmware.bin")
	_, _ = io.WriteString(w, "0123456789")
	_ = z.Close()
	r, size, err := OpenFrontendArchiveMember(context.Background(), bytes.NewReader(b.Bytes()), 0, defaultDataLimits())
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(r)
	r.Close()
	if err != nil || size != 10 || string(data) != "0123456789" {
		t.Fatal(size, string(data), err)
	}
	if r, _, err := OpenFrontendArchiveMember(context.Background(), bytes.NewReader(b.Bytes()), 1, defaultDataLimits()); err == nil {
		r.Close()
		t.Fatal("invalid member accepted")
	}
	limits := defaultDataLimits()
	limits.ArchiveMemberBytes = 3
	if r, _, err := OpenFrontendArchiveMember(context.Background(), bytes.NewReader(b.Bytes()), 0, limits); err == nil {
		r.Close()
		t.Fatal("member limit bypass")
	}
}
