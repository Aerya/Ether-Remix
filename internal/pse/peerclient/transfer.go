package peerclient

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

type Transfer struct {
	State    string `json:"state"`
	Received int64  `json:"received"`
	Relative string `json:"-"`
}

func batchID(h Hit) string {
	v := h.ID
	return v[:8] + "-" + v[8:12] + "-" + v[12:16] + "-" + v[16:20] + "-" + v[20:32]
}
func (c *Client) Enqueue(ctx context.Context, h Hit) error {
	if c.Settings.Kind == "amule" {
		if !hashRE.MatchString(h.ID) {
			return ErrProtocol
		}
		link := fmt.Sprintf("ed2k://|file|%s|%d|%s|/", url.PathEscape(h.Name), h.Size, h.ID)
		var out struct {
			Results []struct {
				OK bool `json:"ok"`
			}
		}
		if err := c.call(ctx, "POST", "/downloads", map[string]any{"links": []string{link}}, &out); err != nil {
			return err
		}
		if len(out.Results) != 1 || !out.Results[0].OK {
			return errors.New("aMule a refusé le téléchargement")
		}
		return nil
	}
	if !hashRE.MatchString(h.ID) || h.ID != hitID(h) {
		return ErrProtocol
	}
	var out struct {
		Failures []any
		Batch    struct {
			ID        string
			Transfers []struct {
				Filename string
				Size     int64
			}
		}
	}
	err := c.call(ctx, "POST", "/transfers/downloads/batches", map[string]any{"id": batchID(h), "username": h.User, "files": []map[string]any{{"filename": h.Filename, "size": h.Size}}, "options": map[string]string{"destination": "remix/" + h.ID}}, &out)
	if err != nil {
		return err
	}
	if len(out.Failures) > 0 || len(out.Batch.Transfers) != 1 || out.Batch.Transfers[0].Filename != h.Filename || out.Batch.Transfers[0].Size != h.Size {
		return errors.New("slskd a refusé le téléchargement")
	}
	return nil
}
func (c *Client) Transfer(ctx context.Context, h Hit) (Transfer, error) {
	if c.Settings.Kind == "amule" {
		if !hashRE.MatchString(h.ID) {
			return Transfer{}, ErrProtocol
		}
		var f struct {
			Hash, Name, Directory, Status string
			Size                          int64 `json:"size_bytes"`
			Completed                     int64 `json:"completed_bytes"`
			Incomplete                    bool
		}
		err := c.call(ctx, "GET", "/downloads/"+h.ID, nil, &f)
		if errors.Is(err, ErrMissing) {
			err = c.call(ctx, "GET", "/shared/"+h.ID, nil, &f)
			if err == nil && !f.Incomplete {
				f.Status = "completed"
				f.Completed = f.Size
			}
		}
		if err != nil {
			return Transfer{}, err
		}
		if !strings.EqualFold(f.Hash, h.ID) || f.Size != h.Size {
			return Transfer{}, ErrProtocol
		}
		t := Transfer{State: f.Status, Received: f.Completed}
		if t.State == "erroneous" {
			t.State = "failed"
		}
		if f.Status == "completed" {
			if f.Name == "" || strings.ContainsAny(f.Name, "/\\") || !filepath.IsAbs(f.Directory) {
				return Transfer{}, ErrProtocol
			}
			rel, err := filepath.Rel(c.Settings.RemoteRoot, filepath.Join(f.Directory, f.Name))
			if err != nil || !filepath.IsLocal(rel) {
				return Transfer{}, errors.New("fichier aMule hors du dossier configuré")
			}
			t.Relative = rel
		}
		return t, nil
	}
	if !hashRE.MatchString(h.ID) {
		return Transfer{}, ErrProtocol
	}
	var b struct {
		Username  string
		Options   struct{ Destination string }
		Transfers []struct {
			Filename, State        string
			Size, BytesTransferred int64
		}
	}
	if err := c.call(ctx, "GET", "/transfers/downloads/batches/"+batchID(h), nil, &b); err != nil {
		return Transfer{}, err
	}
	if b.Username != h.User || b.Options.Destination != "remix/"+h.ID || len(b.Transfers) != 1 {
		return Transfer{}, ErrProtocol
	}
	f := b.Transfers[0]
	if f.Filename != h.Filename || f.Size != h.Size {
		return Transfer{}, ErrProtocol
	}
	t := Transfer{State: "downloading", Received: f.BytesTransferred}
	if strings.Contains(f.State, "Succeeded") {
		t.State = "completed"
		t.Relative = filepath.Join("remix", h.ID, h.Name)
	} else if strings.Contains(f.State, "Completed") || strings.Contains(f.State, "Errored") || strings.Contains(f.State, "Cancelled") || strings.Contains(f.State, "TimedOut") || strings.Contains(f.State, "Rejected") || strings.Contains(f.State, "Aborted") {
		t.State = "failed"
	}
	return t, nil
}
