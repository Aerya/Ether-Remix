package torrentclient

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRT imite rTorrent en XML-RPC.
type fakeRT struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeRT) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "rt" || pass != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		b, _ := io.ReadAll(r.Body)
		body := string(b)
		method := between(body, "<methodName>", "</methodName>")
		f.mu.Lock()
		f.calls = append(f.calls, method+" "+body)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "text/xml")
		switch method {
		case "system.client_version":
			_, _ = io.WriteString(w, `<?xml version="1.0"?><methodResponse><params><param><value><string>0.9.8</string></value></param></params></methodResponse>`)
		case "d.multicall2":
			fin := time.Now().Add(-80 * time.Hour).Unix()
			_, _ = io.WriteString(w, `<?xml version="1.0"?><methodResponse><params><param><value><array><data>
<value><array><data><value><string>0123456789ABCDEF0123456789ABCDEF01234567</string></value><value><string>Album</string></value><value><string>remix</string></value><value><i8>1</i8></value><value><i8>350</i8></value><value><i8>`+itoa(fin)+`</i8></value><value><string>/data/Album</string></value><value><i8>1</i8></value><value><string></string></value></data></array></value>
<value><array><data><value><string>FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF</string></value><value><string>Autre</string></value><value><string>tv</string></value><value><i8>0</i8></value><value><i8>0</i8></value><value><i8>0</i8></value><value><string>/data/Autre</string></value><value><i8>1</i8></value><value><string></string></value></data></array></value>
</data></array></value></param></params></methodResponse>`)
		case "f.multicall":
			_, _ = io.WriteString(w, `<?xml version="1.0"?><methodResponse><params><param><value><array><data>
<value><array><data><value><string>01 Titre.flac</string></value><value><i8>10</i8></value></data></array></value>
</data></array></value></param></params></methodResponse>`)
		case "load.start", "d.custom.set", "d.stop":
			_, _ = io.WriteString(w, `<?xml version="1.0"?><methodResponse><params><param><value><i4>0</i4></value></param></params></methodResponse>`)
		default:
			_, _ = io.WriteString(w, `<?xml version="1.0"?><methodResponse><fault><value><struct><member><name>faultCode</name><value><i4>-506</i4></value></member><member><name>faultString</name><value><string>Method not defined</string></value></member></struct></value></fault></methodResponse>`)
		}
	})
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func between(s, a, b string) string {
	_, rest, _ := strings.Cut(s, a)
	v, _, _ := strings.Cut(rest, b)
	return v
}

func TestRTorrentListFilesAddAndStop(t *testing.T) {
	f := &fakeRT{}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	u, _ := url.Parse(srv.URL + "/RPC2")
	u.User = url.UserPassword("rt", "pw")
	r, err := NewRTorrent(u.String(), srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if v, err := r.Version(ctx); err != nil || v != "0.9.8" {
		t.Fatalf("version %q %v", v, err)
	}
	list, err := r.List(ctx, "remix")
	if err != nil || len(list) != 1 {
		t.Fatalf("liste %+v %v", list, err)
	}
	tr := list[0]
	if tr.Hash != hashA || !tr.Done || tr.Ratio != 0.35 || tr.Seeding < 79*time.Hour || tr.Root != "/data/Album" {
		t.Fatalf("torrent %+v", tr)
	}
	files, err := r.Files(ctx, tr)
	if err != nil || len(files) != 1 || files[0].Path != "/data/Album/01 Titre.flac" || files[0].Size != 10 {
		t.Fatalf("fichiers %+v %v", files, err)
	}
	if err := r.Add(ctx, AddOptions{Magnet: "magnet:?xt=urn:btih:" + hashA, Category: "remix", SavePath: "/data"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Stop(ctx, hashA); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(f.calls, "\n")
	if !strings.Contains(joined, "d.custom1.set=remix") || !strings.Contains(joined, "d.directory.set=/data") || !strings.Contains(joined, "d.stop") {
		t.Fatalf("appels %s", joined)
	}
	got, err := r.Get(ctx, strings.Repeat("e", 40))
	if err != nil || got != nil {
		t.Fatalf("absent %v %v", got, err)
	}
}

func TestXMLRPCFault(t *testing.T) {
	_, err := xmlrpcResponse(strings.NewReader(`<?xml version="1.0"?><methodResponse><fault><value><struct><member><name>faultCode</name><value><i4>-501</i4></value></member><member><name>faultString</name><value><string>Could not find info-hash.</string></value></member></struct></value></fault></methodResponse>`))
	f, ok := err.(*errFault)
	if !ok || f.code != -501 {
		t.Fatalf("erreur %v", err)
	}
}
