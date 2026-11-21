package music

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	dz "github.com/Cycl0o0/OpenDeezer/v3/sdk/deezer"
)

// OpenDeezer : résolveur qui lit le titre Deezer demandé
// avec la session (ARL) du compte Deezer de l'administrateur, via le SDK
// OpenDeezer. Le flux est relayé au fil de l'eau (lecture, déchiffrement,
// envoi) : rien n'est écrit sur le disque et rien n'est téléchargé en
// entier avant la lecture. Seules les qualités autorisées par le compte sont
// demandées (MP3 128 kb/s pour un compte gratuit). L'aperçu de 30 secondes
// n'est jamais présenté comme un titre complet : la source est alors
// indisponible et le résolveur suivant prend le relais.

var (
	errODPreview   = errors.New("music: OpenDeezer : Deezer ne fournit qu'un aperçu de 30 s pour ce titre")
	errODSession   = errors.New("music: OpenDeezer : session Deezer (ARL) refusée ou expirée")
	errODNoARL     = errors.New("music: OpenDeezer : aucune session Deezer (ARL) enregistrée")
	errODCDN       = errors.New("music: OpenDeezer : le CDN Deezer a refusé le flux")
	errODTruncated = errors.New("music: OpenDeezer : flux interrompu")
)

// odStripe : le chiffrement par bandes de Deezer alterne un bloc chiffré et
// deux blocs en clair, tous de 2048 octets. Un flux reprenant à un multiple
// de 3 blocs (6144 octets) se déchiffre avec un déchiffreur neuf.
const odStripe = 3 * 2048

// odSession est la partie du SDK utilisée ; elle permet de la remplacer.
type odSession interface {
	Login() error
	Account() dz.Account
	PrepareStream(id string) (*dz.StreamPlan, error)
	NowPlaying(id string) error
}

// sdkSession adapte le client du SDK ; le signalement d'écoute (log.listen)
// n'est exposé que par le client interne, joignable via Unwrap.
type sdkSession struct{ *dz.Client }

func (s sdkSession) NowPlaying(id string) error { return dz.Unwrap(s.Client).NowPlaying(id) }

// OpenDeezer est le résolveur OpenDeezer.
type OpenDeezer struct {
	arl string
	hc  *http.Client

	mu      sync.Mutex
	sess    odSession
	failed  error
	retryAt time.Time
	logged  bool
}

// NewOpenDeezer prépare le résolveur ; hc sert au CDN (nil : défaut).
func NewOpenDeezer(arl string, hc *http.Client) *OpenDeezer {
	if hc == nil {
		var rt http.RoundTripper = http.DefaultTransport
		if tr, ok := rt.(*http.Transport); ok {
			tr = tr.Clone()
			tr.ResponseHeaderTimeout = 30 * time.Second
			rt = tr
		}
		hc = &http.Client{Transport: rt}
	}
	o := &OpenDeezer{arl: strings.TrimSpace(arl), hc: hc}
	if o.arl != "" {
		c := dz.New(o.arl)
		c.SetQuality(dz.QualityHigh) // jamais FLAC : le SDK ne demande que les formats autorisés
		o.sess = sdkSession{c}
	}
	return o
}

// scrub retire l'ARL de tout message d'erreur avant qu'il soit affiché ou journalisé.
func (o *OpenDeezer) scrub(err error) error {
	if err == nil || o.arl == "" || !strings.Contains(err.Error(), o.arl) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), o.arl, "[ARL]"))
}

// call exécute une opération bloquante du SDK en respectant l'annulation.
func odCall[T any](ctx context.Context, f func() (T, error)) (T, error) {
	type res struct {
		v   T
		err error
	}
	ch := make(chan res, 1)
	go func() { v, err := f(); ch <- res{v, err} }()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

func (o *OpenDeezer) login(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.sess == nil {
		return errODNoARL
	}
	if o.logged {
		return nil
	}
	if o.failed != nil && time.Now().Before(o.retryAt) {
		return o.failed
	}
	_, err := odCall(ctx, func() (struct{}, error) { return struct{}{}, o.sess.Login() })
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, dz.ErrARLExpired) {
			err = errODSession
			o.retryAt = time.Now().Add(5 * time.Minute)
		} else {
			err = fmt.Errorf("music: OpenDeezer : connexion Deezer impossible : %w", o.scrub(err))
			o.retryAt = time.Now().Add(20 * time.Second)
		}
		o.failed = err
		return err
	}
	o.logged, o.failed = true, nil
	return nil
}

// Status renvoie l'offre du compte connecté (test des réglages).
func (o *OpenDeezer) Status(ctx context.Context) (string, error) {
	if err := o.login(ctx); err != nil {
		return "", err
	}
	a := o.sess.Account()
	if a.Offer == "" {
		return "Deezer", nil
	}
	return a.Offer, nil
}

// Resolve trouve le flux complet du titre, sans rien télécharger.
func (o *OpenDeezer) Resolve(ctx context.Context, t Track) (Stream, error) {
	if t.ID <= 0 {
		return Stream{}, ErrNotFound
	}
	if err := o.login(ctx); err != nil {
		return Stream{}, err
	}
	id := strconv.FormatInt(t.ID, 10)
	plan, err := odCall(ctx, func() (*dz.StreamPlan, error) { return o.sess.PrepareStream(id) })
	if err != nil {
		if ctx.Err() != nil {
			return Stream{}, ctx.Err()
		}
		return Stream{}, fmt.Errorf("music: OpenDeezer : titre indisponible pour ce compte : %w", o.scrub(err))
	}
	if plan == nil || plan.CDNURL == "" || plan.Preview || !plan.Encrypted {
		return Stream{}, errODPreview
	}
	r := &odReader{o: o, ctx: ctx, id: id, plan: plan}
	// Ouverture immédiate : un refus du CDN fait passer à la source suivante
	// avant d'avoir répondu au client.
	if err := r.open(0); err != nil {
		return Stream{}, err
	}
	if r.size <= 0 || odLooksLikePreview(plan.Format, r.size, t.Duration) {
		r.Close()
		return Stream{}, errODPreview
	}
	mime := "audio/mpeg"
	if strings.EqualFold(plan.Format, "FLAC") {
		mime = "audio/flac"
	}
	reported := false
	return Stream{
		Provider: "OpenDeezer",
		URL:      "opendeezer://track/" + id, // identifiant seulement, jamais une adresse jouable
		Mime:     mime,
		Quality:  dz.FormatLabel(plan.Format),
		Open: func() (io.ReadSeekCloser, time.Time, error) {
			// Comme le lecteur web, signale l'écoute à Deezer (compteur d'écoutes
			// du compte gratuit), sauf pendant un test des sources.
			if !reported && !CachedOnly(ctx) {
				reported = true
				go func() { _ = o.sess.NowPlaying(id) }()
			}
			return r, time.Time{}, nil
		},
	}, nil
}

// odLooksLikePreview détecte un aperçu présenté comme un flux complet : un
// titre de plus d'une minute dont le fichier ne dépasse pas ~35 s d'audio.
func odLooksLikePreview(format string, size int64, seconds int) bool {
	rate := map[string]int64{"MP3_64": 8000, "MP3_128": 16000, "MP3_MISC": 16000, "MP3_320": 40000}[strings.ToUpper(format)]
	return rate > 0 && seconds > 60 && size < rate*35
}

// odReader lit le flux Deezer à la demande : lecture en cours, déchiffrement
// et envoi, avec reprise HTTP Range et déplacement (Seek) sans stockage.
type odReader struct {
	o    *OpenDeezer
	ctx  context.Context
	id   string
	plan *dz.StreamPlan

	size int64
	pos  int64 // position logique (octets de sortie déjà livrés)

	body    io.ReadCloser
	bodyPos int64 // position logique correspondant à la prochaine sortie du corps
	cancel  context.CancelFunc
	dec     *dz.StripeDecryptor
	skip    int64 // octets déchiffrés à écarter (alignement)
	pending []byte
	eof     bool
	refresh int
	closed  bool
}

func (r *odReader) closeBody() {
	if r.cancel != nil {
		r.cancel()
	}
	if r.body != nil {
		r.body.Close()
	}
	r.body, r.cancel, r.dec, r.pending, r.skip, r.eof = nil, nil, nil, nil, 0, false
}

// open ouvre le flux CDN à la position logique off (alignée vers le bas).
func (r *odReader) open(off int64) error {
	r.closeBody()
	aligned := off - off%odStripe
	for {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		if err := checkAudioHost(r.ctx, r.plan.CDNURL); err != nil {
			return errODCDN
		}
		ctx, cancel := context.WithCancel(r.ctx)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.plan.CDNURL, nil)
		if err != nil {
			cancel()
			return errODCDN
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 OpenDeezer/0.1")
		if aligned > 0 {
			req.Header.Set("Range", "bytes="+strconv.FormatInt(aligned, 10)+"-")
		}
		res, err := r.o.hc.Do(req)
		if err != nil {
			cancel()
			if r.ctx.Err() != nil {
				return r.ctx.Err()
			}
			return fmt.Errorf("music: OpenDeezer : CDN injoignable : %w", redactErr(err))
		}
		switch {
		case (res.StatusCode == http.StatusForbidden || res.StatusCode == http.StatusGone) && r.plan.Refresh != nil && r.refresh < 2:
			// Lien signé expiré : nouvelle résolution du même titre.
			res.Body.Close()
			cancel()
			r.refresh++
			np, err := odCall(r.ctx, r.plan.Refresh)
			if err != nil || np == nil || np.Preview || !np.Encrypted || np.CDNURL == "" {
				return errODCDN
			}
			r.plan = np
			continue
		case res.StatusCode != http.StatusOK && res.StatusCode != http.StatusPartialContent:
			res.Body.Close()
			cancel()
			return errODCDN
		}
		total := odTotal(res)
		if total > 0 {
			r.size = total
		}
		if aligned > 0 && res.StatusCode == http.StatusOK {
			// Range ignoré : on écarte les octets avant la position.
			if _, err := io.CopyN(io.Discard, res.Body, aligned); err != nil {
				res.Body.Close()
				cancel()
				return errODTruncated
			}
		}
		dec, err := dz.NewStripeDecryptor(r.id)
		if err != nil {
			res.Body.Close()
			cancel()
			return errODCDN
		}
		r.body, r.cancel, r.dec = res.Body, cancel, dec
		r.skip, r.bodyPos = off-aligned, off
		return nil
	}
}

// odTotal renvoie la taille totale du fichier annoncée par le CDN.
func odTotal(res *http.Response) int64 {
	if cr := res.Header.Get("Content-Range"); cr != "" {
		if i := strings.LastIndex(cr, "/"); i >= 0 {
			if n, err := strconv.ParseInt(strings.TrimSpace(cr[i+1:]), 10, 64); err == nil {
				return n
			}
		}
	}
	if res.StatusCode == http.StatusOK && res.ContentLength > 0 {
		return res.ContentLength
	}
	return 0
}

func (r *odReader) Read(p []byte) (int, error) {
	if r.closed {
		return 0, errors.New("music: OpenDeezer : flux fermé")
	}
	if len(p) == 0 {
		return 0, nil
	}
	if r.pos >= r.size {
		return 0, io.EOF
	}
	failures := 0
	for {
		if err := r.ctx.Err(); err != nil {
			return 0, err
		}
		if r.body == nil || r.bodyPos != r.pos {
			if err := r.open(r.pos); err != nil {
				return 0, err
			}
		}
		if len(r.pending) > 0 {
			n := copy(p, r.pending)
			r.pending = r.pending[n:]
			r.pos += int64(n)
			r.bodyPos = r.pos
			return n, nil
		}
		if r.eof {
			return 0, io.ErrUnexpectedEOF
		}
		buf := make([]byte, 32<<10)
		n, err := r.body.Read(buf)
		if n > 0 {
			out := r.dec.Feed(buf[:n], nil)
			if err == io.EOF {
				out = r.dec.Finish(out)
			}
			if r.skip > 0 {
				d := min(r.skip, int64(len(out)))
				out, r.skip = out[d:], r.skip-d
			}
			// Jamais au-delà de la taille annoncée.
			if rest := r.size - r.pos; int64(len(out)) > rest {
				out = out[:rest]
			}
			r.pending = out
		}
		if err != nil {
			if err == io.EOF {
				r.eof = true
				if len(r.pending) == 0 {
					if r.pos >= r.size {
						return 0, io.EOF
					}
					return 0, io.ErrUnexpectedEOF
				}
			} else {
				// Coupure en cours de lecture : reprise à la position courante.
				if r.ctx.Err() != nil {
					return 0, r.ctx.Err()
				}
				if len(r.pending) == 0 {
					failures++
					if failures > 3 {
						return 0, errODTruncated
					}
					r.closeBody()
					time.Sleep(time.Duration(failures) * 200 * time.Millisecond)
				} else {
					// Livre d'abord ce qui est prêt ; la reprise se fera à la lecture suivante.
					defer func() { r.body = nil }()
				}
			}
		}
	}
}

func (r *odReader) Seek(offset int64, whence int) (int64, error) {
	var n int64
	switch whence {
	case io.SeekStart:
		n = offset
	case io.SeekCurrent:
		n = r.pos + offset
	case io.SeekEnd:
		n = r.size + offset
	default:
		return 0, errors.New("music: OpenDeezer : position invalide")
	}
	if n < 0 {
		return 0, errors.New("music: OpenDeezer : position négative")
	}
	r.pos = n // le flux est rouvert à la prochaine lecture si besoin
	return n, nil
}

func (r *odReader) Close() error {
	r.closed = true
	r.closeBody()
	return nil
}
