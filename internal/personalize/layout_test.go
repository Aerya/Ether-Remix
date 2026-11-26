package personalize

import (
	"math"
	"testing"
)

func TestPlacePinned(t *testing.T) {
	got := placePinned([]string{"a", "b", "c"}, []string{"z@9", "x@0", "y@2"}, func(s string) int {
		switch s {
		case "x@0":
			return 0
		case "y@2":
			return 2
		}
		return 9
	})
	want := []string{"x@0", "a", "y@2", "b", "c", "z@9"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("placePinned = %v, want %v", got, want)
		}
	}
}

func TestBanditPosterior(t *testing.T) {
	// No evidence: the hand-set prior.
	mean, _ := rowRate(0.1, arm{}, arm{}, 0, nil)
	if !approx(mean, 0.1, 1e-9) {
		t.Fatalf("prior mean = %v", mean)
	}
	// Server-wide evidence moves the estimate for everyone…
	globalGood := arm{shows: 300, reward: 90}
	mean, _ = rowRate(0.1, globalGood, arm{}, 0, nil)
	if mean < 0.25 {
		t.Fatalf("global evidence ignored: %v", mean)
	}
	// …and a viewer's own evidence moves theirs further.
	userBad := arm{shows: 200, reward: 0}
	meanBad, _ := rowRate(0.1, globalGood, userBad, 0, nil)
	if meanBad > 0.1 {
		t.Fatalf("viewer who never plays this row still rated %v", meanBad)
	}
	// Thompson samples are deterministic per seed and spread around the mean.
	_, s1 := rowRate(0.1, arm{}, arm{}, 1, seeded("u", 1, "day", 20261014))
	_, s2 := rowRate(0.1, arm{}, arm{}, 1, seeded("u", 1, "day", 20261014))
	if s1 != s2 {
		t.Fatal("same seed, different sample")
	}
	sum, sumSq, n := 0.0, 0.0, 2000
	for i := 0; i < n; i++ {
		_, s := rowRate(0.1, arm{}, arm{}, 1, seeded("u", i))
		sum += s
		sumSq += s * s
	}
	m := sum / float64(n)
	sd := math.Sqrt(sumSq/float64(n) - m*m)
	if !approx(m, 0.1, 0.01) || sd < 0.02 || sd > 0.08 {
		t.Fatalf("sample mean %v sd %v", m, sd)
	}
	// More evidence, less exploration.
	sumSq, sum = 0, 0
	for i := 0; i < n; i++ {
		_, s := rowRate(0.1, arm{}, arm{shows: 2000, reward: 200}, 1, seeded("v", i))
		sum += s
		sumSq += s * s
	}
	m2 := sum / float64(n)
	sd2 := math.Sqrt(sumSq/float64(n) - m2*m2)
	if sd2 >= sd/2 {
		t.Fatalf("evidence should shrink exploration: sd %v vs %v", sd2, sd)
	}
}
