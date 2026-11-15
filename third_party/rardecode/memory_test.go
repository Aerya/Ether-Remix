package rardecode

import (
	"bytes"
	"errors"
	"testing"
)

// Ether's grant must cover PPMd independently of the LZ dictionary.
func TestMemoryGrant(t *testing.T) {
	d := newPPM29Decoder()
	err := d.init(newRarBitReader(bytes.NewReader([]byte{0x40, 0xff, 0xff, 0xff})), 1<<20)
	if !errors.Is(err, ErrDictionaryTooLarge) {
		t.Fatalf("PPMd memory grant: %v", err)
	}
}
