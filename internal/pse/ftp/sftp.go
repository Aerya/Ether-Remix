package ftp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// sftpRemote garde une seule connexion SSH : SFTP y fait passer plusieurs
// lectures à la fois, à n'importe quelle position du fichier. Une coupure
// est réparée à la demande suivante.
type sftpRemote struct {
	s    Settings
	auth []ssh.AuthMethod

	mu      sync.Mutex
	ssh     *ssh.Client
	client  *sftp.Client
	gen     int // génération de la connexion : un échec ne coupe que la sienne
	handles map[string]*handle
	closed  bool
	stop    chan struct{}
}

type handle struct {
	f    *sftp.File
	gen  int
	used time.Time
}

const (
	dialTimeout   = 15 * time.Second
	handleIdle    = 5 * time.Minute
	sftpReadAhead = 64 // requêtes simultanées par fichier
)

func newSFTP(s Settings) (*sftpRemote, error) {
	auth, err := sshAuth(s)
	if err != nil {
		return nil, err
	}
	r := &sftpRemote{s: s, auth: auth, handles: map[string]*handle{}, stop: make(chan struct{})}
	go r.janitor()
	return r, nil
}

// sshAuth prépare l'authentification : clé privée, puis mot de passe
// (simple et « keyboard-interactive »).
func sshAuth(s Settings) ([]ssh.AuthMethod, error) {
	var out []ssh.AuthMethod
	if s.KeyFile != "" {
		raw, err := os.ReadFile(s.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("clé SSH illisible (%s) : vérifiez le volume monté", s.KeyFile)
		}
		var signer ssh.Signer
		if s.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(raw, []byte(s.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey(raw)
		}
		if err != nil {
			var missing *ssh.PassphraseMissingError
			if errors.As(err, &missing) {
				return nil, errors.New("la clé SSH est protégée : renseignez sa phrase de passe")
			}
			return nil, errors.New("clé SSH illisible ou phrase de passe incorrecte")
		}
		out = append(out, ssh.PublicKeys(signer))
	}
	if s.Password != "" {
		pw := s.Password
		out = append(out, ssh.Password(pw), ssh.KeyboardInteractive(func(_, _ string, qs []string, _ []bool) ([]string, error) {
			ans := make([]string, len(qs))
			for i := range qs {
				ans[i] = pw
			}
			return ans, nil
		}))
	}
	if len(out) == 0 {
		return nil, errors.New("mot de passe ou clé SSH manquant")
	}
	return out, nil
}

// HostKeyError : l'empreinte de l'hôte manque ou ne correspond pas.
type HostKeyError struct {
	Seen     string // empreinte présentée par le serveur
	Expected string // empreinte enregistrée ("" : aucune)
}

func (e *HostKeyError) Error() string {
	if e.Expected == "" {
		return "empreinte de l'hôte SSH non enregistrée (vue : " + e.Seen + ")"
	}
	return "l'empreinte de l'hôte SSH a changé (vue : " + e.Seen + ", attendue : " + e.Expected + ")"
}

// hostKeyCallback accepte seulement l'empreinte enregistrée.
func hostKeyCallback(expected string) ssh.HostKeyCallback {
	want := NormalizeFingerprint(expected)
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		seen := ssh.FingerprintSHA256(key)
		if want == "" || seen != want {
			return &HostKeyError{Seen: seen, Expected: want}
		}
		return nil
	}
}

func dialSSH(ctx context.Context, s Settings, auth []ssh.AuthMethod) (*ssh.Client, error) {
	d := net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", s.Addr())
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(dialTimeout))
	cfg := &ssh.ClientConfig{User: s.User, Auth: auth, HostKeyCallback: hostKeyCallback(s.HostKey), Timeout: dialTimeout}
	c, chans, reqs, err := ssh.NewClientConn(conn, s.Addr(), cfg)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return ssh.NewClient(c, chans, reqs), nil
}

// conn renvoie le client courant, reconnecté si besoin.
func (r *sftpRemote) conn(ctx context.Context) (*sftp.Client, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, 0, errClosed
	}
	if r.client != nil {
		return r.client, r.gen, nil
	}
	sc, err := dialSSH(ctx, r.s, r.auth)
	if err != nil {
		return nil, 0, err
	}
	cl, err := sftp.NewClient(sc, sftp.UseConcurrentReads(true), sftp.MaxConcurrentRequestsPerFile(sftpReadAhead))
	if err != nil {
		_ = sc.Close()
		return nil, 0, err
	}
	r.ssh, r.client = sc, cl
	r.gen++
	go func(gen int) { // la connexion tombe : la suivante la remplace
		_ = sc.Wait()
		r.drop(gen)
	}(r.gen)
	return cl, r.gen, nil
}

// drop oublie la connexion gen (et ses fichiers ouverts).
func (r *sftpRemote) drop(gen int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if gen != r.gen || r.client == nil {
		return
	}
	_ = r.client.Close()
	_ = r.ssh.Close()
	r.client, r.ssh = nil, nil
	for k, h := range r.handles {
		if h.gen == gen {
			delete(r.handles, k)
		}
	}
}

// retry relance une fois une opération coupée par une connexion perdue.
func (r *sftpRemote) retry(ctx context.Context, op func(*sftp.Client) error) error {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		var cl *sftp.Client
		var gen int
		cl, gen, err = r.conn(ctx)
		if err != nil {
			return err
		}
		err = op(cl)
		if err == nil || !connLost(err) {
			return err
		}
		r.drop(gen)
	}
	return err
}

func connLost(err error) bool {
	return errors.Is(err, sftp.ErrSSHFxConnectionLost) || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrUnexpectedEOF)
}

func notFound(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrNotExist)
}

func (r *sftpRemote) ReadDir(ctx context.Context, dir string) ([]Entry, error) {
	var out []Entry
	err := r.retry(ctx, func(cl *sftp.Client) error {
		infos, err := cl.ReadDirContext(ctx, dir)
		if err != nil {
			return err
		}
		out = out[:0]
		for _, fi := range infos {
			e := Entry{Path: path.Join(dir, fi.Name()), Size: fi.Size(), MTime: fi.ModTime(), Dir: fi.IsDir()}
			if fi.Mode()&fs.ModeSymlink != 0 { // lien : on suit sa cible
				if t, err := cl.Stat(e.Path); err == nil {
					e.Size, e.MTime, e.Dir = t.Size(), t.ModTime(), t.IsDir()
				} else {
					continue
				}
			}
			out = append(out, e)
		}
		return nil
	})
	if notFound(err) {
		return nil, ErrNotFound
	}
	return out, err
}

func (r *sftpRemote) Stat(ctx context.Context, p string) (Entry, error) {
	var e Entry
	err := r.retry(ctx, func(cl *sftp.Client) error {
		fi, err := cl.Stat(p)
		if err != nil {
			return err
		}
		e = Entry{Path: p, Size: fi.Size(), MTime: fi.ModTime(), Dir: fi.IsDir()}
		return nil
	})
	if notFound(err) {
		return Entry{}, ErrNotFound
	}
	return e, err
}

// file renvoie un fichier ouvert, gardé quelques minutes : les lectures
// suivantes du même film n'attendent pas une nouvelle ouverture.
func (r *sftpRemote) file(ctx context.Context, p string) (*sftp.File, int, error) {
	r.mu.Lock()
	if h := r.handles[p]; h != nil && h.gen == r.gen && r.client != nil {
		h.used = time.Now()
		f, gen := h.f, h.gen
		r.mu.Unlock()
		return f, gen, nil
	}
	r.mu.Unlock()
	var f *sftp.File
	var gen int
	err := r.retry(ctx, func(cl *sftp.Client) error {
		var err error
		f, err = cl.Open(p)
		r.mu.Lock()
		gen = r.gen
		r.mu.Unlock()
		return err
	})
	if err != nil {
		if notFound(err) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if old := r.handles[p]; old != nil && old.gen == gen {
		_ = f.Close() // ouvert en parallèle : on garde le premier
		old.used = time.Now()
		return old.f, gen, nil
	}
	r.handles[p] = &handle{f: f, gen: gen, used: time.Now()}
	return f, gen, nil
}

func (r *sftpRemote) Open(ctx context.Context, p string, off int64) (io.ReadCloser, error) {
	f, gen, err := r.file(ctx, p)
	if err != nil {
		return nil, err
	}
	return &sftpReader{r: r, f: f, gen: gen, off: off}, nil
}

// sftpReader lit par ReadAt : chaque appel avec un grand tampon part en
// plusieurs requêtes simultanées sur la connexion SSH.
type sftpReader struct {
	r   *sftpRemote
	f   *sftp.File
	gen int
	off int64
}

func (rd *sftpReader) Read(p []byte) (int, error) {
	n, err := rd.f.ReadAt(p, rd.off)
	rd.off += int64(n)
	if err != nil && err != io.EOF && connLost(err) {
		rd.r.drop(rd.gen)
	}
	if err == io.EOF && n > 0 {
		err = nil // io.ReaderAt peut rendre EOF avec les derniers octets
	}
	return n, err
}

func (rd *sftpReader) Close() error { return nil }

func (r *sftpRemote) janitor() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-t.C:
		}
		r.mu.Lock()
		for k, h := range r.handles {
			if time.Since(h.used) > handleIdle {
				_ = h.f.Close()
				delete(r.handles, k)
			}
		}
		r.mu.Unlock()
	}
}

func (r *sftpRemote) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	close(r.stop)
	// Fermer le transport avant les handles débloque les lectures distantes.
	if r.ssh != nil {
		_ = r.ssh.Close()
	}
	for _, h := range r.handles {
		_ = h.f.Close()
	}
	r.handles = map[string]*handle{}
	if r.client != nil {
		_ = r.client.Close()
		_ = r.ssh.Close()
		r.client, r.ssh = nil, nil
	}
	return nil
}

// ProbeHostKey se connecte sans s'authentifier pour lire l'empreinte de
// l'hôte (bouton « Tester la connexion »). Rien n'est accepté ici : c'est
// l'administrateur qui enregistre l'empreinte après l'avoir vérifiée.
func ProbeHostKey(ctx context.Context, s Settings) (string, error) {
	d := net.Dialer{Timeout: dialTimeout}
	conn, err := d.DialContext(ctx, "tcp", s.Addr())
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(dialTimeout))
	var seen string
	cfg := &ssh.ClientConfig{User: s.User, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		seen = ssh.FingerprintSHA256(key)
		return errors.New("lecture de l'empreinte seulement")
	}, Timeout: dialTimeout}
	_, _, _, err = ssh.NewClientConn(conn, s.Addr(), cfg)
	if seen != "" {
		return seen, nil
	}
	return "", err
}
