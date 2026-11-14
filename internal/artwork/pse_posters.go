package artwork

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/braedonsaunders/ether/internal/lru"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

// Ajouts Remix autour des affiches servies aux applications. Voir
// docs/pse/FORK.fr.md.

// PosterOrigin indique, pour une requête, si l'affiche renvoyée vient du
// fournisseur d'affiches du propriétaire (PostersPlus, XRDB…) ou du repli
// TMDB. Une affiche du fournisseur peut être gardée par l'application ; un
// repli doit être revérifié, pour ne pas rester affiché après une panne.
type PosterOrigin struct{ provider atomic.Bool }

type posterOriginKey struct{}

// WithPosterOrigin attache un témoin à ctx. GetPoster le renseigne.
func WithPosterOrigin(ctx context.Context) (context.Context, *PosterOrigin) {
	o := &PosterOrigin{}
	return context.WithValue(ctx, posterOriginKey{}, o), o
}

// FromProvider indique si la dernière affiche lue sous ce contexte venait du
// fournisseur. Faux sur un témoin nil.
func (o *PosterOrigin) FromProvider() bool { return o != nil && o.provider.Load() }

func markProviderPoster(ctx context.Context) {
	if o, _ := ctx.Value(posterOriginKey{}).(*PosterOrigin); o != nil {
		o.provider.Store(true)
	}
}

// PosterRefreshHours : durée, en heures, au bout de laquelle l'adresse des
// affiches du fournisseur change, ce qui force les applications à les
// reprendre (réglage PSE_POSTER_REFRESH_HOURS, 24 par défaut, de 1 à 168).
// Ether utilisait 6 heures : chaque application retéléchargeait alors toutes
// ses affiches quatre fois par jour.
func PosterRefreshHours() int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(pseenv.Get("PSE_POSTER_REFRESH_HOURS")), 10, 64)
	if err != nil || n < 1 {
		return 24
	}
	return min(n, 168)
}

func psePosterRefreshSeconds() int64 { return PosterRefreshHours() * 3600 }

// Les applications redemandent la même affiche à la même taille à chaque
// affichage d'une grille. Le redimensionnement (décodage, mise à l'échelle,
// encodage JPEG) est gardé en mémoire, indexé par le contenu de l'image
// source : une affiche modifiée par le fournisseur a un autre contenu, donc
// une autre entrée. Aucune durée de vie n'est nécessaire.
const pseResizeCacheBytes = 64 << 20

var (
	pseResizeMu    sync.Mutex
	pseResizeCache = lru.New(lru.Options[string, []byte]{MaxCost: pseResizeCacheBytes, Cost: func(_ string, v []byte) int64 {
		return int64(len(v))
	}})
)

// ResizePosterCached renvoie ResizePoster(data, w) en réutilisant un résultat
// déjà calculé pour la même image et la même largeur. Le résultat ne doit pas
// être modifié par l'appelant.
func ResizePosterCached(data []byte, w int) ([]byte, error) {
	sum := sha256.Sum256(data)
	key := hex.EncodeToString(sum[:]) + "/" + strconv.Itoa(clampWidth(w))
	pseResizeMu.Lock()
	out, ok := pseResizeCache.Get(key)
	pseResizeMu.Unlock()
	if ok {
		return out, nil
	}
	out, err := ResizePoster(data, w)
	if err != nil {
		return nil, err
	}
	pseResizeMu.Lock()
	pseResizeCache.Add(key, out)
	pseResizeMu.Unlock()
	return out, nil
}
