package releasedb

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Test réel facultatif : PSE_PRE_LIVE=1 go test ./internal/pse/releasedb -run PreLive -v
// Lit les vrais flux publics (sans rien enregistrer). Avec PSE_PRE_LIVE_PROXY_URL (et
// PSE_PRE_LIVE_PROXY_TYPE=proxy|mediaflow, PSE_PRE_LIVE_PROXY_AUTH), passe par ce proxy.
func TestPreLive(t *testing.T) {
	if os.Getenv("PSE_PRE_LIVE") == "" {
		t.Skip("PSE_PRE_LIVE absent")
	}
	vals := map[string]string{
		"PSE_PREDB_PROXY_TYPE": os.Getenv("PSE_PRE_LIVE_PROXY_TYPE"),
		"PSE_PREDB_PROXY_URL":  os.Getenv("PSE_PRE_LIVE_PROXY_URL"),
		"PSE_PREDB_PROXY_AUTH": os.Getenv("PSE_PRE_LIVE_PROXY_AUTH"),
	}
	cfg := LoadPreDBSettingsWith(func(k string) string { return vals[k] })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	res := TestPreFeeds(ctx, cfg)
	if res.Proxy != "" {
		t.Fatalf("proxy : %s", res.Proxy)
	}
	ok := 0
	for _, r := range res.Runs {
		line := r.Feed + " → " + r.State
		if r.State == "ok" {
			ok++
			line += " · " + itoa(r.Fetched) + " annonces · " + itoa(r.Kept) + " retenues"
			if len(r.Sample) > 0 {
				line += " · ex : " + strings.Join(r.Sample, " | ")
			}
		} else {
			line += " · " + r.Error
		}
		t.Log(line)
	}
	if ok == 0 {
		t.Fatal("aucun flux lisible")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
