package console

import (
	"strconv"
	"testing"

	"github.com/braedonsaunders/ether/internal/pse/continuewatch"
)

// Les bornes des réglages de « Continuer à regarder » sont contrôlées côté serveur.
func TestContinueWatchSettingsBounds(t *testing.T) {
	cases := []struct {
		key string
		ok  []string
		bad []string
	}{
		{continuewatch.KeyScanLimit, []string{"20", "200", "1000"}, []string{"19", "1001", "0", "-5", "abc", ""}},
		{continuewatch.KeyWatchDays, []string{"0", "90", "3650"}, []string{"-1", "3651", "x"}},
		{continuewatch.KeyNewDays, []string{"0", "60", "365"}, []string{"-1", "366"}},
		{continuewatch.KeyMaxMovies, []string{"0", "12", "200"}, []string{"-1", "201"}},
		{continuewatch.KeyMaxShows, []string{"0", "12", "200"}, []string{"-1", "201"}},
	}
	for _, c := range cases {
		fd, ok := fieldByKey[c.key]
		if !ok {
			t.Fatalf("réglage %s non déclaré", c.key)
		}
		for _, v := range c.ok {
			if _, err := validate(fd, v, nil); err != nil {
				t.Errorf("%s=%q refusé : %v", c.key, v, err)
			}
		}
		for _, v := range c.bad {
			if _, err := validate(fd, v, nil); err == nil {
				t.Errorf("%s=%q accepté", c.key, v)
			}
		}
	}
	d := continuewatch.Defaults()
	for key, want := range map[string]int{
		continuewatch.KeyScanLimit: d.ScanLimit, continuewatch.KeyWatchDays: d.WatchDays, continuewatch.KeyNewDays: d.NewDays,
		continuewatch.KeyMaxMovies: d.MaxMovies, continuewatch.KeyMaxShows: d.MaxShows,
	} {
		if got := fieldByKey[key].Default; got != strconv.Itoa(want) {
			t.Errorf("%s : défaut %q, attendu %d", key, got, want)
		}
	}
}
