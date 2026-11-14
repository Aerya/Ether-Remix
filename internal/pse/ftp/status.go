package ftp

import (
	"context"
	"errors"
	"os"
	"sort"
	"strings"
	"time"
)

// TaskView est une copie telle que la console l'affiche.
type TaskView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	State   string `json:"state"`
	Current int64  `json:"current"`
	Speed   int64  `json:"speed"`
	ETA     int64  `json:"eta,omitempty"` // secondes
	Reason  string `json:"reason,omitempty"`
	Keep    bool   `json:"keep"`
	Manual  bool   `json:"manual,omitempty"`
	Reading bool   `json:"reading,omitempty"`
	Created string `json:"created"`
}

// Status est l'état de la source pour la console. Aucun secret n'y figure.
type Status struct {
	Protocol    string     `json:"protocol"`
	Server      string     `json:"server"` // hôte:port, sans identifiants
	Mode        string     `json:"mode"`
	Writes      bool       `json:"writes"`
	Files       int        `json:"files"`
	Movies      int        `json:"movies"`
	Episodes    int        `json:"episodes"`
	Scanning    bool       `json:"scanning"`
	LastScan    string     `json:"lastScan,omitempty"`
	NextScan    string     `json:"nextScan,omitempty"`
	ScanDirs    int        `json:"scanDirs,omitempty"`
	ScanFailed  []string   `json:"scanFailed,omitempty"`
	ScanCapped  bool       `json:"scanCapped,omitempty"`
	ScanError   string     `json:"scanError,omitempty"`
	Error       string     `json:"error,omitempty"`
	ErrorAt     string     `json:"errorAt,omitempty"`
	Reading     int        `json:"reading"`
	DownloadDir string     `json:"downloadDir,omitempty"`
	DownloadOK  bool       `json:"downloadVisible"`
	Active      int        `json:"active"`
	MaxActive   int        `json:"maxActive"`
	CacheUsed   int64      `json:"cacheUsed"`
	CacheMax    int64      `json:"cacheMax"`
	Tasks       []TaskView `json:"tasks"`
}

const statusTasks = 60

// Status lit l'état courant (sans contacter le serveur).
func (s *Service) Status(_ context.Context) Status {
	out := Status{Protocol: s.Settings.Protocol, Server: s.Settings.Addr(), Mode: s.Settings.Mode, Writes: s.Settings.Writes(),
		MaxActive: s.Settings.MaxActive, CacheMax: s.Settings.CacheBytes, DownloadDir: s.Settings.DownloadDir}
	if d := s.Settings.DownloadDir; d != "" {
		st, err := os.Stat(d)
		out.DownloadOK = err == nil && st.IsDir()
	}
	out.CacheUsed = s.cacheUsed()
	s.mu.Lock()
	defer s.mu.Unlock()
	out.Files = len(s.files)
	for _, f := range s.files {
		if len(f.info.Episodes) > 0 {
			out.Episodes++
		} else {
			out.Movies++
		}
	}
	out.Scanning = s.scanning
	if !s.lastScan.IsZero() {
		out.LastScan = s.lastScan.UTC().Format(time.RFC3339)
		out.NextScan = s.lastScan.Add(s.Settings.ScanEvery).UTC().Format(time.RFC3339)
		out.ScanDirs, out.ScanCapped = s.lastScanRes.Dirs, s.lastScanRes.Capped
		if n := len(s.lastScanRes.Failed); n > 0 {
			out.ScanFailed = append([]string(nil), s.lastScanRes.Failed[:min(n, 10)]...)
		}
	}
	out.ScanError = s.scanErr
	if s.lastErr != "" && s.now().Sub(s.lastErrT) < 30*time.Minute {
		out.Error, out.ErrorAt = s.lastErr, s.lastErrT.UTC().Format(time.RFC3339)
	}
	for _, n := range s.readers {
		out.Reading += n
	}
	var views []TaskView
	for _, t := range s.tasks {
		v := TaskView{ID: t.ID, Name: t.Name, Size: t.Size, State: t.State, Reason: t.Fail, Keep: t.Keep, Manual: t.Manual,
			Reading: s.readers[t.ID] > 0, Created: t.Created.UTC().Format(time.RFC3339)}
		if t.State == StateDone {
			v.Current = t.Size
		}
		if l := s.lives[t.ID]; l != nil {
			v.Current, v.Speed = l.done, l.speed
			if l.speed > 0 && t.Size > l.done {
				v.ETA = (t.Size - l.done) / l.speed
			}
		}
		if t.State == StateDownloading {
			out.Active++
		}
		views = append(views, v)
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Created > views[j].Created })
	if len(views) > statusTasks {
		views = views[:statusTasks]
	}
	out.Tasks = views
	return out
}

// TestResult est le résultat du bouton « Tester la connexion ».
type TestResult struct {
	OK          bool   `json:"ok"`
	Message     string `json:"message"`
	Fingerprint string `json:"fingerprint,omitempty"` // empreinte vue, à vérifier puis enregistrer
	Entries     int    `json:"entries,omitempty"`
}

// Test vérifie la connexion avec les réglages enregistrés : empreinte de
// l'hôte, authentification, lecture des dossiers choisis. Rien n'est
// enregistré ni accepté automatiquement.
func Test(ctx context.Context, s Settings) TestResult {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if s.Host == "" || ValidateHost(s.Host) != nil {
		return TestResult{Message: "Renseignez d'abord l'adresse du serveur (nom d'hôte ou IP, sans « sftp:// »)."}
	}
	var res TestResult
	switch s.Protocol {
	case ProtoSFTP:
		fp, err := ProbeHostKey(ctx, s)
		if err != nil {
			return TestResult{Message: "Serveur SSH injoignable : " + scrub(err, s)}
		}
		res.Fingerprint = fp
		if want := NormalizeFingerprint(s.HostKey); want == "" {
			return TestResult{Fingerprint: fp, Message: "Empreinte de l'hôte : " + fp + ". Vérifiez-la auprès de votre hébergeur (ou avec « ssh-keyscan » depuis une machine de confiance), puis copiez-la dans « Empreinte du serveur » et enregistrez."}
		} else if want != fp {
			return TestResult{Fingerprint: fp, Message: "Attention : l'empreinte du serveur a changé (vue " + fp + ", enregistrée " + want + "). Connexion refusée. Si le changement est attendu (réinstallation du serveur), vérifiez-la puis remplacez-la."}
		}
	case ProtoFTPS, ProtoFTPSImpl:
		if fp, err := ProbeCertificate(ctx, s); err == nil {
			res.Fingerprint = fp
		}
	}
	if s.User == "" {
		res.Message = "Renseignez l'utilisateur."
		return res
	}
	rm, err := NewRemote(s)
	if err != nil {
		res.Message = err.Error()
		return res
	}
	defer rm.Close()
	total := 0
	var bad []string
	for _, root := range s.Roots {
		entries, err := rm.ReadDir(ctx, root)
		if err != nil {
			var hk *HostKeyError
			var ce *CertError
			switch {
			case errors.As(err, &hk), errors.As(err, &ce):
				res.Message = err.Error()
				return res
			case errors.Is(err, ErrNotFound):
				bad = append(bad, root+" (introuvable)")
				continue
			case root == s.Roots[0] && total == 0 && len(bad) == 0 && !errors.Is(err, ErrNotFound):
				res.Message = connMessage(s, err)
				return res
			}
			bad = append(bad, root)
			continue
		}
		total += len(entries)
	}
	res.Entries = total
	if len(bad) == len(s.Roots) {
		res.Message = "Connexion réussie, mais aucun dossier lisible : " + strings.Join(bad, ", ")
		return res
	}
	res.OK = true
	res.Message = "Connexion réussie."
	if len(bad) > 0 {
		res.Message += " Dossiers illisibles : " + strings.Join(bad, ", ") + "."
	}
	if s.Protocol == ProtoFTP {
		res.Message += " Attention : FTP en clair, le mot de passe et les fichiers circulent sans chiffrement."
	}
	return res
}

func connMessage(s Settings, err error) string {
	msg := scrub(err, s)
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "unable to authenticate") || strings.Contains(low, "530") || strings.Contains(low, "login"):
		return "Authentification refusée : vérifiez l'utilisateur, le mot de passe ou la clé SSH."
	case strings.Contains(low, "certificate") || strings.Contains(low, "x509"):
		return "Certificat FTPS non reconnu : " + msg + ". Pour un certificat auto-signé, enregistrez son empreinte dans « Empreinte du serveur »."
	case strings.Contains(low, "connection refused") || strings.Contains(low, "timeout") || strings.Contains(low, "no such host"):
		return "Serveur injoignable : " + msg
	}
	return "Connexion impossible : " + msg
}
