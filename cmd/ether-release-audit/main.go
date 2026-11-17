// ether-release-audit audits a stopped server or a coherent database backup.
// Configuration is read from a separate private copy: config.Open may upgrade it.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
	_ "modernc.org/sqlite"
	"os"
)

func main() {
	path := flag.String("db", "", "database path")
	cfgDir := flag.String("config", "", "private copied config directory")
	apply := flag.Bool("apply", false, "quarantine unconfirmed entries (server must be stopped)")
	flag.Parse()
	if *path == "" || *cfgDir == "" {
		fmt.Fprintln(os.Stderr, "db and copied config directory required")
		os.Exit(2)
	}
	cfg, err := config.Open(*cfgDir)
	if err != nil {
		panic("cannot open copied configuration")
	}
	mode := "ro"
	if *apply {
		mode = "rw"
	}
	db, err := sql.Open("sqlite", "file:"+*path+"?mode="+mode)
	if err != nil {
		panic(err)
	}
	defer db.Close()
	report, err := releasedb.AuditAssociations(context.Background(), db, func(s string) (string, error) { b, e := cfg.Unseal("pse-release-library", s); return string(b), e }, *apply)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	json.NewEncoder(os.Stdout).Encode(report)
}
