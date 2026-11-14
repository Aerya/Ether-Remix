package xdcc

import (
	"context"
	"fmt"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// Catégories Newznab des résultats (films, séries).
const (
	movieCategory = 2000
	tvCategory    = 5000
)

// Queries renvoie les recherches texte d'une demande : titre et titre
// d'origine, avec l'année d'un film ou le numéro SxxEyy d'un épisode.
func Queries(req core.SearchRequest) []string {
	titles := []string{req.Title}
	if o := strings.TrimSpace(req.OriginalTitle); o != "" && !strings.EqualFold(releaseparse.NormalizeTitle(o), releaseparse.NormalizeTitle(req.Title)) {
		titles = append(titles, o)
	}
	var out []string
	seen := map[string]bool{}
	for _, t := range titles {
		var q string
		switch req.Type {
		case core.TypeMovie:
			q = searchText(t)
			if req.Year > 0 {
				q += fmt.Sprintf(" %d", req.Year)
			}
		case core.TypeEpisode, core.TypeShow:
			q = searchText(t)
			switch {
			case req.Season > 0 && req.Episode > 0:
				q += fmt.Sprintf(" S%02dE%02d", req.Season, req.Episode)
			case req.Season > 0:
				q += fmt.Sprintf(" S%02d", req.Season)
			}
		default:
			continue
		}
		q = strings.TrimSpace(q)
		if k := strings.ToLower(q); q != "" && !seen[k] {
			seen[k] = true
			out = append(out, q)
		}
	}
	return out
}

// Matches vérifie qu'un nom de paquet correspond bien à la demande : titre
// (ou titre d'origine, ou titre alternatif), année d'un film, saison et
// épisode d'un épisode. Un paquet sans année ou sans numéro est écarté :
// l'identification doit être fiable, Grabscher ne connaît pas les
// identifiants TMDB/IMDb.
func Matches(name string, req core.SearchRequest) (core.ReleaseInfo, bool) {
	if !videoExt(name) {
		return core.ReleaseInfo{}, false
	}
	info := releaseparse.Parse(name)
	alts := append([]string(nil), req.AlternativeTitles...)
	if req.OriginalTitle != "" {
		alts = append(alts, req.OriginalTitle)
	}
	if !releaseparse.TitleMatchesAny(info, req.Title, alts) {
		return info, false
	}
	switch req.Type {
	case core.TypeMovie:
		if info.Year == 0 || len(info.Seasons) > 0 || len(info.Episodes) > 0 || !releaseparse.YearMatches(info.Year, req.Year) {
			return info, false
		}
	case core.TypeEpisode, core.TypeShow:
		if info.FullSeason || len(info.Seasons) == 0 || len(info.Episodes) == 0 {
			return info, false
		}
		if req.Season > 0 && req.Episode > 0 && !releaseparse.EpisodeMatches(info, req.Season, req.Episode) {
			return info, false
		}
		if req.Season > 0 && req.Episode == 0 && !containsInt(info.Seasons, req.Season) {
			return info, false
		}
	default:
		return info, false
	}
	return info, true
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// Search est la recherche de la source dans le gestionnaire d'indexeurs
// d'Ether : budgets, cache, priorités, filtres et classement restent ceux
// d'Ether. Seuls les paquets de bots en ligne, non exclus et sans échec
// récent sont rendus. Une recherche ne télécharge jamais rien.
func (s *Service) Search(ctx context.Context, req core.SearchRequest, reserve func(context.Context, string) error) ([]core.Release, error) {
	if req.Type != core.TypeMovie && req.Type != core.TypeEpisode && req.Type != core.TypeShow {
		return nil, nil
	}
	var out []core.Release
	seen := map[string]bool{}
	for _, q := range Queries(req) {
		if reserve != nil {
			if err := reserve(ctx, "hit"); err != nil {
				if len(out) > 0 {
					break
				}
				return nil, err
			}
		}
		packets, err := s.Client.Search(ctx, q, s.Settings.MaxResults)
		if err != nil {
			s.mu.Lock()
			s.noteErr(err)
			s.mu.Unlock()
			if len(out) > 0 {
				break
			}
			return nil, fmt.Errorf("%w: %w", core.ErrSourceUnavailable, err)
		}
		for _, p := range packets {
			if seen[p.GUID] || p.Size <= 0 {
				continue
			}
			seen[p.GUID] = true
			info, ok := Matches(p.Name, req)
			if !ok {
				continue
			}
			s.mu.Lock()
			s.init()
			_, pf := s.failed(p.GUID)
			s.mu.Unlock()
			if pf {
				continue
			}
			b, err := s.bot(ctx, p.BotGUID, false)
			if err != nil || !b.Connected || s.Settings.Excluded(b.Name) {
				continue
			}
			s.mu.Lock()
			_, bf := s.failed("bot:" + strings.ToLower(b.Name))
			s.mu.Unlock()
			if bf {
				continue
			}
			out = append(out, s.release(p, info, req))
		}
	}
	return out, nil
}

// release construit le candidat d'Ether. Le lien enregistré n'est qu'un
// repère : la préparation donne le vrai lien local à la lecture.
func (s *Service) release(p Packet, info core.ReleaseInfo, req core.SearchRequest) core.Release {
	cat := movieCategory
	if req.Type != core.TypeMovie {
		cat = tvCategory
	}
	return core.Release{
		GUID:         GUIDPrefix + p.GUID,
		Name:         p.Name,
		Size:         p.Size,
		PubDate:      p.LastUpdated,
		Category:     cat,
		Info:         info,
		Protocol:     core.ProtocolHTTP,
		StreamURL:    PlaceholderURL + p.GUID,
		StreamOrigin: "http://127.0.0.1",
	}
}

// ManualResult est un résultat de recherche de la console.
type ManualResult struct {
	Packet  string `json:"packet"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Bot     string `json:"bot"`
	Online  bool   `json:"online"`
	Queue   int    `json:"queue"`
	Slots   string `json:"slots,omitempty"`
	State   string `json:"state"` // available, offline, failed, queued, downloading, ready
	Reason  string `json:"reason,omitempty"`
	Height  int    `json:"height,omitempty"`
	Codec   string `json:"codec,omitempty"`
	Langs   string `json:"langs,omitempty"`
	Pack    int    `json:"pack"`
	Stream  string `json:"stream,omitempty"` // ok, no : lisible en progressif
	Updated string `json:"updated,omitempty"`
}

// ManualSearch cherche un texte libre pour la sélection manuelle (console) :
// bots hors ligne compris, signalés comme tels.
func (s *Service) ManualSearch(ctx context.Context, text string) ([]ManualResult, error) {
	q := searchText(text)
	if q == "" {
		return nil, nil
	}
	packets, err := s.Client.Search(ctx, q, s.Settings.MaxResults)
	if err != nil {
		return nil, err
	}
	out := make([]ManualResult, 0, len(packets))
	for _, p := range packets {
		info := releaseparse.Parse(p.Name)
		m := ManualResult{Packet: p.GUID, Name: p.Name, Size: p.Size, Pack: p.ID, Height: info.Height, Codec: info.VideoCodec,
			Langs: strings.Join(info.Languages, ", ")}
		if !p.LastUpdated.IsZero() {
			m.Updated = p.LastUpdated.Format("2006-01-02")
		}
		b, err := s.bot(ctx, p.BotGUID, false)
		if err == nil {
			m.Bot, m.Online, m.Queue = b.Name, b.Connected, b.QueuePosition
			if b.SlotTotal > 0 {
				m.Slots = fmt.Sprintf("%d/%d", b.SlotCurrent, b.SlotTotal)
			}
		}
		m.State = "available"
		if !m.Online {
			m.State = "offline"
		}
		s.mu.Lock()
		s.init()
		if f, ok := s.failed(p.GUID); ok {
			m.State, m.Reason = "failed", clip(f.reason, 160)
		}
		if t := s.tasks[p.GUID]; t != nil {
			switch t.State {
			case StateQueued:
				m.State = "queued"
			case StateDownloading:
				m.State = "downloading"
			case StateCompleted:
				m.State = "ready"
			}
		}
		s.mu.Unlock()
		if !videoExt(p.Name) {
			m.Stream = "no"
		}
		out = append(out, m)
	}
	return out, nil
}
