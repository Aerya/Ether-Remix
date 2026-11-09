package console

import (
	"strings"
	"testing"
)

func TestSFRURLValidationAndMask(t *testing.T) {
	fd := fieldByKey["PSE_SFR_URL"]
	in := "postgres://lecture:secret@db:5432/streamfusion"
	got, err := validate(fd, in, nil)
	if err != nil || got != in {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := validate(fd, "https://db/x", nil); err == nil {
		t.Fatal("adresse http acceptée")
	}
	shown := display(fd, in)
	if strings.Contains(shown, "secret") || strings.Contains(shown, "lecture") {
		t.Fatalf("secret affiché : %s", shown)
	}
	if back := restore(fd, shown, in); back != in {
		t.Fatalf("restauration : %s", back)
	}
}
