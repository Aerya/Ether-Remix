//go:build ignore

// Run from the repository root: go run internal/plugins/testdata/archives/generate.go
package main

import (
	"fmt"
	"github.com/braedonsaunders/ether/internal/archive/archivetest"
	"os"
)

func main() {
	for _, v := range []int{4, 5} {
		b, e := archivetest.BuildRAR([]archivetest.RARFile{{Name: "a.txt", Data: []byte("first member: abcdefghijklmnopqrstuvwxyz")}, {Name: "b.txt", Data: []byte("second member: 0123456789")}}, archivetest.RAROptions{Version: v})
		if e != nil {
			panic(e)
		}
		if e = os.WriteFile(fmt.Sprintf("internal/plugins/testdata/archives/store%d.rar", v), b[0], 0644); e != nil {
			panic(e)
		}
	}
}
