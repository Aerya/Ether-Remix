package safety

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestDownloadAllowanceAndOwnerExemption(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err := New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	s.SetDownloadDefaults(func() core.DownloadAllowance {
		return core.DownloadAllowance{
			LimitUnit:   "titles",
			LimitAmount: 3,
			LimitPeriod: "day",
			MaxHeight:   1080,
			NoTranscode: true,
		}
	})

	owner := &core.User{ID: 1, IsOwner: true}
	friend := &core.User{ID: 2}

	// Owner is never limited on titles/GB or per-viewer height, while server-wide
	// NoTranscode still applies.
	oa := s.DownloadAllowance(owner)
	if oa.LimitUnit != "" || oa.LimitAmount != 0 || oa.MaxHeight != 0 || !oa.NoTranscode {
		t.Fatalf("owner allowance = %+v, want unlimited with NoTranscode=true", oa)
	}
	if dec := s.AdmitDownload(ctx, owner, core.MovieKey(1), 100<<30); !dec.Allowed || dec.Reserved {
		t.Fatalf("owner AdmitDownload = %+v, want allowed without reservation", dec)
	}

	// Friend inherits server defaults.
	fa := s.DownloadAllowance(friend)
	if fa.LimitUnit != "titles" || fa.LimitAmount != 3 || fa.LimitPeriod != "day" || fa.MaxHeight != 1080 || !fa.NoTranscode {
		t.Fatalf("friend default allowance = %+v", fa)
	}

	// DownloadMaxHeight is lower-only: raising above server 1080 stays 1080; lowering to 720 takes effect.
	if err := s.Put(ctx, friend.ID, Policy{DownloadMaxHeight: 2160, DownloadLimitUnit: "none"}); err != nil {
		t.Fatal(err)
	}
	fa = s.DownloadAllowance(friend)
	if fa.MaxHeight != 1080 || fa.LimitUnit != "" {
		t.Fatalf("friend raised height / none limit = %+v, want MaxHeight=1080, LimitUnit=''", fa)
	}
	if err := s.Put(ctx, friend.ID, Policy{DownloadMaxHeight: 720, DownloadLimitUnit: "gb", DownloadLimitAmount: 5, DownloadLimitPeriod: "month"}); err != nil {
		t.Fatal(err)
	}
	fa = s.DownloadAllowance(friend)
	if fa.MaxHeight != 720 || fa.LimitUnit != "gb" || fa.LimitAmount != 5 || fa.LimitPeriod != "month" {
		t.Fatalf("friend lower height / month GB = %+v", fa)
	}

	// Per-viewer DownloadTranscodingDisabled works independently of server switch and playback switch.
	s.SetDownloadDefaults(func() core.DownloadAllowance { return core.DownloadAllowance{} })
	if err := s.Put(ctx, friend.ID, Policy{VideoTranscodingDisabled: true}); err != nil {
		t.Fatal(err)
	}
	if s.DownloadAllowance(friend).NoTranscode {
		t.Fatalf("playback VideoTranscodingDisabled must not disable download conversion")
	}
	if err := s.Put(ctx, friend.ID, Policy{DownloadTranscodingDisabled: true}); err != nil {
		t.Fatal(err)
	}
	if !s.DownloadAllowance(friend).NoTranscode || s.VideoTranscodingDisabled(friend) {
		t.Fatalf("DownloadTranscodingDisabled must be independent of VideoTranscodingDisabled")
	}
}

func TestDownloadLimitReservationReconcileReleaseAndRollover(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err := New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 3, 15, 14, 30, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	friend := &core.User{ID: 2}
	if err := s.Put(ctx, friend.ID, Policy{
		DownloadLimitUnit:   "gb",
		DownloadLimitAmount: 5,
		DownloadLimitPeriod: "day",
	}); err != nil {
		t.Fatal(err)
	}

	k1, k2, k3 := core.MovieKey(101), core.MovieKey(102), core.MovieKey(103)

	// 1. Reserve 3 GB for k1 while in-flight.
	dec1 := s.AdmitDownload(ctx, friend, k1, 3<<30)
	if !dec1.Allowed || !dec1.Reserved {
		t.Fatalf("k1 admit = %+v, want allowed+reserved", dec1)
	}

	// 2. Concurrent k2 estimated at 3 GB cannot overshoot the 5 GB limit while k1 is in-flight.
	dec2 := s.AdmitDownload(ctx, friend, k2, 3<<30)
	wantReset := time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC)
	if dec2.Allowed || dec2.Unit != "gb" || dec2.Period != "day" || !dec2.ResetAt.Equal(wantReset) {
		t.Fatalf("k2 concurrent overshoot = %+v, want refused gb/day reset %v", dec2, wantReset)
	}

	// 3. Reconciling k1 to actual prepared 2 GB frees room so k2 (3 GB) now fits.
	if rec := s.ReconcileDownloadBytes(ctx, friend, k1, 2<<30); !rec.Allowed {
		t.Fatalf("k1 reconcile 2GB = %+v, want allowed", rec)
	}
	dec2 = s.AdmitDownload(ctx, friend, k2, 3<<30)
	if !dec2.Allowed || !dec2.Reserved {
		t.Fatalf("k2 admit after reconcile = %+v, want allowed+reserved", dec2)
	}

	// 4. Reconciling k2 to actual 4 GB overshoots 2 + 4 > 5 GB -> refused; caller releases k2's reservation.
	if rec := s.ReconcileDownloadBytes(ctx, friend, k2, 4<<30); rec.Allowed || rec.Unit != "gb" || !rec.ResetAt.Equal(wantReset) {
		t.Fatalf("k2 reconcile overshoot = %+v, want refused gb reset %v", rec, wantReset)
	}
	s.ReleaseDownloadReservation(ctx, friend, k2)

	// 5. After releasing k2, k3 at 2 GB fits cleanly.
	dec3 := s.AdmitDownload(ctx, friend, k3, 2<<30)
	if !dec3.Allowed || !dec3.Reserved {
		t.Fatalf("k3 admit after release = %+v, want allowed+reserved", dec3)
	}
	s.RecordDownloadBytes(ctx, friend, k3, 2<<30)

	// 6. Re-downloading k1 on the same day is free and returns Reserved=false.
	reK1 := s.AdmitDownload(ctx, friend, k1, 3<<30)
	if !reK1.Allowed || reK1.Reserved {
		t.Fatalf("same-day re-download k1 = %+v, want allowed without new reservation", reK1)
	}

	// 7. After UTC midnight, daily quota resets.
	now = time.Date(2026, 3, 16, 0, 5, 0, 0, time.UTC)
	nextDay := s.AdmitDownload(ctx, friend, k2, 4<<30)
	if !nextDay.Allowed || !nextDay.Reserved {
		t.Fatalf("next-day admit = %+v, want allowed after UTC reset", nextDay)
	}

	// 8. Monthly title limit resets on the 1st of the next month UTC.
	if err := s.Put(ctx, friend.ID, Policy{
		DownloadLimitUnit:   "titles",
		DownloadLimitAmount: 3,
		DownloadLimitPeriod: "month",
	}); err != nil {
		t.Fatal(err)
	}
	// Currently in March 2026: k1 (Mar 15), k3 (Mar 15), k2 (Mar 16) = 3 titles.
	overMonth := s.AdmitDownload(ctx, friend, core.MovieKey(104), 0)
	wantMonthReset := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	if overMonth.Allowed || overMonth.Unit != "titles" || overMonth.Period != "month" || !overMonth.ResetAt.Equal(wantMonthReset) {
		t.Fatalf("monthly over-limit = %+v, want refused titles/month reset %v", overMonth, wantMonthReset)
	}
	now = time.Date(2026, 4, 1, 0, 1, 0, 0, time.UTC)
	if ok := s.AdmitDownload(ctx, friend, core.MovieKey(104), 0); !ok.Allowed {
		t.Fatalf("new-month admit = %+v, want allowed", ok)
	}
}
