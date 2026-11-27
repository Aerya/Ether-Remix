package ftp

import (
	"context"
	"errors"
	"net"
	"net/textproto"
	"testing"
	"time"
)

// La tolérance du serveur de test pour les fermetures en cours ne doit pas
// masquer un vrai dépassement : les deux premières sessions restent ouvertes.
func TestFTPServerRejectsExcessConnections(t *testing.T) {
	srv := startFTP(t, t.TempDir(), 2)
	for i := 0; i < 3; i++ {
		conn, err := net.DialTimeout("tcp", srv.addr, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		c := textproto.NewConn(conn)
		want := 220
		if i == 2 {
			want = 421
		}
		if _, _, err := c.ReadResponse(want); err != nil {
			t.Fatal(err)
		}
	}
	if got := srv.peak.Load(); got != 3 {
		t.Fatalf("pic %d, attendu 3", got)
	}
}

// Vérifie directement la limite côté client, indépendamment de la tolérance
// du serveur : une troisième demande attend, puis réutilise une place rendue.
func TestFTPPoolWaitsAtConnectionLimit(t *testing.T) {
	srv := startFTP(t, t.TempDir(), 0)
	s := ftpSettings(srv.addr)
	s.Connections = 2
	rm := newFTP(s)
	defer rm.Close()
	first, err := rm.get(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	defer rm.release(first)
	second, err := rm.get(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	defer rm.release(second)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if extra, err := rm.get(ctx); !errors.Is(err, context.DeadlineExceeded) {
		if extra != nil {
			rm.release(extra)
		}
		t.Fatalf("troisième connexion : %v, attendu une expiration", err)
	}
	rm.put(second)
	reused, err := rm.get(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	if reused != second {
		t.Fatal("la connexion rendue n'a pas été réutilisée")
	}
	if got := srv.peak.Load(); got != 2 {
		t.Fatalf("pic %d, attendu 2", got)
	}
}
