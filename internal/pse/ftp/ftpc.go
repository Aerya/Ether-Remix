package ftp

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/jlaffaye/ftp"
)

// ftpRemote est un serveur FTP ou FTPS. FTP ne fait passer qu'un
// transfert par connexion et ne reprend au milieu d'un fichier qu'avec une
// nouvelle commande (REST) : Remix garde une petite réserve de connexions
// (Connections) et, quand une lecture continue exactement où la précédente
// s'est arrêtée, réutilise le transfert déjà ouvert au lieu d'en rouvrir un.
type ftpRemote struct {
	s   Settings
	tls *tls.Config

	mu     sync.Mutex
	idle   []*ftp.ServerConn
	open   int // connexions ouvertes (libres, prêtées ou garées)
	parked []*parked
	freed  chan struct{}
	closed bool
}

// parked est un transfert interrompu à pos, gardé un court instant.
type parked struct {
	path  string
	pos   int64
	resp  *ftp.Response
	conn  *ftp.ServerConn
	timer *time.Timer
}

const parkFor = 3 * time.Second

func newFTP(s Settings) *ftpRemote {
	r := &ftpRemote{s: s, freed: make(chan struct{})}
	if s.Protocol == ProtoFTPS || s.Protocol == ProtoFTPSImpl {
		r.tls = ftpsConfig(s)
	}
	return r
}

// ftpsConfig vérifie le certificat du serveur : chaîne de confiance
// habituelle, ou empreinte enregistrée pour un certificat auto-signé.
// Le canal de données reprend la session TLS (exigé par vsftpd et
// d'autres).
func ftpsConfig(s Settings) *tls.Config {
	cfg := &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12, ClientSessionCache: tls.NewLRUClientSessionCache(16)}
	if pin := NormalizeFingerprint(s.HostKey); pin != "" && !strings.HasPrefix(pin, "SHA256:") {
		cfg.InsecureSkipVerify = true // remplacé par la vérification de l'empreinte
		cfg.VerifyConnection = func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("aucun certificat présenté")
			}
			if seen := CertFingerprint(cs.PeerCertificates[0]); seen != pin {
				return &CertError{Seen: seen, Expected: pin}
			}
			return nil
		}
	}
	return cfg
}

// CertFingerprint est l'empreinte SHA-256 d'un certificat, en hexadécimal.
func CertFingerprint(c *x509.Certificate) string {
	h := sha256.Sum256(c.Raw)
	return hex.EncodeToString(h[:])
}

// CertError : le certificat FTPS ne correspond pas à l'empreinte.
type CertError struct{ Seen, Expected string }

func (e *CertError) Error() string {
	return "le certificat FTPS a changé (vu : " + e.Seen + ", attendu : " + e.Expected + ")"
}

func (r *ftpRemote) dial(ctx context.Context) (*ftp.ServerConn, error) {
	opts := []ftp.DialOption{ftp.DialWithContext(ctx), ftp.DialWithTimeout(dialTimeout), ftp.DialWithShutTimeout(5 * time.Second),
		ftp.DialWithDialer(net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second})}
	switch r.s.Protocol {
	case ProtoFTPS:
		opts = append(opts, ftp.DialWithExplicitTLS(r.tls))
	case ProtoFTPSImpl:
		opts = append(opts, ftp.DialWithTLS(r.tls))
	}
	c, err := ftp.Dial(r.s.Addr(), opts...)
	if err != nil {
		return nil, err
	}
	if err := c.Login(r.s.User, r.s.Password); err != nil {
		_ = c.Quit()
		return nil, err
	}
	if err := c.Type(ftp.TransferTypeBinary); err != nil {
		_ = c.Quit()
		return nil, err
	}
	return c, nil
}

// get prête une connexion ; au-delà de la réserve, attend qu'une se libère
// (un transfert garé est fermé pour faire place).
func (r *ftpRemote) get(ctx context.Context) (*ftp.ServerConn, error) {
	for {
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			return nil, errClosed
		}
		if n := len(r.idle); n > 0 {
			c := r.idle[n-1]
			r.idle = r.idle[:n-1]
			r.mu.Unlock()
			return c, nil
		}
		if r.open < r.s.Connections {
			r.open++
			r.mu.Unlock()
			c, err := r.dial(ctx)
			if err != nil {
				r.release(nil)
				return nil, err
			}
			return c, nil
		}
		var victim *parked
		if len(r.parked) > 0 {
			victim = r.parked[0]
			r.parked = r.parked[1:]
		}
		wait := r.freed
		r.mu.Unlock()
		if victim != nil {
			if victim.timer.Stop() { // sinon, sa minuterie le ferme déjà
				r.finish(victim.resp, victim.conn)
			}
			continue
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-wait:
		}
	}
}

// put rend une connexion saine à la réserve.
func (r *ftpRemote) put(c *ftp.ServerConn) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		_ = c.Quit()
		r.release(nil)
		return
	}
	r.idle = append(r.idle, c)
	r.signal()
	r.mu.Unlock()
}

// release oublie une connexion (fermée ou cassée).
func (r *ftpRemote) release(c *ftp.ServerConn) {
	if c != nil {
		_ = c.Quit()
	}
	r.mu.Lock()
	r.open--
	r.signal()
	r.mu.Unlock()
}

func (r *ftpRemote) signal() {
	close(r.freed)
	r.freed = make(chan struct{})
}

// do exécute une commande sur une connexion de la réserve ; une connexion
// cassée est remplacée une fois.
func (r *ftpRemote) do(ctx context.Context, op func(*ftp.ServerConn) error) error {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		var c *ftp.ServerConn
		c, err = r.get(ctx)
		if err != nil {
			return err
		}
		err = op(c)
		if err == nil || ftpCode(err) != 0 {
			r.put(c) // réponse du serveur : la connexion reste bonne
			return err
		}
		r.release(c)
	}
	return err
}

// ftpCode renvoie le code de réponse du serveur, 0 pour une erreur réseau.
func ftpCode(err error) int {
	var te *textproto.Error
	if errors.As(err, &te) {
		return te.Code
	}
	return 0
}

func ftpNotFound(err error) bool {
	c := ftpCode(err)
	return c == ftp.StatusFileUnavailable || c == ftp.StatusFileActionIgnored
}

func (r *ftpRemote) ReadDir(ctx context.Context, dir string) ([]Entry, error) {
	var out []Entry
	err := r.do(ctx, func(c *ftp.ServerConn) error {
		list, err := c.List(dir)
		if err != nil {
			return err
		}
		out = out[:0]
		for _, e := range list {
			if e.Name == "." || e.Name == ".." || e.Name == "" || strings.Contains(e.Name, "/") {
				continue
			}
			ent := Entry{Path: path.Join(dir, e.Name), Size: int64(e.Size), MTime: e.Time, Dir: e.Type == ftp.EntryTypeFolder}
			if e.Type == ftp.EntryTypeLink { // lien : dossier ou fichier selon sa cible
				if n, err := c.FileSize(ent.Path); err == nil {
					ent.Size = n
				} else {
					ent.Dir = true
				}
			}
			out = append(out, ent)
		}
		return nil
	})
	if ftpNotFound(err) {
		return nil, ErrNotFound
	}
	return out, err
}

func (r *ftpRemote) Stat(ctx context.Context, p string) (Entry, error) {
	var e Entry
	err := r.do(ctx, func(c *ftp.ServerConn) error {
		n, err := c.FileSize(p)
		if err != nil {
			return err
		}
		e = Entry{Path: p, Size: n}
		if t, err := c.GetTime(p); err == nil {
			e.MTime = t
		}
		return nil
	})
	if ftpNotFound(err) {
		return Entry{}, ErrNotFound
	}
	return e, err
}

func (r *ftpRemote) Open(ctx context.Context, p string, off int64) (io.ReadCloser, error) {
	r.mu.Lock()
	for i, pk := range r.parked {
		if pk.path == p && pk.pos == off && pk.timer.Stop() {
			r.parked = append(r.parked[:i], r.parked[i+1:]...)
			r.mu.Unlock()
			return &ftpReader{r: r, path: p, pos: off, resp: pk.resp, conn: pk.conn}, nil
		}
	}
	r.mu.Unlock()
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		c, err := r.get(ctx)
		if err != nil {
			return nil, err
		}
		resp, err := c.RetrFrom(p, uint64(off))
		if err == nil {
			return &ftpReader{r: r, path: p, pos: off, resp: resp, conn: c}, nil
		}
		if ftpCode(err) != 0 {
			r.put(c)
			if ftpNotFound(err) {
				return nil, ErrNotFound
			}
			if off > 0 && (ftpCode(err) == ftp.StatusNotImplemented || ftpCode(err) == ftp.StatusBadArguments || ftpCode(err) == ftp.StatusNotImplementedParameter) {
				return nil, fmt.Errorf("%w : %v", ErrNoResume, err)
			}
			return nil, err
		}
		r.release(c)
		lastErr = err
	}
	return nil, lastErr
}

// ErrNoResume : le serveur FTP refuse de reprendre au milieu d'un fichier.
var ErrNoResume = errors.New("reprise (REST) refusée par le serveur FTP")

// finish ferme un transfert et rend (ou oublie) sa connexion.
func (r *ftpRemote) finish(resp *ftp.Response, c *ftp.ServerConn) {
	if err := resp.Close(); err != nil && ftpCode(err) == 0 {
		r.release(c)
		return
	}
	r.put(c)
}

// ftpReader est un transfert en cours. Close le gare un court instant :
// la plage suivante du moteur d'Ether commence souvent là.
type ftpReader struct {
	r    *ftpRemote
	path string
	pos  int64
	resp *ftp.Response
	conn *ftp.ServerConn
	err  error
	done bool
}

func (rd *ftpReader) Read(p []byte) (int, error) {
	n, err := rd.resp.Read(p)
	rd.pos += int64(n)
	if err != nil {
		rd.err = err
	}
	return n, err
}

func (rd *ftpReader) Close() error {
	if rd.done {
		return nil
	}
	rd.done = true
	if rd.err != nil {
		rd.r.finish(rd.resp, rd.conn)
		return nil
	}
	r := rd.r
	pk := &parked{path: rd.path, pos: rd.pos, resp: rd.resp, conn: rd.conn}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		r.finish(rd.resp, rd.conn)
		return nil
	}
	pk.timer = time.AfterFunc(parkFor, func() {
		r.mu.Lock()
		for i, x := range r.parked {
			if x == pk {
				r.parked = append(r.parked[:i], r.parked[i+1:]...)
				break
			}
		}
		r.mu.Unlock()
		r.finish(pk.resp, pk.conn)
	})
	r.parked = append(r.parked, pk)
	r.signal() // un demandeur en attente peut fermer ce transfert garé
	r.mu.Unlock()
	return nil
}

func (r *ftpRemote) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	idle, parkedList := r.idle, r.parked
	r.idle, r.parked = nil, nil
	r.signal()
	r.mu.Unlock()
	for _, pk := range parkedList {
		if pk.timer.Stop() {
			_ = pk.resp.Close()
			_ = pk.conn.Quit()
		}
	}
	for _, c := range idle {
		_ = c.Quit()
	}
	return nil
}

// ProbeCertificate lit l'empreinte du certificat d'un serveur FTPS, sans
// la vérifier (bouton « Tester la connexion », certificat auto-signé).
func ProbeCertificate(ctx context.Context, s Settings) (string, error) {
	cfg := &tls.Config{ServerName: s.Host, InsecureSkipVerify: true, MinVersion: tls.VersionTLS12} // lecture seule de l'empreinte
	var seen string
	cfg.VerifyConnection = func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) > 0 {
			seen = CertFingerprint(cs.PeerCertificates[0])
		}
		return errors.New("lecture de l'empreinte seulement")
	}
	opts := []ftp.DialOption{ftp.DialWithContext(ctx), ftp.DialWithTimeout(dialTimeout)}
	if s.Protocol == ProtoFTPSImpl {
		opts = append(opts, ftp.DialWithTLS(cfg))
	} else {
		opts = append(opts, ftp.DialWithExplicitTLS(cfg))
	}
	c, err := ftp.Dial(s.Addr(), opts...)
	if c != nil {
		_ = c.Quit()
	}
	if seen != "" {
		return seen, nil
	}
	return "", err
}
