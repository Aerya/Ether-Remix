// Package language shares Ether's supported audio language choices with Remix.
package language

import (
	_ "embed"
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

type Choice struct {
	Code string `json:"code"`
	FR   string `json:"fr"`
	EN   string `json:"en"`
}

//go:embed languages.json
var data []byte

func Choices() []Choice {
	var out []Choice
	if err := json.Unmarshal(data, &out); err != nil {
		panic(err)
	}
	return out
}
func Name(code string) string { return releaseparse.LanguageName(code) }
