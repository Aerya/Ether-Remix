package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/debrid"
	"github.com/braedonsaunders/ether/internal/pse/music"
)

func dmmFailure(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, debrid.ErrAuth):
		return "account_refused"
	case errors.Is(err, debrid.ErrRateLimited):
		return "rate_limit"
	case errors.Is(err, debrid.ErrBlocked):
		return "blocked"
	case errors.Is(err, music.ErrNotCached):
		return "not_cached"
	default:
		return "upstream_error"
	}
}

// Les erreurs amont peuvent contenir des liens ou une clé : seuls leur type et
// l'étape sont journalisés, jamais leur texte ni les URL de lecture.
func (d *pseDebridAccount) traceDMM(stage string, err error, files int) {
	if !d.dmm || d.log == nil {
		return
	}
	attrs := []any{"component", "pse-music", "source", "dmm", "stage", stage, "account", d.index + 1, "outcome", dmmFailure(err), "files", files}
	if err != nil && !errors.Is(err, music.ErrNotCached) {
		d.log.Warn("music source stage failed", attrs...)
	} else {
		d.log.Info("music source stage completed", attrs...)
	}
}

func (a *App) testPSEMusicDMM(ctx context.Context) (string, error) {
	accounts := musicDebridAccounts(a.cfg.Get().Debrid)[config.DebridRealDebrid]
	if len(accounts) == 0 {
		if a.log != nil {
			a.log.Warn("music source test failed", "component", "pse-music", "source", "dmm", "stage", "account", "outcome", "not_configured")
		}
		return "", errors.New("DMM : aucun compte Real-Debrid actif avec clé dans Sources › Debrid.")
	}
	resolver := a.musicDMMResolvers(accounts)
	rs, ok := resolver.(music.Resolvers)
	if !ok {
		rs = music.Resolvers{resolver}
	}
	finder := rs[0].(*music.DebridResolver).Finder.(*music.DMMFinder)
	return checkPSEMusicDMM(ctx, finder, rs, a.log)
}

// Le test utilise des albums DMM, pas les nouveautés Deezer : il vérifie le
// catalogue, le compte, le cache et le débridage séparément.
func checkPSEMusicDMM(ctx context.Context, finder *music.DMMFinder, rs music.Resolvers, log *slog.Logger) (message string, resultErr error) {
	started := time.Now()
	albums, attempts, cached, files, misses, accountErrors, cacheErrors, linkErrors := 0, 0, 0, 0, 0, 0, 0, 0
	stage, outcome := "catalogue", "ok"
	defer func() {
		if log == nil {
			return
		}
		attrs := []any{"component", "pse-music", "source", "dmm", "ok", resultErr == nil, "stage", stage, "outcome", outcome,
			"albums", albums, "cache_attempts", attempts, "cached_albums", cached, "files", files, "not_cached", misses,
			"account_errors", accountErrors, "cache_errors", cacheErrors, "link_errors", linkErrors, "duration", time.Since(started).Round(time.Millisecond)}
		if resultErr != nil {
			log.Warn("music source test completed", attrs...)
		} else {
			log.Info("music source test completed", attrs...)
		}
	}()
	hits, err := finder.SampleAlbums(ctx)
	if err != nil {
		outcome = "catalogue_unavailable"
		return "", fmt.Errorf("DMM — étape catalogue : %w. Le compte Real-Debrid n’a pas encore été testé.", err)
	}
	albums = len(hits)
	if albums == 0 {
		outcome = "catalogue_empty"
		return "", errors.New("DMM — étape catalogue : aucun album exploitable renvoyé. Le compte Real-Debrid n’a pas encore été testé.")
	}
	var problems []string
	for i, r := range rs {
		if ctx.Err() != nil {
			break
		}
		dr := r.(*music.DebridResolver)
		account := dr.Account.(*pseDebridAccount)
		stage = "compte"
		provider, err := account.provider()
		if err == nil {
			var info debrid.Account
			info, err = provider.Account(ctx)
			if err == nil && !info.Premium {
				err = debrid.ErrAuth
			}
		}
		if err != nil {
			accountErrors++
			outcome = dmmFailure(err)
			problems = append(problems, fmt.Sprintf("compte #%d : %s", i+1, dmmFailureLabel(outcome)))
			account.traceDMM("account", err, 0)
			continue
		}
		account.traceDMM("account", nil, 0)
		for _, hit := range hits {
			if ctx.Err() != nil {
				break
			}
			attempts++
			stage = "cache"
			fs, err := account.OpenCached(ctx, hit)
			if err != nil {
				outcome = dmmFailure(err)
				if errors.Is(err, music.ErrNotCached) {
					misses++
				} else {
					cacheErrors++
					problems = append(problems, "cache : "+dmmFailureLabel(outcome))
				}
				continue
			}
			if len(fs) == 0 {
				misses++
				outcome = "not_cached"
				continue
			}
			cached++
			files += len(fs)
			stage = "lien audio"
			for _, file := range fs[:min(3, len(fs))] {
				st, err := account.Unlock(ctx, file)
				if err == nil && st.URL != "" {
					outcome = "ok"
					return fmt.Sprintf("DMM : catalogue joignable (%d albums proposés), compte Real-Debrid #%d actif, %d album(s) en cache, lien audio obtenu. Le diagnostic des sources peut vérifier les octets audio.", albums, i+1, cached), nil
				}
				linkErrors++
				outcome = dmmFailure(err)
				if err == nil {
					outcome = "empty_link"
				}
				problems = append(problems, "lien audio : "+dmmFailureLabel(outcome))
			}
		}
	}
	if ctx.Err() != nil {
		outcome = dmmFailure(ctx.Err())
		problems = append(problems, "délai du test dépassé")
	}
	detail := ""
	if len(problems) > 0 {
		detail = " " + strings.Join(problems[:min(3, len(problems))], ";") + "."
	}
	return "", fmt.Errorf("DMM : catalogue joignable (%d albums), %d vérification(s), %d album(s) absents du cache, %d erreur(s) de compte, %d erreur(s) de cache, %d erreur(s) de lien. Aucun flux obtenu.%s", albums, attempts, misses, accountErrors, cacheErrors, linkErrors, detail)
}

func dmmFailureLabel(reason string) string {
	switch reason {
	case "account_refused":
		return "compte refusé ou premium inactif"
	case "rate_limit":
		return "limite de requêtes atteinte"
	case "not_cached":
		return "album absent du cache"
	case "blocked":
		return "fichier refusé par Real-Debrid"
	case "timeout":
		return "délai dépassé"
	case "cancelled":
		return "test interrompu"
	case "empty_link":
		return "lien vide"
	default:
		return "service injoignable ou réponse invalide"
	}
}
