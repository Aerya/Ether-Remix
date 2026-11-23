package debrid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

// TorBox defaults allow_zip=true and zips torrents of 100+ files into a single
// archive unless createtorrent sends allow_zip=false (#667).
func TestTorBoxLargeTorrentDisablesAutoZipAndSelectsNamedFile(t *testing.T) {
	var sawAllowZip string
	var sawListFilesParam string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/torrents/createtorrent":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse multipart: %v", err)
			}
			sawAllowZip = r.FormValue("allow_zip")
			fmt.Fprint(w, `{"success":true,"data":{"torrent_id":88}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/torrents/mylist":
			if r.URL.Query().Get("id") == "" {
				sawListFilesParam = r.URL.Query().Get("files")
				fmt.Fprintf(w, `{"success":true,"data":[{"id":88,"hash":"%s","download_present":true}]}`, testHash)
				return
			}
			type tbFile struct {
				ID       int    `json:"id"`
				Name     string `json:"name"`
				Size     int64  `json:"size"`
				MIMEType string `json:"mimetype"`
				Zipped   bool   `json:"zipped"`
			}
			var files []tbFile
			if sawAllowZip != "false" {
				files = []tbFile{{ID: 0, Name: "ROMs.zip", Size: 50 << 30, MIMEType: "application/zip", Zipped: true}}
			} else {
				files = make([]tbFile, 150)
				for i := range files {
					files[i] = tbFile{
						ID:       i,
						Name:     fmt.Sprintf("ROMs/Game %03d (USA).zip", i),
						Size:     2 << 20,
						MIMEType: "application/zip",
					}
				}
			}
			raw, _ := json.Marshal(files)
			fmt.Fprintf(w, `{"success":true,"data":{"id":88,"hash":"%s","download_present":true,"download_finished":true,"files":%s}}`, testHash, raw)
		default:
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := newTorBox(testKey, fastOpts(srv.URL))
	req := acquireReq(true)
	req.Choose = func(fs []File) (File, error) {
		return ChooseFile(fs, &core.ReleaseTarget{
			ExactFile:     "Game 123 (USA).zip",
			AllowNonVideo: true,
		})
	}
	acq, err := p.Acquire(context.Background(), req)
	if err != nil {
		t.Fatalf("Acquire large torrent: %v", err)
	}
	if sawAllowZip != "false" {
		t.Fatalf("createtorrent allow_zip = %q, want %q", sawAllowZip, "false")
	}
	if acq.File.ID != "123" || acq.File.Path != "ROMs/Game 123 (USA).zip" {
		t.Fatalf("chosen = %+v, want Game 123 (USA).zip", acq.File)
	}
	// Account-wide mylist requests omit files to avoid multi-MB JSON bloat.
	if _, err := p.loadList(context.Background()); err != nil {
		t.Fatalf("loadList: %v", err)
	}
	if sawListFilesParam != "false" {
		t.Fatalf("mylist files param = %q, want %q", sawListFilesParam, "false")
	}
}

// DOWNLOAD_TOO_LARGE preserves TorBox's reported plan limit in Service.Acquire,
// fails over to another provider, does not trip TorBox's circuit breaker, and
// is refused (never reported as queued) by QueueDownload (#667).
func TestTorBoxDownloadTooLargeFailoverNoBreakerAndNoQueue(t *testing.T) {
	tbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"success":false,"error":"DOWNLOAD_TOO_LARGE","detail":"Download exceeds your Essential plan limit of 200 GB"}`)
	}))
	defer tbSrv.Close()

	tb := newTorBox(testKey, fastOpts(tbSrv.URL))
	rel := core.Release{
		ID:       "huge-pack",
		Name:     "Huge.Pack.1.27TB",
		Protocol: core.ProtocolTorrent,
		InfoHash: testHash,
		Target:   &core.ReleaseTarget{ExactFile: "Game.zip", AllowNonVideo: true},
	}

	// Single-provider TorBox: error keeps ErrNotCached and names the plan limit,
	// and repeated hits never open the circuit breaker.
	cfgSolo := config.Debrid{
		CachedOnly:     true,
		RemoveUncached: true,
		Providers:      []config.DebridProvider{{ID: "tb", Kind: "torbox", Name: "TorBox", APIKey: testKey, Enabled: true}},
	}
	svcSolo := NewService(cfgSolo, Options{
		NewProvider: func(config.DebridProvider, ProviderOptions) (Provider, error) { return tb, nil },
	})
	for i := 0; i < 4; i++ {
		r := rel
		r.InfoHash = fmt.Sprintf("%040x", i+1)
		_, err := svcSolo.Acquire(context.Background(), r)
		if !errors.Is(err, core.ErrNotCached) {
			t.Fatalf("Acquire[%d] err = %v, want core.ErrNotCached", i, err)
		}
		if !strings.Contains(err.Error(), "200 GB") {
			t.Fatalf("Acquire[%d] err = %q, want TorBox plan size limit detail", i, err.Error())
		}
	}
	if st := svcSolo.Status(); len(st.Providers) != 1 || st.Providers[0].State != "ok" || st.Providers[0].Failures != 0 {
		t.Fatalf("DOWNLOAD_TOO_LARGE tripped breaker/failures: %+v", st.Providers)
	}

	// QueueDownload must refuse (never claim the torrent was queued).
	cfgQueue := cfgSolo
	cfgQueue.CachedOnly = false
	svcQueue := NewService(cfgQueue, Options{
		NewProvider: func(config.DebridProvider, ProviderOptions) (Provider, error) { return tb, nil },
	})
	if _, err := svcQueue.QueueDownload(context.Background(), rel); !errors.Is(err, core.ErrDownloadRefused) || errors.Is(err, core.ErrDownloadQueued) {
		t.Fatalf("QueueDownload err = %v, want ErrDownloadRefused", err)
	}

	// Multi-provider failover: a second provider still serves the release.
	media := newMediaServer(t, 1024)
	pm := newFakeProvider("premiumize", media, []File{{ID: "0", Path: "Game.zip", Size: 1024}})
	pm.cached[testHash] = true
	cfgPair := config.Debrid{
		CachedOnly: true,
		Providers: []config.DebridProvider{
			{ID: "tb", Kind: "torbox", Name: "TorBox", APIKey: testKey, Enabled: true, Priority: 1},
			{ID: "pm", Kind: "premiumize", Name: "Premiumize", APIKey: testKey, Enabled: true, Priority: 2},
		},
	}
	svcPair := NewService(cfgPair, Options{
		NewProvider: func(c config.DebridProvider, _ ProviderOptions) (Provider, error) {
			if c.ID == "tb" {
				return tb, nil
			}
			return pm, nil
		},
	})
	payload, err := svcPair.Acquire(context.Background(), rel)
	if err != nil || !IsPayload(payload) {
		t.Fatalf("failover after DOWNLOAD_TOO_LARGE failed: %v", err)
	}
}

// If TorBox already cached a >100-file torrent as a single zipped archive
// despite allow_zip=false, TorBox must not return ErrWrongContent (which aborts
// failover to other providers and falsely blames the plugin).
func TestTorBoxPreCachedZippedArchiveFailsOverAndSummarizes(t *testing.T) {
	var deleted int
	tbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/torrents/createtorrent":
			fmt.Fprint(w, `{"success":true,"data":{"torrent_id":91}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/torrents/mylist":
			fmt.Fprintf(w, `{"success":true,"data":{"id":91,"hash":"%s","download_present":true,"download_finished":true,"files":[{"id":0,"name":"No-Intro.zip","size":4000000000,"mimetype":"application/zip","zipped":true}]}}`, testHash)
		case r.Method == http.MethodPost && r.URL.Path == "/torrents/controltorrent":
			deleted++
			fmt.Fprint(w, `{"success":true,"data":null}`)
		}
	}))
	defer tbSrv.Close()

	tb := newTorBox(testKey, fastOpts(tbSrv.URL))
	rel := core.Release{
		ID:       "zipped-pack",
		Name:     "No-Intro.Pack",
		Protocol: core.ProtocolTorrent,
		InfoHash: testHash,
		Target:   &core.ReleaseTarget{ExactFile: "Super Mario World (USA).zip", AllowNonVideo: true},
	}

	// Solo TorBox: reports ErrNotCached (never ErrWrongContent) and names the zip.
	svcSolo := NewService(config.Debrid{
		CachedOnly:     true,
		RemoveUncached: true,
		Providers:      []config.DebridProvider{{ID: "tb", Kind: "torbox", Name: "TorBox", APIKey: testKey, Enabled: true}},
	}, Options{
		NewProvider: func(config.DebridProvider, ProviderOptions) (Provider, error) { return tb, nil },
	})
	_, err := svcSolo.Acquire(context.Background(), rel)
	if !errors.Is(err, core.ErrNotCached) || errors.Is(err, core.ErrWrongContent) {
		t.Fatalf("zipped solo Acquire err = %v, want ErrNotCached (not ErrWrongContent)", err)
	}
	if !strings.Contains(err.Error(), "No-Intro.zip") {
		t.Fatalf("zipped solo Acquire err = %q, want file summary", err.Error())
	}
	if deleted != 1 {
		t.Fatalf("cleanup delete count = %d, want 1", deleted)
	}

	// With a second provider configured, failover succeeds instead of aborting.
	media := newMediaServer(t, 4096)
	pm := newFakeProvider("premiumize", media, []File{{ID: "42", Path: "Super Mario World (USA).zip", Size: 4096}})
	pm.cached[testHash] = true
	svcPair := NewService(config.Debrid{
		CachedOnly:     true,
		RemoveUncached: true,
		Providers: []config.DebridProvider{
			{ID: "tb", Kind: "torbox", Name: "TorBox", APIKey: testKey, Enabled: true, Priority: 1},
			{ID: "pm", Kind: "premiumize", Name: "Premiumize", APIKey: testKey, Enabled: true, Priority: 2},
		},
	}, Options{
		NewProvider: func(c config.DebridProvider, _ ProviderOptions) (Provider, error) {
			if c.ID == "tb" {
				return tb, nil
			}
			return pm, nil
		},
	})
	payload, err := svcPair.Acquire(context.Background(), rel)
	if err != nil || !IsPayload(payload) {
		t.Fatalf("failover after TorBox zipped archive failed: %v", err)
	}
}

// ChooseFile explicit-selector mismatches include a bounded torrentFileSummary
// so a zipped or mismatched torrent is diagnosable from the log (#667).
func TestChooseFileExplicitMismatchIncludesSummary(t *testing.T) {
	files := []File{{ID: "0", Path: "Pack/OnlyArchive.zip", Size: 4 << 30}}
	_, err := ChooseFile(files, &core.ReleaseTarget{ExactFile: "Missing.rom", AllowNonVideo: true})
	if !errors.Is(err, core.ErrWrongContent) {
		t.Fatalf("err = %v, want ErrWrongContent", err)
	}
	if !strings.Contains(err.Error(), "OnlyArchive.zip") || !strings.Contains(err.Error(), "Missing.rom") {
		t.Fatalf("err = %q, want target and file summary", err.Error())
	}

	idx5 := 5
	_, err = ChooseFile(files, &core.ReleaseTarget{FileIndex: &idx5, AllowNonVideo: true})
	if !errors.Is(err, core.ErrWrongContent) || !strings.Contains(err.Error(), "OnlyArchive.zip") {
		t.Fatalf("fileIndex err = %v, want ErrWrongContent with file summary", err)
	}
}

// Provider torrent-size-limit errors classify as ErrTooLarge (wrapping
// ErrNotCached) consistently across Real-Debrid, AllDebrid and Debrid-Link.
func TestOtherProvidersClassifySizeLimitErrors(t *testing.T) {
	rd := newRealDebrid("k", ProviderOptions{})
	if err := rd.classify(http.StatusBadRequest, rdError{Error: "torrent_too_big", ErrorCode: 29}); !errors.Is(err, ErrTooLarge) || !errors.Is(err, core.ErrNotCached) {
		t.Fatalf("real-debrid code 29 = %v, want ErrTooLarge", err)
	}
	for _, code := range []string{"MAGNET_TOO_LARGE", "MAGNET_MAGNET_TOO_BIG"} {
		if err := adError(http.StatusOK, code, "max 1TB"); !errors.Is(err, ErrTooLarge) || !errors.Is(err, core.ErrNotCached) {
			t.Fatalf("alldebrid %s = %v, want ErrTooLarge", code, err)
		}
	}
	if err := dlClassify(http.StatusOK, dlEnvelope{Error: "torrentTooBig", Desc: "too big"}); !errors.Is(err, ErrTooLarge) || !errors.Is(err, core.ErrNotCached) {
		t.Fatalf("debrid-link torrentTooBig = %v, want ErrTooLarge", err)
	}
}
