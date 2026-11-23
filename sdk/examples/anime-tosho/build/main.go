package main

import (
	"fmt"
	"os"

	"github.com/evanw/esbuild/pkg/api"
)

func main() {
	res := api.Build(api.BuildOptions{
		EntryPoints: []string{"sdk/examples/anime-tosho/plugin.ts"},
		Bundle:      true,
		Format:      api.FormatIIFE,
		GlobalName:  "__plugin",
		Platform:    api.PlatformNeutral,
		Target:      api.ES2020,
		Outfile:     "sdk/examples/anime-tosho/dist/plugin.js",
		Write:       true,
		LogLevel:    api.LogLevelInfo,
	})
	if len(res.Errors) > 0 {
		fmt.Fprintln(os.Stderr, "bundle failed")
		os.Exit(1)
	}
}
