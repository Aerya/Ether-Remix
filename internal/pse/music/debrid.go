package music

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Lecture depuis un compte debrid (AllDebrid), sans rien stocker : quand un
// titre est lancé, l'album est cherché sur les indexeurs torrent d'Ether
// (catégorie audio), ouvert sur le compte debrid uniquement s'il y est déjà
// en cache, puis le fichier du titre est diffusé en flux. Un torrent absent
// du cache est retiré aussitôt du compte.

// TorrentHit est un résultat d'indexeur.
type TorrentHit struct {
	Title    string
	InfoHash string // hex minuscule ; vide si l'indexeur ne le donne pas
	Magnet   string
	Download string // lien .torrent, quand l'indexeur ne donne ni empreinte ni magnet
	Seeders  int
	Size     int64
}

// TorrentFinder cherche des albums sur les indexeurs torrent.
type TorrentFinder interface {
	FindAlbum(ctx context.Context, query string) ([]TorrentHit, error)
	// Magnet complète un résultat qui n'a qu'un lien de téléchargement.
	Magnet(ctx context.Context, h TorrentHit) (TorrentHit, error)
}

// DebridFile est un fichier d'un torrent ouvert sur le compte debrid.
type DebridFile struct {
	Path              string
	Size              int64
	TorrentID, FileID string
	AccountID         string // opaque in-memory owner, never a credential
	Link              string // lien côté service, à débrider
}

// DebridAccount ouvre un torrent déjà en cache et débride ses fichiers.
type DebridAccount interface {
	// OpenCached renvoie les fichiers du torrent, ou ErrNotCached (le
	// torrent ajouté pour la vérification est alors retiré).
	OpenCached(ctx context.Context, hit TorrentHit) ([]DebridFile, error)
	Unlock(ctx context.Context, file DebridFile) (Stream, error)
}

// ErrNotCached signale un album absent du cache debrid.
var ErrNotCached = errors.New("music: album absent du cache debrid")

// ErrDebridUnavailable signale une panne passagère (indexeurs ou compte
// debrid injoignables, clé refusée, limite de requêtes…) : l'album n'est
// pas déclaré absent, un nouvel essai a lieu après une courte attente.
var ErrDebridUnavailable = errors.New("music: recherche debrid momentanément impossible")

// DebridResolver résout un titre via le compte debrid.
type DebridResolver struct {
	Provider string
	Finder   TorrentFinder
	Account  DebridAccount
	// Tries borne le nombre de torrents vérifiés par album (6 par défaut).
	Tries int
	Now   func() time.Time

	mu     sync.Mutex
	albums map[string]*debridAlbum
	links  map[int64]cachedStream
	locks  map[string]chan struct{}
}

// debridAlbum est l'état partagé de la recherche d'un album : les torrents
// déjà ouverts (dans l'ordre de classement) et ceux qui restent à vérifier
// si un titre manque (album partiel, autre édition, disque séparé).
type debridAlbum struct {
	torrents [][]DebridFile // torrents en cache, fichiers audio seulement
	rest     []TorrentHit   // candidats pas encore vérifiés
	tried    int            // candidats vérifiés
	until    time.Time      // validité de l'état
	err      error          // ErrNotCached (absence confirmée) ou panne
	fails    int            // pannes consécutives, pour l'attente
	retry    time.Time      // pas de nouvel essai avant (panne)
}

const (
	debridAlbumTTL = 6 * time.Hour
	debridMissTTL  = 30 * time.Minute
	debridLinkTTL  = 50 * time.Minute
	// debridLookupTimeout borne une recherche d'album détachée du client.
	debridLookupTimeout = 2 * time.Minute
	// Attente après une panne : 15 s, doublée à chaque échec, 5 min au plus.
	debridRetryMin = 15 * time.Second
	debridRetryMax = 5 * time.Minute
)

func debridBackoff(fails int) time.Duration {
	d := debridRetryMin
	for i := 1; i < fails && d < debridRetryMax; i++ {
		d *= 2
	}
	return min(d, debridRetryMax)
}

var audioExts = map[string]string{
	".flac": "audio/flac", ".mp3": "audio/mpeg", ".m4a": "audio/mp4", ".aac": "audio/aac",
	".ogg": "audio/ogg", ".opus": "audio/ogg", ".wav": "audio/wav", ".alac": "audio/mp4",
}

func (r *DebridResolver) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *DebridResolver) tries() int {
	if r.Tries > 0 {
		return r.Tries
	}
	return 6
}

// Resolve trouve le flux d'un titre.
func (r *DebridResolver) Resolve(ctx context.Context, t Track) (Stream, error) {
	if r.Finder == nil || r.Account == nil {
		return Stream{}, ErrNotFound
	}
	r.mu.Lock()
	if r.links == nil {
		r.links, r.albums, r.locks = map[int64]cachedStream{}, map[string]*debridAlbum{}, map[string]chan struct{}{}
	}
	if c, ok := r.links[t.ID]; ok {
		if skipped(ctx, c.s.URL) {
			// Let another account serve this request; refresh on the next playback.
			delete(r.links, t.ID)
			r.mu.Unlock()
			return Stream{}, ErrNotFound
		}
		if r.now().Before(c.until) {
			r.mu.Unlock()
			return c.s, nil
		}
	}
	r.mu.Unlock()

	f, err := r.findFile(ctx, t)
	if err != nil {
		return Stream{}, err
	}
	s, err := r.Account.Unlock(ctx, f)
	if err != nil {
		return Stream{}, err
	}
	if s.URL == "" || skipped(ctx, s.URL) {
		return Stream{}, ErrNotFound
	}
	s.Provider, s.Quality, s.Mime = r.Provider, debridQuality(f.Path), audioExts[strings.ToLower(path.Ext(f.Path))]
	until := r.now().Add(debridLinkTTL)
	if s.ExpiresAt > 0 && time.Unix(s.ExpiresAt, 0).Add(-5*time.Second).Before(until) {
		until = time.Unix(s.ExpiresAt, 0).Add(-5 * time.Second)
	}
	if !r.now().Before(until) {
		return Stream{}, ErrNotFound
	}
	r.mu.Lock()
	if len(r.links) > 4096 {
		r.links = map[int64]cachedStream{}
	}
	r.links[t.ID] = cachedStream{s: s, until: until}
	r.mu.Unlock()
	return s, nil
}

// findFile cherche le fichier du titre dans les torrents déjà ouverts de
// l'album, puis, s'il manque, dans les candidats suivants.
func (r *DebridResolver) findFile(ctx context.Context, t Track) (DebridFile, error) {
	key := albumKey(t)
	seen := -1
	for {
		a, err := r.album(ctx, t, key, seen)
		if err != nil {
			return DebridFile{}, err
		}
		for _, files := range a.torrents[max(seen, 0):] {
			if f, ok := pickDebridFile(files, t); ok {
				return f, nil
			}
		}
		if len(a.rest) == 0 || a.tried >= r.tries() {
			if a.err != nil && !errors.Is(a.err, ErrNotCached) && r.now().Before(a.retry) {
				return DebridFile{}, a.err // panne en cours : pas une absence
			}
			return DebridFile{}, ErrNotFound
		}
		if a.err != nil && r.now().Before(a.retry) {
			return DebridFile{}, a.err
		}
		seen = len(a.torrents)
	}
}

func albumKey(t Track) string {
	if t.Album.ID != 0 {
		return strconv.FormatInt(t.Album.ID, 10)
	}
	return fold(t.Artist.Name) + "|" + fold(t.Album.Title)
}

// snapshot copie l'état pour le lire hors verrou.
func (a *debridAlbum) snapshot() debridAlbum {
	c := *a
	c.torrents = append([][]DebridFile(nil), a.torrents...)
	c.rest = append([]TorrentHit(nil), a.rest...)
	return c
}

// album renvoie l'état de la recherche de l'album. seen < 0 : premier
// appel ; sinon le titre n'est pas dans les seen premiers torrents et il
// faut vérifier le candidat suivant. Une seule recherche par album à la
// fois : les autres titres attendent son résultat, et un client qui
// abandonne cesse d'attendre sans interrompre la recherche, dont le
// résultat est gardé pour la tentative suivante.
func (r *DebridResolver) album(ctx context.Context, t Track, key string, seen int) (debridAlbum, error) {
	r.mu.Lock()
	lock := r.locks[key]
	if lock == nil {
		if len(r.locks) > 1024 {
			r.locks = map[string]chan struct{}{}
		}
		lock = make(chan struct{}, 1)
		r.locks[key] = lock
	}
	r.mu.Unlock()
	select {
	case lock <- struct{}{}:
	case <-ctx.Done():
		return debridAlbum{}, ctx.Err()
	}

	r.mu.Lock()
	prev := r.albums[key]
	if prev != nil && r.now().Before(prev.until) && (seen < 0 || len(prev.torrents) > seen ||
		len(prev.rest) == 0 || prev.tried >= r.tries() || r.now().Before(prev.retry)) {
		// Déjà connu, ou déjà complété par une autre requête.
		snap := prev.snapshot()
		r.mu.Unlock()
		<-lock
		if seen < 0 && len(snap.torrents) == 0 {
			if snap.err == nil {
				snap.err = ErrNotCached
			}
			return debridAlbum{}, snap.err
		}
		return snap, nil
	}
	var base *debridAlbum
	if prev != nil {
		c := prev.snapshot()
		base = &c
	}
	r.mu.Unlock()

	// La recherche (indexeurs puis compte debrid) prend souvent plus de
	// temps qu'un client n'en accorde : elle continue sans la requête.
	done := make(chan debridAlbum, 1)
	go func() {
		defer func() { <-lock }()
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), debridLookupTimeout)
		defer cancel()
		var a debridAlbum
		if base != nil && r.now().Before(base.until) && len(base.torrents) > 0 {
			a = r.extend(lctx, *base)
		} else {
			fails := 0
			if base != nil && base.err != nil && !errors.Is(base.err, ErrNotCached) {
				fails = base.fails
			}
			a = r.lookup(lctx, t, fails)
		}
		r.mu.Lock()
		if len(r.albums) > 2048 {
			r.albums = map[string]*debridAlbum{}
		}
		r.albums[key] = &a
		r.mu.Unlock()
		done <- a.snapshot()
	}()
	select {
	case a := <-done:
		if seen < 0 && len(a.torrents) == 0 {
			return debridAlbum{}, a.err
		}
		return a, nil
	case <-ctx.Done():
		return debridAlbum{}, ctx.Err()
	}
}

// lookup cherche l'album sur les indexeurs et ouvre le premier candidat en
// cache. fails compte les pannes précédentes.
func (r *DebridResolver) lookup(ctx context.Context, t Track, fails int) debridAlbum {
	now := r.now()
	artist, album := strings.TrimSpace(t.Artist.Name), cleanAlbumTitle(t.Album.Title)
	if album == "" {
		return debridAlbum{err: ErrNotCached, until: now.Add(debridMissTTL)}
	}
	hits, err := r.Finder.FindAlbum(ctx, artist+" "+album)
	if err != nil {
		return r.failed(debridAlbum{}, err, fails+1)
	}
	a := debridAlbum{rest: rankAlbumHits(hits, artist, album), until: now.Add(debridAlbumTTL)}
	a = r.extend(ctx, a)
	if len(a.torrents) == 0 && (a.err == nil || errors.Is(a.err, ErrNotCached)) {
		// Absence confirmée : aucun candidat en cache.
		return debridAlbum{err: ErrNotCached, until: now.Add(debridMissTTL)}
	}
	if len(a.torrents) == 0 {
		a.fails = fails + 1
		return r.failed(a, a.err, a.fails)
	}
	return a
}

// failed garde un état de panne avec une attente bornée.
func (r *DebridResolver) failed(a debridAlbum, err error, fails int) debridAlbum {
	wait := debridBackoff(fails)
	a.fails, a.retry = fails, r.now().Add(wait)
	if len(a.torrents) == 0 {
		a.until = a.retry
	}
	if !errors.Is(err, ErrDebridUnavailable) {
		err = fmt.Errorf("%w : %v", ErrDebridUnavailable, err)
	}
	a.err = err
	return a
}

// extend vérifie les candidats restants jusqu'au prochain torrent en
// cache contenant de l'audio. Un candidat en panne reste en tête de liste
// pour le prochain essai.
func (r *DebridResolver) extend(ctx context.Context, a debridAlbum) debridAlbum {
	a.err = nil
	var temp, soft error
	for len(a.rest) > 0 && a.tried < r.tries() {
		h := a.rest[0]
		if h.Magnet == "" {
			// Lien .torrent illisible : propre à ce candidat, on passe au
			// suivant ; l'album ne sera pas déclaré absent pour autant.
			full, err := r.Finder.Magnet(ctx, h)
			if err != nil && ctx.Err() != nil {
				temp = err
				break
			}
			if err != nil || full.Magnet == "" {
				if err != nil {
					soft = err
				}
				a.rest, a.tried = a.rest[1:], a.tried+1
				continue
			}
			h = full
		}
		files, err := r.Account.OpenCached(ctx, h)
		if err != nil && !errors.Is(err, ErrNotCached) {
			temp = err // panne, délai dépassé : candidat gardé
			break
		}
		a.rest, a.tried = a.rest[1:], a.tried+1
		var audio []DebridFile
		for _, f := range files {
			if _, ok := audioExts[strings.ToLower(path.Ext(f.Path))]; ok && (f.Link != "" || (f.TorrentID != "" && f.FileID != "")) {
				audio = append(audio, f)
			}
		}
		if len(audio) > 0 {
			a.torrents = append(a.torrents, audio)
			a.fails, a.retry = 0, time.Time{}
			return a
		}
	}
	if temp == nil {
		temp = soft
	}
	if temp != nil {
		return r.failed(a, temp, a.fails+1)
	}
	a.err = ErrNotCached
	return a
}

// cleanAlbumTitle retire les mentions d'édition que les noms de torrents
// reprennent rarement : « (Deluxe Edition) », « [Remastered] »…
func cleanAlbumTitle(s string) string {
	s = strings.TrimSpace(s)
	for _, open := range []string{" (", " ["} {
		if i := strings.Index(s, open); i > 0 {
			rest := strings.ToLower(s[i:])
			for _, w := range []string{"deluxe", "edition", "édition", "remaster", "expanded", "bonus", "version", "anniversary"} {
				if strings.Contains(rest, w) {
					s = strings.TrimSpace(s[:i])
					break
				}
			}
		}
	}
	return s
}

func tokens(s string) []string {
	var out []string
	for _, w := range strings.Fields(foldWords(s)) {
		if len([]rune(w)) >= 2 || isDigits(w) {
			out = append(out, w)
		}
	}
	return out
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func containsAll(hay string, words []string) bool {
	padded := " " + hay + " "
	for _, w := range words {
		if !strings.Contains(padded, " "+w+" ") {
			return false
		}
	}
	return true
}

// rankAlbumHits garde les torrents qui nomment l'album (et l'artiste), le
// lossless et les plus partagés d'abord, les discographies en dernier.
func rankAlbumHits(hits []TorrentHit, artist, album string) []TorrentHit {
	at, al := tokens(artist), tokens(album)
	type scored struct {
		h     TorrentHit
		score int
	}
	var out []scored
	seen := map[string]bool{}
	for _, h := range hits {
		key := h.InfoHash
		if key == "" {
			key = h.Magnet
		}
		if key == "" {
			key = h.Download
		}
		if key == "" {
			continue
		}
		if seen[key] {
			continue
		}
		title := foldWords(h.Title)
		if !containsAll(title, al) {
			continue
		}
		score := 0
		if containsAll(title, at) {
			score += 100
		} else if len(at) > 0 {
			continue
		}
		low := strings.ToLower(h.Title)
		if strings.Contains(low, "flac") || strings.Contains(low, "lossless") {
			score += 40
		}
		if strings.Contains(low, "discograph") || strings.Contains(low, "collection") || strings.Contains(low, "integrale") || strings.Contains(low, "intégrale") {
			score -= 60
		}
		score += min(h.Seeders, 50)
		seen[key] = true
		out = append(out, scored{h, score})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	res := make([]TorrentHit, len(out))
	for i := range out {
		res[i] = out[i].h
	}
	return res
}

// pickDebridFile choisit le fichier du titre : nom du titre dans le nom de
// fichier, numéro de piste (et de disque) en appui.
func pickDebridFile(files []DebridFile, t Track) (DebridFile, bool) {
	want := tokens(t.Title)
	best, bestScore := DebridFile{}, 0
	for _, f := range files {
		dir, base := path.Split(f.Path)
		name := strings.TrimSuffix(base, path.Ext(base))
		fn := foldWords(name)
		score := 0
		if len(want) > 0 && containsAll(fn, want) {
			score += 100
			// Un titre court (« Intro ») se retrouve dans d'autres noms : le
			// nom exact passe devant.
			if strings.TrimSpace(strings.TrimLeft(fn, "0123456789 ")) == strings.Join(want, " ") {
				score += 20
			}
		}
		if n := leadingNumber(fn); t.Number > 0 && n > 0 {
			if n == t.Number || n == t.Volume*100+t.Number {
				score += 50
				if t.Volume > 1 && !strings.Contains(foldWords(dir), strconv.Itoa(t.Volume)) && n < 100 {
					score -= 40 // même numéro, autre disque
				}
			}
		}
		if score > bestScore {
			best, bestScore = f, score
		}
	}
	return best, bestScore >= 100
}

func leadingNumber(s string) int {
	s = strings.TrimSpace(s)
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 || i > 4 {
		return 0
	}
	n, _ := strconv.Atoi(s[:i])
	return n
}

func debridQuality(p string) string {
	switch strings.ToLower(path.Ext(p)) {
	case ".flac", ".alac", ".wav":
		return "LOSSLESS"
	}
	return "HIGH"
}

// Resolvers essaie plusieurs résolveurs dans l'ordre (Eclipse, puis le
// compte debrid).
type Resolvers []Resolver

// Resolve renvoie le premier flux trouvé.
func (rs Resolvers) Resolve(ctx context.Context, t Track) (Stream, error) {
	var last error = ErrNotFound
	for _, r := range rs {
		s, err := r.Resolve(ctx, t)
		if err == nil && skipped(ctx, s.URL) {
			err = ErrNotFound // lien déjà refusé : source suivante
		}
		if err == nil {
			return s, nil
		}
		if ctx.Err() != nil {
			return Stream{}, ctx.Err()
		}
		last = err
	}
	return Stream{}, last
}

// foldWords normalise comme fold mais garde les mots séparés par une
// espace : « Racine Carrée (Deluxe) » donne « racine carree deluxe ».
func foldWords(s string) string {
	var b strings.Builder
	space := true
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		switch {
		case unicode.Is(unicode.Mn, r):
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			space = false
		case !space:
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

// IsAudio indique si un chemin désigne un fichier audio lisible.
func IsAudio(p string) bool {
	_, ok := audioExts[strings.ToLower(path.Ext(p))]
	return ok
}
