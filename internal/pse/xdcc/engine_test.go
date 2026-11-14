package xdcc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/debrid"
)

type noInner struct{}

func (noInner) Open(context.Context, string, []byte) (core.MediaSource, error) {
	return nil, errors.New("pas de Usenet ici")
}
func (noInner) Prefetch(context.Context, string, []byte, int64, int64) error { return nil }

// TestEtherEngineStreamsAGrowingXDCCFile passe par le vrai chemin de lecture
// d'Ether : DirectPayload (sonde 0-0 sur le lien local préparé par la source
// XDCC), puis le moteur debrid (blocs, lecture anticipée, reprises). Le
// fichier grandit pendant la lecture ; chaque octet lu doit être celui du
// bot, y compris en revenant en arrière.
func TestEtherEngineStreamsAGrowingXDCCFile(t *testing.T) {
	const size = 40 << 20 // au-delà du minimum d'Ether pour un flux HTTP (32 Mo)
	f, s, p, name := streamFixture(t, ModeStream, size)
	defer s.Close()
	f.receive(p, 2<<20)
	done := make(chan struct{})
	defer close(done)
	var paused atomic.Bool
	go func() { // le bot envoie 2 Mo toutes les 50 ms (~40 Mo/s)
		for {
			select {
			case <-done:
				return
			case <-time.After(50 * time.Millisecond):
				if !paused.Load() {
					f.receive(p, 2<<20)
				}
			}
		}
	}()
	debrid.SetPSEDirectPrepare(s.Prepare)
	defer debrid.SetPSEDirectPrepare(nil)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := debrid.NewService(config.Debrid{CachedOnly: true}, debrid.Options{Logger: quiet})
	rel := release(p, name, size)
	rel.ID, rel.StreamOnDemand = "xdcc-test-release", true
	ctx := playCtx(t)
	raw, err := svc.DirectPayload(ctx, rel)
	if err != nil {
		t.Fatal(err)
	}
	pl, err := debrid.DecodePayload(raw)
	if err != nil || pl.Size != size || !pl.IsDirect() {
		t.Fatalf("payload : %+v %v", pl, err)
	}
	eng := debrid.NewEngine(svc, debrid.EngineOptions{Inner: noInner{}, Logger: quiet, HedgeDelay: -1})
	defer eng.Close()
	src, err := eng.Open(ctx, rel.ID, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if src.Size() != size {
		t.Fatalf("taille %d", src.Size())
	}
	want := f.packets[p].content
	read := func(off, n int64) {
		t.Helper()
		buf := make([]byte, n)
		if _, err := src.ReadAt(buf, off); err != nil && err != io.EOF {
			t.Fatalf("lecture à %d : %v", off, err)
		}
		if !bytes.Equal(buf, want[off:off+n]) {
			t.Fatalf("octets faux à %d", off)
		}
	}
	// Lecture séquentielle comme un lecteur : début, puis la suite au fil
	// du transfert, puis un retour en arrière (déjà reçu : immédiat).
	for off := int64(0); off < 24<<20; off += 1 << 20 {
		read(off, 64<<10)
	}
	start := time.Now()
	read(1<<20, 64<<10)
	if time.Since(start) > 2*time.Second {
		t.Fatalf("retour en arrière lent : %s", time.Since(start))
	}
	// Le bot se tait : un saut au-delà de la partie reçue est refusé vite,
	// sans octets inventés (transfert séquentiel), et ce refus ne bloque
	// pas la lecture de ce qui est déjà reçu.
	paused.Store(true)
	time.Sleep(100 * time.Millisecond)
	f.mu.Lock()
	got := int64(f.packets[p].got)
	f.packets[p].Speed = 0
	f.mu.Unlock()
	_ = s.refresh(ctx, true)
	if got >= size {
		t.Fatal("transfert déjà fini : le test ne vérifie plus le saut")
	}
	start = time.Now()
	buf := make([]byte, 4096)
	if _, err := src.ReadAt(buf, size-4096); err == nil {
		t.Fatal("saut au-delà de la partie reçue servi alors que le bot se tait")
	}
	t.Logf("saut au-delà de la partie reçue (%d/%d) refusé en %s", got, size, time.Since(start).Round(time.Millisecond))
	t0 := time.Now()
	read(got-(1<<20), 64<<10) // reçu, jamais lu : nouvelle requête au serveur local
	if time.Since(t0) > 2*time.Second {
		t.Fatalf("lecture des octets reçus bloquée après un refus : %s", time.Since(t0))
	}
	paused.Store(false)
	start = time.Now()
	// Une fois les octets reçus, la même position se lit.
	deadline := time.Now().Add(20 * time.Second)
	for {
		buf := make([]byte, 4096)
		_, err := src.ReadAt(buf, size-4096)
		if err == nil || err == io.EOF {
			if !bytes.Equal(buf, want[size-4096:]) {
				t.Fatal("fin du fichier fausse")
			}
			t.Logf("fin du fichier lue %s après la reprise du bot", time.Since(start).Round(time.Millisecond))
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fin du fichier jamais lisible : %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
