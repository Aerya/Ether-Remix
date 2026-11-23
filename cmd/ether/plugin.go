package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/catalogue"
	"github.com/braedonsaunders/ether/internal/plugins/kit"
	"go.yaml.in/yaml/v3"
)

const pluginUsage = `usage:
  ether plugin new <type> [--lang ts|js|python] <dir>
  ether plugin new --kind <kind> [--lang ts|js|python] <dir>
      Scaffold a new plugin project.
      Types: catalogue, job, livetv_source, source, integration, notification_channel, extension

  ether plugin check [--json] <dir|pkg>
      Run the offline conformance kit and copy linter against a plugin directory
      or .etherpkg archive. Exits non-zero on any conformance error.

  ether plugin test <dir>
      Run recorded fixtures, conformance checks, and (when $ETHER_BIN is set)
      an integration check against a headless Ether instance. Set
      ETHER_PLUGIN_URL to test a running remote plugin.

  ether plugin pack [-o <out.etherpkg>] <dir>
      Compile TypeScript, verify conformance, and bundle a verified .etherpkg.

  ether plugin dev <dir> --server <url> [--once]
      Watch files in <dir>, hot-reload into a running Ether server via the
      Studio API, and stream plugin logs.

  ether plugin publish [--url <asset-url>] [-o <out.etherpkg>] [<dir|pkg>]
      Pack a verified .etherpkg, compute its SHA-256 digest, and print a
      validated plugins/<id>.yaml entry and PR steps for
      https://github.com/braedonsaunders/ether-plugins.
`

func runPlugin(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return runPluginWithContext(ctx, args, stdout, stderr)
}

func runPluginWithContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stderr, pluginUsage)
		return 2
	}
	sub := args[0]
	rest := args[1:]

	switch sub {
	case "new":
		return runPluginNew(rest, stdout, stderr)
	case "check":
		return runPluginCheck(ctx, rest, stdout, stderr)
	case "test":
		return runPluginTest(ctx, rest, stdout, stderr)
	case "pack":
		return runPluginPack(ctx, rest, stdout, stderr)
	case "dev":
		return runPluginDev(ctx, rest, stdout, stderr)
	case "publish":
		return runPluginPublish(ctx, rest, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "ether plugin: unknown subcommand %q\n\n%s", sub, pluginUsage)
		return 2
	}
}

func runPluginNew(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ether plugin new", flag.ContinueOnError)
	fs.SetOutput(stderr)
	kind := fs.String("kind", "", "plugin kind: livetv_source, source, integration, notification_channel, extension")
	lang := fs.String("lang", "ts", "language: ts, js, python")
	id := fs.String("id", "", "optional plugin ID")
	name := fs.String("name", "", "optional display name")

	pos := parseInterleaved(fs, args)
	recipe := ""
	if len(pos) == 2 && *kind == "" {
		*kind, pos = pos[0], pos[1:]
		switch *kind {
		case "catalogue", "catalog", "job":
			recipe = *kind
			*kind = "integration"
		}
	}
	if len(pos) != 1 || *kind == "" {
		fmt.Fprint(stderr, pluginUsage)
		return 2
	}
	dir := pos[0]
	if err := kit.Scaffold(kit.TemplateOptions{
		Dir:    dir,
		Recipe: recipe,
		Kind:   *kind,
		Lang:   *lang,
		ID:     *id,
		Name:   *name,
	}); err != nil {
		fmt.Fprintln(stderr, "ether plugin new:", err)
		return 1
	}
	fmt.Fprintf(stdout, "Created %s (%s) plugin in %s\n", *kind, *lang, dir)
	fmt.Fprintf(stdout, "Next: ether plugin check %q\nThen: ether plugin dev %q --server http://localhost:32400\nReference: /ether/plugins/reference/getting-started\n", dir, dir)
	return 0
}

func runPluginCheck(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ether plugin check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "output structured JSON report")

	pos := parseInterleaved(fs, args)
	if len(pos) != 1 {
		fmt.Fprint(stderr, pluginUsage)
		return 2
	}
	pkg, err := kit.Load(pos[0])
	if err != nil && pkg == nil {
		fmt.Fprintln(stderr, "ether plugin check:", err)
		return 1
	}
	rep := kit.Check(ctx, pkg)
	if *asJSON {
		b, err := rep.JSON()
		if err != nil {
			fmt.Fprintln(stderr, "ether plugin check:", err)
			return 1
		}
		fmt.Fprintln(stdout, string(b))
	} else {
		fmt.Fprint(stdout, pluginReport(rep))
	}
	if !rep.OK {
		return 1
	}
	return 0
}

func runPluginTest(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ether plugin test", flag.ContinueOnError)
	fs.SetOutput(stderr)
	pos := parseInterleaved(fs, args)
	if len(pos) != 1 {
		fmt.Fprint(stderr, pluginUsage)
		return 2
	}
	pkg, err := kit.Load(pos[0])
	if err != nil && pkg == nil {
		fmt.Fprintln(stderr, "ether plugin test:", err)
		return 1
	}
	rep := kit.Check(ctx, pkg)
	fmt.Fprint(stdout, pluginReport(rep))
	if !rep.OK {
		return 1
	}
	tr, err := kit.RunTests(ctx, pkg, os.Getenv("ETHER_BIN"))
	if err != nil {
		fmt.Fprintln(stderr, "ether plugin test:", err)
		return 1
	}
	fmt.Fprint(stdout, tr.Text())
	if !tr.OK {
		return 1
	}
	return 0
}

func runPluginPack(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ether plugin pack", flag.ContinueOnError)
	fs.SetOutput(stderr)
	outPath := fs.String("o", "", "output .etherpkg path")

	pos := parseInterleaved(fs, args)
	if len(pos) != 1 {
		fmt.Fprint(stderr, pluginUsage)
		return 2
	}
	dir := pos[0]
	pkg, err := kit.Load(dir)
	if err != nil {
		fmt.Fprintln(stderr, "ether plugin pack:", err)
		return 1
	}
	rep := kit.Check(ctx, pkg)
	if !rep.OK {
		fmt.Fprint(stderr, pluginReport(rep))
		return 1
	}
	pkgBytes, err := kit.Pack(pkg)
	if err != nil {
		fmt.Fprintln(stderr, "ether plugin pack:", err)
		return 1
	}
	dest := *outPath
	if dest == "" {
		base := filepath.Base(filepath.Clean(dir))
		if base == "." || base == "/" || base == "" {
			base = pkg.Manifest.ID
		}
		if !strings.HasSuffix(base, ".etherpkg") {
			base += ".etherpkg"
		}
		dest = base
	}
	if err := os.WriteFile(dest, pkgBytes, 0o644); err != nil {
		fmt.Fprintln(stderr, "ether plugin pack:", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s\n", dest)
	return 0
}

func runPluginDev(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ether plugin dev", flag.ContinueOnError)
	fs.SetOutput(stderr)
	serverURL := fs.String("server", "http://localhost:32400", "Ether server URL")
	token := os.Getenv("ETHER_TOKEN")
	once := fs.Bool("once", false, "reload once, stream current logs, and exit")
	interval := fs.Duration("interval", 750*time.Millisecond, "watch poll interval")

	pos := parseInterleaved(fs, args)
	if len(pos) != 1 {
		fmt.Fprint(stderr, pluginUsage)
		return 2
	}
	dir := pos[0]
	base := strings.TrimRight(strings.TrimSpace(*serverURL), "/")
	if *interval <= 0 {
		fmt.Fprintln(stderr, "ether plugin dev: --interval must be positive (for example 750ms)")
		return 2
	}
	if base == "" {
		fmt.Fprintln(stderr, "ether plugin dev: --server is required")
		return 2
	}

	client := &http.Client{Timeout: 3 * time.Minute}
	var seenLogs []string

	reload := func() (string, error) {
		pkg, err := kit.LoadDir(dir)
		if err != nil {
			return "", err
		}
		rep := kit.Check(ctx, pkg)
		if !rep.OK {
			return "", fmt.Errorf("conformance failed before install:\n%s", pluginReport(rep))
		}
		pkgBytes, err := kit.Pack(pkg)
		if err != nil {
			return "", err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/ether/api/plugins/install", bytes.NewReader(pkgBytes))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("X-Ether-Request", "1")
		if token != "" {
			req.Header.Set("X-Ether-Admin-Token", token)
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("install returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return pkg.Manifest.ID, nil
	}

	pollLogs := func(pluginID string) {
		if pluginID == "" {
			return
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/ether/api/plugins/"+pluginID+"/logs", nil)
		if err != nil {
			return
		}
		req.Header.Set("X-Ether-Request", "1")
		if token != "" {
			req.Header.Set("X-Ether-Admin-Token", token)
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return
		}
		var payload struct {
			Lines []string `json:"lines"`
		}
		if json.NewDecoder(resp.Body).Decode(&payload) != nil {
			return
		}
		// Preserve repeated lines while avoiding replaying the overlapping log window.
		overlap := 0
		for n := 1; n <= len(seenLogs) && n <= len(payload.Lines); n++ {
			match := true
			for i := 0; i < n; i++ {
				if seenLogs[len(seenLogs)-n+i] != payload.Lines[i] {
					match = false
					break
				}
			}
			if match {
				overlap = n
			}
		}
		for _, line := range payload.Lines[overlap:] {
			fmt.Fprintf(stdout, "[log] %s\n", line)
		}
		seenLogs = append(seenLogs[:0], payload.Lines...)

	}

	lastSnap := dirSnapshot(dir)
	pluginID, err := reload()
	if err != nil {
		fmt.Fprintln(stderr, "ether plugin dev:", err)
		fmt.Fprintln(stderr, "Fix the reported field or route and save again. For 401/403, set ETHER_TOKEN to this development server's admin token; for connection errors, check --server.")
		if *once {
			return 1
		}
	} else {
		fmt.Fprintf(stdout, "Reloaded %s on %s\n", pluginID, base)
	}
	pollLogs(pluginID)
	if *once {
		return 0
	}

	fmt.Fprintf(stdout, "Watching %s. Save to check and reload; Ctrl-C stops.\n", dir)
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return 0
		case <-ticker.C:
			snap := dirSnapshot(dir)
			if snap != lastSnap {
				lastSnap = snap
				if id, err := reload(); err != nil {
					fmt.Fprintf(stderr, "reload error: %v\n", err)
				} else {
					pluginID = id
					fmt.Fprintf(stdout, "Reloaded %s on %s\n", pluginID, base)
				}
			}
			pollLogs(pluginID)
		}
	}
}

func dirSnapshot(dir string) string {
	var b strings.Builder
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if info, err := d.Info(); err == nil {
			fmt.Fprintf(&b, "%s:%d:%d;", p, info.Size(), info.ModTime().UnixNano())
		}
		return nil
	})
	return b.String()
}

const marketplaceRepoURL = "https://github.com/braedonsaunders/ether-plugins"

type publishEntryYAML struct {
	ID           string              `yaml:"id"`
	Name         string              `yaml:"name"`
	Summary      string              `yaml:"summary"`
	Description  string              `yaml:"description,omitempty"`
	Kind         string              `yaml:"kind"`
	Icon         string              `yaml:"icon,omitempty"`
	Repo         string              `yaml:"repo,omitempty"`
	Homepage     string              `yaml:"homepage,omitempty"`
	Author       string              `yaml:"author,omitempty"`
	License      string              `yaml:"license,omitempty"`
	Capabilities []string            `yaml:"capabilities,omitempty"`
	Permissions  plugins.Permissions `yaml:"permissions,omitempty"`
	Versions     []catalogue.Version `yaml:"versions"`
}

func runPluginPublish(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ether plugin publish", flag.ContinueOnError)
	fs.SetOutput(stderr)
	assetURL := fs.String("url", "", "public release asset URL for the .etherpkg")
	outPath := fs.String("o", "", "output .etherpkg path")
	pos := parseInterleaved(fs, args)
	target := "."
	if len(pos) == 1 {
		target = pos[0]
	} else if len(pos) > 1 {
		fmt.Fprint(stderr, pluginUsage)
		return 2
	}

	cleanURL := strings.TrimSpace(*assetURL)
	if cleanURL != "" {
		u, err := url.Parse(cleanURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			fmt.Fprintln(stderr, "ether plugin publish: --url must be an http(s) release asset URL")
			return 2
		}
	}

	pkg, err := kit.Load(target)
	if err != nil {
		fmt.Fprintln(stderr, "ether plugin publish:", err)
		return 1
	}
	rep := kit.Check(ctx, pkg)
	if !rep.OK {
		fmt.Fprint(stderr, pluginReport(rep))
		return 1
	}
	if pkg.Manifest.Ether == nil {
		fmt.Fprintln(stderr, "ether plugin publish: marketplace entries require an ether manifest block")
		return 1
	}

	var pkgBytes []byte
	if info, statErr := os.Stat(target); statErr == nil && !info.IsDir() {
		pkgBytes, err = os.ReadFile(target)
		if err != nil {
			fmt.Fprintln(stderr, "ether plugin publish:", err)
			return 1
		}
	} else {
		pkgBytes, err = kit.Pack(pkg)
		if err != nil {
			fmt.Fprintln(stderr, "ether plugin publish:", err)
			return 1
		}
	}

	man := pkg.Manifest
	pkgName := fmt.Sprintf("%s-%s.etherpkg", man.ID, man.Version)
	dest := strings.TrimSpace(*outPath)
	if dest == "" {
		cleanTarget := filepath.Clean(target)
		if cleanTarget == "." {
			dest = pkgName
		} else {
			dest = filepath.Join(filepath.Dir(cleanTarget), pkgName)
		}
	}
	if filepath.Clean(dest) != filepath.Clean(target) {
		if err := os.WriteFile(dest, pkgBytes, 0o644); err != nil {
			fmt.Fprintln(stderr, "ether plugin publish:", err)
			return 1
		}
	}

	sum := sha256.Sum256(pkgBytes)
	digest := hex.EncodeToString(sum[:])

	if cleanURL == "" {
		fmt.Fprintf(stdout, "Packed %s %s (%s) -> %s\n", man.ID, man.Version, man.Ether.Kind, dest)
		fmt.Fprintf(stdout, "SHA-256: %s\n\n", digest)
		fmt.Fprintf(stdout, "To submit %s to the Ether plugin marketplace (%s):\n", man.Name, marketplaceRepoURL)
		fmt.Fprintf(stdout, "  1. Upload %s to a GitHub release in your plugin repository.\n", dest)
		fmt.Fprintf(stdout, "  2. Re-run with --url set to that release asset URL to print plugins/%s.yaml:\n", man.ID)
		fmt.Fprintf(stdout, "       ether plugin publish --url https://github.com/<owner>/<repo>/releases/download/v%s/%s %s\n", man.Version, pkgName, target)
		fmt.Fprintf(stdout, "  3. Fork %s, write plugins/%s.yaml, and open a pull request.\n", marketplaceRepoURL, man.ID)
		return 0
	}

	e := man.Ether
	caps := make([]string, len(e.Capabilities))
	for i, c := range e.Capabilities {
		caps[i] = string(c)
	}
	icon := ""
	if strings.HasPrefix(e.Icon, "http://") || strings.HasPrefix(e.Icon, "https://") {
		icon = e.Icon
	} else if strings.HasPrefix(man.Logo, "http://") || strings.HasPrefix(man.Logo, "https://") {
		icon = man.Logo
	}
	repo := strings.TrimSpace(e.Repo)
	homepage := strings.TrimSpace(e.Author.URL)
	if homepage == repo {
		homepage = ""
	}

	entryDoc := publishEntryYAML{
		ID:           man.ID,
		Name:         man.Name,
		Summary:      resolvePublishSummary(pkg),
		Description:  strings.TrimSpace(man.Description),
		Kind:         string(e.Kind),
		Icon:         icon,
		Repo:         repo,
		Homepage:     homepage,
		Author:       strings.TrimSpace(e.Author.Name),
		License:      strings.TrimSpace(e.License),
		Capabilities: caps,
		Permissions:  e.Permissions,
		Versions: []catalogue.Version{{
			Version:    man.Version,
			URL:        cleanURL,
			SHA256:     digest,
			ReleasedAt: time.Now().UTC().Format(time.RFC3339),
			MinEther:   "0.3.14",
		}},
	}

	yamlBytes, err := yaml.Marshal(entryDoc)
	if err != nil {
		fmt.Fprintln(stderr, "ether plugin publish:", err)
		return 1
	}
	if _, err := catalogue.ParseEntryYAML(yamlBytes); err != nil {
		fmt.Fprintln(stderr, "ether plugin publish: generated entry failed validation:", err)
		return 1
	}

	fmt.Fprintf(stdout, "Packed %s %s (%s) -> %s\n", man.ID, man.Version, e.Kind, dest)
	fmt.Fprintf(stdout, "SHA-256: %s\n\n", digest)
	fmt.Fprintf(stdout, "# plugins/%s.yaml\n%s\n", man.ID, strings.TrimRight(string(yamlBytes), "\n"))
	fmt.Fprintf(stdout, "\nPull request steps:\n")
	fmt.Fprintf(stdout, "  1. Upload %s at %s.\n", dest, cleanURL)
	fmt.Fprintf(stdout, "  2. Fork %s and save the YAML above to plugins/%s.yaml (or add the versions entry when updating an existing plugin).\n", marketplaceRepoURL, man.ID)
	fmt.Fprintf(stdout, "  3. Open a pull request against %s. Marketplace CI verifies the SHA-256 digest and runs `ether plugin check`.\n", marketplaceRepoURL)
	return 0
}

func resolvePublishSummary(pkg *kit.Package) string {
	if pkg.Manifest.Ether == nil {
		return strings.TrimSpace(pkg.Manifest.Description)
	}
	summary := strings.TrimSpace(pkg.Manifest.Ether.Summary)
	if key, ok := strings.CutPrefix(summary, "$t:"); ok {
		if enRaw, hasEN := pkg.Manifest.Ether.Strings["en"]; hasEN {
			var doc map[string]string
			var relPath string
			if json.Unmarshal(enRaw, &relPath) == nil && relPath != "" {
				if b, ok := pkg.Files[relPath]; ok {
					_ = json.Unmarshal(b, &doc)
				}
			} else {
				_ = json.Unmarshal(enRaw, &doc)
			}
			if v := strings.TrimSpace(doc[key]); v != "" {
				return v
			}
		}
		return strings.TrimSpace(pkg.Manifest.Description)
	}
	return summary
}

// pluginReport leaves JSON reports stable while giving CLI users direct repair links.
func pluginReport(rep kit.Report) string {
	var b strings.Builder
	b.WriteString(rep.Text())
	seen := map[string]bool{}
	for _, issue := range rep.Errors {
		topic, fix := "testing", "Fix the reported route response to match its capability contract, then run ether plugin check again."
		switch issue.Rule {
		case "manifest":
			topic, fix = "manifest", "Edit the named manifest field to the value required above; keep kind, capabilities and contributions aligned."
		case "strings", "copy":
			topic, fix = "settings", "Add the missing translation key or correct its copy in every declared locale."
		case "settings":
			topic, fix = "settings", "Correct the named settings field and include every referenced schema file in the package."
		}
		if !seen[topic] {
			fmt.Fprintf(&b, "  Fix: %s\n  Reference: /ether/plugins/reference/%s\n", fix, topic)
			seen[topic] = true
		}
	}
	return b.String()
}
