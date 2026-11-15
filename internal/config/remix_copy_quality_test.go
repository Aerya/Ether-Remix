package config

import (
	"encoding/json"
	"testing"
)

func TestRemixCopyQualityDefaultsAndSavedPreferences(t *testing.T) {
	c := Defaults(t.TempDir())
	if c.Transcode.ABRLadderUnderCopy {
		t.Fatal("video-copy sessions should not offer encoded fallbacks by default")
	}
	if err := json.Unmarshal([]byte(`{"transcode":{"abrLadderUnderCopy":true,"fastStart":false}}`), &c); err != nil {
		t.Fatal(err)
	}
	if !c.Transcode.ABRLadderUnderCopy || c.Transcode.FastStart {
		t.Fatal("explicit saved preferences must be preserved")
	}
}
