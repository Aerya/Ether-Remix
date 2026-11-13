package torrentclient

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// ClientStatus est l'état d'un client pour la console.
type ClientStatus struct {
	Name     string `json:"name"`
	Version  string `json:"version,omitempty"`
	Error    string `json:"error,omitempty"`
	Free     *int64 `json:"free,omitempty"` // octets libres, si Remix voit le dossier
	FreeNote string `json:"freeNote,omitempty"`
	LowSpace bool   `json:"lowSpace,omitempty"`
}

// DownloadStatus est un torrent de la catégorie Remix, pour la console.
type DownloadStatus struct {
	Client   string  `json:"client"`
	Name     string  `json:"name"`
	Progress float64 `json:"progress"`
	Size     int64   `json:"size"`
	ETA      int64   `json:"eta,omitempty"` // secondes
	Done     bool    `json:"done"`
	Active   bool    `json:"active"`
	Ratio    float64 `json:"ratio"`
}

// Status résume les clients et les torrents de Remix.
type Status struct {
	Clients   []ClientStatus   `json:"clients"`
	Downloads []DownloadStatus `json:"downloads"`
	// Seeding : torrents complets, qui restent en partage ou arrêtés.
	Seeding    int   `json:"seeding"`
	MinFree    int64 `json:"minFree"`
	AddedToday int   `json:"addedToday"`
	MaxPerDay  int   `json:"maxPerDay"`
}

// maxListed borne la liste affichée : les téléchargements en cours d'abord.
const maxListed = 30

// Status interroge les clients (5 s au plus chacun, en parallèle).
func (p *Provider) Status(ctx context.Context) Status {
	out := Status{Clients: make([]ClientStatus, len(p.Slots)), MinFree: p.Settings.MinFree, MaxPerDay: p.Settings.MaxPerDay}
	p.mu.Lock()
	if p.day == p.now().Format("2006-01-02") {
		out.AddedToday = p.added
	}
	p.mu.Unlock()
	lists := make([][]Torrent, len(p.Slots))
	var wg sync.WaitGroup
	for i, s := range p.Slots {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			cs := ClientStatus{Name: s.Conf.Label()}
			if v, err := s.Client.Version(ctx); err != nil {
				cs.Error = statusError(err)
			} else {
				cs.Version = v
				lists[i], _ = s.Client.List(ctx, p.Settings.Category)
			}
			if free, known := p.FreeSpace(ctx, s); known {
				cs.Free = &free
				cs.LowSpace = p.Settings.MinFree > 0 && free < p.Settings.MinFree
			} else {
				s.mu.Lock()
				cs.FreeNote = s.freeErr
				s.mu.Unlock()
			}
			out.Clients[i] = cs
		}()
	}
	wg.Wait()
	for i, ts := range lists {
		for _, t := range ts {
			if t.Done {
				out.Seeding++
			}
			d := DownloadStatus{Client: p.Slots[i].Conf.Label(), Name: t.Name, Progress: t.Progress, Size: t.Size,
				ETA: int64(t.ETA / time.Second), Done: t.Done, Active: t.Active, Ratio: t.Ratio}
			out.Downloads = append(out.Downloads, d)
		}
	}
	sort.SliceStable(out.Downloads, func(a, b int) bool {
		x, y := out.Downloads[a], out.Downloads[b]
		if x.Done != y.Done {
			return !x.Done // en cours d'abord
		}
		return x.Progress > y.Progress
	})
	if len(out.Downloads) > maxListed {
		out.Downloads = out.Downloads[:maxListed]
	}
	return out
}

func statusError(err error) string {
	switch {
	case errors.Is(err, ErrAuth):
		return "identifiants ou clé d'API refusés"
	case errors.Is(err, context.DeadlineExceeded):
		return "pas de réponse"
	}
	return "injoignable"
}
