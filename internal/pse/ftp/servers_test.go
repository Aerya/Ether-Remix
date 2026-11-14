package ftp

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Serveurs de test lancés dans le test lui-même, sur 127.0.0.1 : jamais un
// serveur public.

const testUser, testPass = "remix", "secret-de-test-1"

// sftpTestServer est un serveur SSH/SFTP en lecture seule, enfermé dans
// root.
type sftpTestServer struct {
	addr        string
	fingerprint string
	opens       atomic.Int64

	mu    sync.Mutex
	conns []net.Conn
	ln    net.Listener
}

func startSFTP(t *testing.T, root string) *sftpTestServer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
		if c.User() == testUser && string(pw) == testPass {
			return nil, nil
		}
		return nil, errors.New("refusé")
	}}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &sftpTestServer{addr: ln.Addr().String(), fingerprint: ssh.FingerprintSHA256(signer.PublicKey()), ln: ln}
	t.Cleanup(func() { _ = ln.Close(); srv.dropAll() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			srv.mu.Lock()
			srv.conns = append(srv.conns, c)
			srv.mu.Unlock()
			go srv.handle(c, cfg, root)
		}
	}()
	return srv
}

// dropAll coupe toutes les connexions (panne réseau simulée).
func (s *sftpTestServer) dropAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		_ = c.Close()
	}
	s.conns = nil
}

func (s *sftpTestServer) handle(c net.Conn, cfg *ssh.ServerConfig, root string) {
	_, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "non")
			continue
		}
		ch, in, err := nc.Accept()
		if err != nil {
			return
		}
		go func() {
			for req := range in {
				ok := req.Type == "subsystem" && len(req.Payload) > 4 && string(req.Payload[4:]) == "sftp"
				_ = req.Reply(ok, nil)
			}
		}()
		h := &chroot{root: root, srv: s}
		rs := sftp.NewRequestServer(ch, sftp.Handlers{FileGet: h, FilePut: h, FileCmd: h, FileList: h})
		go func() { _ = rs.Serve(); _ = rs.Close() }()
	}
}

// chroot sert root en lecture seule.
type chroot struct {
	root string
	srv  *sftpTestServer
}

func (h *chroot) local(p string) string {
	return filepath.Join(h.root, filepath.FromSlash(path.Clean("/"+p)))
}

func (h *chroot) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	h.srv.opens.Add(1)
	return os.Open(h.local(r.Filepath))
}

func (h *chroot) Filewrite(*sftp.Request) (io.WriterAt, error) {
	return nil, sftp.ErrSSHFxPermissionDenied
}
func (h *chroot) Filecmd(*sftp.Request) error { return sftp.ErrSSHFxPermissionDenied }

type lister []os.FileInfo

func (l lister) ListAt(out []os.FileInfo, off int64) (int, error) {
	if off >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(out, l[off:])
	if n < len(out) {
		return n, io.EOF
	}
	return n, nil
}

func (h *chroot) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	p := h.local(r.Filepath)
	switch r.Method {
	case "List":
		entries, err := os.ReadDir(p)
		if err != nil {
			return nil, err
		}
		var out lister
		for _, e := range entries {
			if fi, err := e.Info(); err == nil {
				out = append(out, fi)
			}
		}
		return out, nil
	case "Stat":
		fi, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		return lister{fi}, nil
	}
	return nil, sftp.ErrSSHFxOpUnsupported
}

// ftpTestServer est un mini-serveur FTP en lecture seule (USER, PASS,
// TYPE, PASV, LIST, SIZE, MDTM, REST, RETR, QUIT), suffisant pour la
// bibliothèque FTP de Remix. Il compte les connexions et les transferts.
type ftpTestServer struct {
	addr      string
	root      string
	noRest    bool
	maxConns  int32
	open      atomic.Int32
	peak      atomic.Int32
	transfers atomic.Int64
}

func startFTP(t *testing.T, root string, maxConns int) *ftpTestServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &ftpTestServer{addr: ln.Addr().String(), root: root, maxConns: int32(maxConns)}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.session(c)
		}
	}()
	return s
}

func (s *ftpTestServer) session(c net.Conn) {
	defer c.Close()
	n := s.open.Add(1)
	defer s.open.Add(-1)
	for {
		p := s.peak.Load()
		if n <= p || s.peak.CompareAndSwap(p, n) {
			break
		}
	}
	w := bufio.NewWriter(c)
	reply := func(format string, a ...any) {
		fmt.Fprintf(w, format+"\r\n", a...)
		_ = w.Flush()
	}
	if s.maxConns > 0 && n > s.maxConns {
		reply("421 Too many connections")
		return
	}
	reply("220 test")
	rd := bufio.NewReader(c)
	var pasv net.Listener
	var rest int64
	authed := false
	defer func() {
		if pasv != nil {
			_ = pasv.Close()
		}
	}()
	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			return
		}
		cmd, arg, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " ")
		cmd = strings.ToUpper(cmd)
		local := filepath.Join(s.root, filepath.FromSlash(path.Clean("/"+arg)))
		switch {
		case cmd == "USER":
			reply("331 password")
		case cmd == "PASS":
			if arg != testPass {
				reply("530 Login incorrect")
				continue
			}
			authed = true
			reply("230 ok")
		case !authed:
			reply("530 Please login")
		case cmd == "FEAT":
			reply("211-Features:\r\n SIZE\r\n MDTM\r\n REST STREAM\r\n211 End")
		case cmd == "TYPE", cmd == "OPTS":
			reply("200 ok")
		case cmd == "PASV":
			if pasv != nil {
				_ = pasv.Close()
			}
			pasv, err = net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				reply("425 no")
				continue
			}
			port := pasv.Addr().(*net.TCPAddr).Port
			reply("227 Entering Passive Mode (127,0,0,1,%d,%d)", port/256, port%256)
		case cmd == "SIZE":
			fi, err := os.Stat(local)
			if err != nil || fi.IsDir() {
				reply("550 not found")
				continue
			}
			reply("213 %d", fi.Size())
		case cmd == "MDTM":
			fi, err := os.Stat(local)
			if err != nil {
				reply("550 not found")
				continue
			}
			reply("213 %s", fi.ModTime().UTC().Format("20060102150405"))
		case cmd == "REST":
			if s.noRest {
				reply("502 REST not implemented")
				continue
			}
			rest, _ = strconv.ParseInt(arg, 10, 64)
			reply("350 ok")
		case cmd == "LIST", cmd == "RETR":
			if pasv == nil {
				reply("425 use PASV")
				continue
			}
			var body io.Reader
			if cmd == "LIST" {
				if strings.HasPrefix(arg, "-") {
					arg, local = "/", s.root
				}
				entries, err := os.ReadDir(local)
				if err != nil {
					reply("550 not found")
					continue
				}
				var b strings.Builder
				for _, e := range entries {
					fi, err := e.Info()
					if err != nil {
						continue
					}
					mode := "-rw-r--r--"
					if fi.IsDir() {
						mode = "drwxr-xr-x"
					}
					fmt.Fprintf(&b, "%s 1 user group %d %s %s\r\n", mode, fi.Size(), fi.ModTime().UTC().Format("Jan 02 15:04"), fi.Name())
				}
				body = strings.NewReader(b.String())
			} else {
				f, err := os.Open(local)
				if err != nil {
					reply("550 not found")
					continue
				}
				defer f.Close()
				if _, err := f.Seek(rest, io.SeekStart); err != nil {
					reply("550 bad offset")
					continue
				}
				rest = 0
				body = f
				s.transfers.Add(1)
			}
			reply("150 opening")
			dc, err := pasv.Accept()
			_ = pasv.Close()
			pasv = nil
			if err != nil {
				reply("425 no data")
				continue
			}
			_, cerr := io.Copy(dc, body)
			_ = dc.Close()
			if cerr != nil {
				reply("426 aborted")
			} else {
				reply("226 done")
			}
		case cmd == "QUIT":
			reply("221 bye")
			return
		default:
			reply("502 not implemented")
		}
	}
}
