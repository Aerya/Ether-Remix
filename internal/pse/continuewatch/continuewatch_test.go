package continuewatch

import "testing"

func fake(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaultsWithoutValues(t *testing.T) {
	if got := From(fake(nil)); got != Defaults() {
		t.Fatalf("sans valeur enregistrée : %+v, attendu %+v", got, Defaults())
	}
	d := Defaults()
	if d.ScanLimit != 200 || d.WatchDays != 0 || d.NewDays != 60 || d.MaxMovies != 0 || d.MaxShows != 0 {
		t.Fatalf("valeurs par défaut inattendues : %+v", d)
	}
}

func TestFromReadsAndClamps(t *testing.T) {
	got := From(fake(map[string]string{
		KeyScanLimit: " 500 ", KeyWatchDays: "90", KeyNewDays: "30", KeyMaxMovies: "4", KeyMaxShows: "8",
	}))
	want := Settings{ScanLimit: 500, WatchDays: 90, NewDays: 30, MaxMovies: 4, MaxShows: 8}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	got = From(fake(map[string]string{KeyScanLimit: "5", KeyWatchDays: "-3", KeyNewDays: "9999", KeyMaxMovies: "abc", KeyMaxShows: "100000"}))
	want = Settings{ScanLimit: MinScanLimit, WatchDays: 0, NewDays: MaxNewDays, MaxMovies: 0, MaxShows: MaxQuantity}
	if got != want {
		t.Fatalf("bornes : got %+v, want %+v", got, want)
	}
	if got := From(fake(map[string]string{KeyScanLimit: "99999"})); got.ScanLimit != MaxScanLimit {
		t.Fatalf("plafond non borné : %d", got.ScanLimit)
	}
}
