package notifyfr

import (
	"bytes"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// strict : PSE_STRICT_I18N=1 transforme les signalements de dérive (nouveau
// texte upstream non traduit) en échecs. Désactivé par défaut pour ne pas
// bloquer la synchronisation automatique pour une simple traduction.
func strict() bool { return os.Getenv("PSE_STRICT_I18N") == "1" }

func drift(t *testing.T, format string, args ...any) {
	t.Helper()
	if strict() {
		t.Errorf(format, args...)
	} else {
		t.Logf("DÉRIVE upstream (non bloquant) : "+format, args...)
	}
}

func TestLanguageSelection(t *testing.T) {
	for v, want := range map[string]bool{"fr": true, "FR-ca": true, " français ": true, "": true, "en": false, "de": false} {
		t.Setenv(EnvLanguage, v)
		if got := Enabled(); got != want {
			t.Errorf("%s=%q : Enabled()=%v, attendu %v", EnvLanguage, v, got, want)
		}
	}
	t.Setenv(EnvLanguage, "en")
	if title, _, _ := Localize("test", "Ether notification test", "", ""); title != "Ether notification test" {
		t.Errorf("en doit laisser le texte intact, obtenu %q", title)
	}
}

func TestUnsetLanguageKeepsEnglishInTests(t *testing.T) {
	old, had := os.LookupEnv(EnvLanguage)
	os.Unsetenv(EnvLanguage)
	t.Cleanup(func() {
		if had {
			os.Setenv(EnvLanguage, old)
		}
	})
	if Enabled() {
		t.Fatal("sans variable, les binaires de test doivent garder l'anglais d'Ether")
	}
}

func TestFrench(t *testing.T) {
	cases := []struct {
		kind, title, message, wantTitle, wantMessage string
	}{
		{"server.started", "Ether is online", "Salon is ready for connections.", "Ether est en ligne", "Salon est prêt à accepter les connexions."},
		{"server.stopping", "Ether is stopping", "The server is shutting down or restarting.", "Ether s'arrête", "Le serveur s'arrête ou redémarre."},
		{"update.available", "Ether 0.3.5 is available", "This server runs 0.3.4. Open Maintenance → Updates to install 0.3.5.",
			"Ether 0.3.5 est disponible", "Ce serveur exécute la version 0.3.4. Ouvrez Maintenance → Mises à jour pour installer la version 0.3.5."},
		{"provider.recovered", "Provider recovered", "Eweka is available again.", "Fournisseur rétabli", "Eweka est de nouveau disponible."},
		{"provider.outage", "Eweka", "Provider is unavailable or has rejected authentication. See server health for details.",
			"Eweka", "Le fournisseur est indisponible ou a refusé l'authentification. Consultez l'état du serveur pour plus de détails."},
		{"indexer.budget", "NZBGeek", "The indexer's daily budget has been exhausted.", "NZBGeek", "Le budget quotidien de l'indexeur est épuisé."},
		{"backup.failed", "Scheduled backup failed", "Ether could not take its scheduled backup: disk full",
			"Échec de la sauvegarde planifiée", "Ether n'a pas pu effectuer sa sauvegarde planifiée : disk full"},
		{"playback.start", "Ether playback start", "alice: Dune", "Lecture démarrée sur Ether", "alice: Dune"},
		{"title.available", "Title available", "Dune: Part Two", "Titre disponible", "Dune: Part Two"},
		// Un nom propre qui ressemble à un modèle d'un autre type d'événement
		// n'est pas traduit.
		{"title.available", "Title available", "Ether 2 is available", "Titre disponible", "Ether 2 is available"},
		// Texte inconnu (plugin, nouveau message upstream) : inchangé.
		{"plugin.custom", "Custom plugin alert", "Something happened.", "Custom plugin alert", "Something happened."},
	}
	for _, c := range cases {
		title, message, _ := French(c.kind, c.title, c.message, "")
		if title != c.wantTitle || message != c.wantMessage {
			t.Errorf("%s : obtenu (%q, %q), attendu (%q, %q)", c.kind, title, message, c.wantTitle, c.wantMessage)
		}
	}
}

func TestNewsletterUsesEthersTemplate(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "stats", "http.go"))
	if err != nil {
		t.Skip("gabarit d'Ether introuvable :", err)
	}
	m := regexp.MustCompile("(?s)newsletterTemplate = template.Must\\(template.New\\(\"newsletter\"\\).Parse\\(`(.*?)`\\)\\)").FindSubmatch(src)
	if m == nil {
		drift(t, "gabarit newsletterTemplate introuvable dans internal/stats/http.go")
		return
	}
	tpl := template.Must(template.New("n").Parse(string(m[1])))
	type title struct {
		Label string
		Plays int
	}
	for _, titles := range [][]title{{{"Dune", 3}, {"Heat", 1}}, nil} {
		var b bytes.Buffer
		data := map[string]any{
			"Since": time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC), "Until": time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC),
			"Plays": 4, "Completed": 1, "Hours": "6.5", "Titles": titles,
		}
		if err := tpl.Execute(&b, data); err != nil {
			drift(t, "le gabarit attend d'autres données : %v", err)
			return
		}
		_, _, html := French("newsletter.weekly", "", "", b.String())
		for _, want := range []string{`lang="fr"`, "Votre semaine sur Ether", "29 oct. 2026", "5 nov. 2026", "4 lectures · 1 terminée · 6,5 h"} {
			if !strings.Contains(html, want) {
				drift(t, "résumé hebdomadaire : %q absent de %s", want, html)
			}
		}
		if titles != nil && !strings.Contains(html, "Dune — 3 lectures") {
			drift(t, "résumé hebdomadaire : ligne de titre non traduite : %s", html)
		}
		if titles == nil && !strings.Contains(html, "Aucune lecture enregistrée cette semaine.") {
			drift(t, "résumé hebdomadaire : semaine vide non traduite : %s", html)
		}
		for _, english := range []string{"Your Ether week", "Popular on your server", " plays", "Playback time comes", "No recorded plays"} {
			if strings.Contains(html, english) {
				drift(t, "résumé hebdomadaire : %q reste en anglais", english)
			}
		}
	}
}

// TestUpstreamStringsAreTranslated relève les textes littéraux de
// notification dans le code d'Ether et signale ceux qui restent en anglais.
func TestUpstreamStringsAreTranslated(t *testing.T) {
	root := filepath.Join("..", "..")
	literal := `"((?:[^"\\]|\\.)*)"`
	// Champs Title/Message d'un littéral Event{…} ; un littéral suivi de « + »
	// est un préfixe concaténé, couvert par les modèles (patterns).
	event := regexp.MustCompile(`(?s)\bEvent\{(.{0,600}?)\}\)`)
	fields := regexp.MustCompile(`(?:Title|Message): +` + literal + `(\s*\+)?`)
	lifecycle := regexp.MustCompile(`notifyLifecycle\("[a-z.]+", ` + literal + `, ` + literal + `\)`)
	var missing []string
	seen := 0
	check := func(file, raw string) {
		s, err := strconv.Unquote(`"` + raw + `"`)
		if err != nil || s == "" {
			return
		}
		seen++
		if _, ok := titles[s]; ok {
			return
		}
		if _, ok := messages[s]; ok {
			return
		}
		missing = append(missing, file+": "+s)
	}
	for _, dir := range []string{"notify", "app"} {
		files, _ := filepath.Glob(filepath.Join(root, dir, "*.go"))
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") || strings.HasPrefix(filepath.Base(f), "pse_") {
				continue
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			base := filepath.Base(f)
			for _, e := range event.FindAllStringSubmatch(string(src), -1) {
				for _, m := range fields.FindAllStringSubmatch(e[1], -1) {
					if m[2] == "" {
						check(base, m[1])
					}
				}
			}
			for _, m := range lifecycle.FindAllStringSubmatch(string(src), -1) {
				check(base, m[1])
			}
		}
	}
	// Garde-fou : un scanner qui ne trouve plus rien ne prouve rien.
	if seen < 12 {
		drift(t, "seulement %d textes de notification relevés ; le scanner doit être adapté au code d'Ether", seen)
	}
	t.Logf("%d textes de notification relevés", seen)
	sort.Strings(missing)
	for _, m := range missing {
		drift(t, "texte de notification non traduit : %s", m)
	}
}
