package xdcc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Erreurs du client. Aucune ne contient la clé ni l'adresse complète.
var (
	ErrAuth        = errors.New("xdcc: clé d'API de Grabscher refusée (désactivée ou fausse)")
	ErrUnreachable = errors.New("xdcc: Grabscher injoignable")
	ErrNotFound    = errors.New("xdcc: objet inconnu de Grabscher")
	ErrProtocol    = errors.New("xdcc: réponse inattendue de Grabscher")
)

// Client parle à XDCC Grabscher : REST /api/1.0 (clé dans l'en-tête
// Authorization) et compatibilité SABnzbd (clé dans le corps POST). La clé
// n'apparaît jamais dans une URL, un journal ou une erreur.
type Client struct {
	Base string
	Key  string
	HTTP *http.Client
}

// Packet est un paquet XDCC, tel que le REST le décrit.
type Packet struct {
	GUID        string    `json:"Guid"`
	BotGUID     string    `json:"ParentGuid"`
	ID          int       `json:"Id"`
	Name        string    `json:"Name"`
	Size        int64     `json:"Size"`
	LastUpdated time.Time `json:"LastUpdated"`
	Enabled     bool      `json:"Enabled"`   // demandé
	Connected   bool      `json:"Connected"` // en cours de transfert
	CurrentSize int64     `json:"CurrentSize"`
	Speed       int64     `json:"Speed"`
	TimeMissing int64     `json:"TimeMissing"`
}

// Bot est un bot XDCC.
type Bot struct {
	GUID          string    `json:"Guid"`
	ChannelGUID   string    `json:"ParentGuid"`
	Name          string    `json:"Name"`
	Connected     bool      `json:"Connected"` // présent dans le canal
	State         int       `json:"State"`     // 0 inactif, 1 actif, 2 en attente
	LastMessage   string    `json:"LastMessage"`
	LastContact   time.Time `json:"LastContact"`
	QueuePosition int       `json:"QueuePosition"`
	QueueTime     int       `json:"QueueTime"`
	SlotTotal     int       `json:"InfoSlotTotal"`
	SlotCurrent   int       `json:"InfoSlotCurrent"`
	QueueTotal    int       `json:"InfoQueueTotal"`
	QueueCurrent  int       `json:"InfoQueueCurrent"`
	Problems      bool      `json:"HasNetworkProblems"`
}

// États d'un bot.
const (
	BotIdle    = 0
	BotActive  = 1
	BotWaiting = 2
)

// Node est un serveur ou un canal IRC.
type Node struct {
	GUID       string `json:"Guid"`
	ParentGUID string `json:"ParentGuid"`
	Name       string `json:"Name"`
	Connected  bool   `json:"Connected"`
	Enabled    bool   `json:"Enabled"`
	BotCount   int    `json:"BotCount"`
	ErrorCode  int    `json:"ErrorCode"`
}

type objects[T any] struct {
	Results     []T `json:"Results"`
	ResultCount int `json:"ResultCount"`
}

type defaultResult struct {
	ReturnValue *int   `json:"ReturnValue"`
	Message     string `json:"Message"`
}

func (c *Client) hc() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

// rest appelle GET /api/1.0/<path>.json.
func (c *Client) rest(ctx context.Context, method, p string, q url.Values, out any) error {
	u := c.Base + "/api/1.0/" + p + ".json"
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return fmt.Errorf("%w: adresse invalide", ErrProtocol)
	}
	req.Header.Set("Authorization", c.Key)
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %s", ErrUnreachable, netReason(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return fmt.Errorf("%w: réponse interrompue", ErrUnreachable)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return ErrAuth
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("%w: HTTP %d", ErrProtocol, resp.StatusCode)
	}
	var d defaultResult
	if json.Unmarshal(body, &d) == nil && d.ReturnValue != nil && *d.ReturnValue == 0 {
		msg := strings.TrimSpace(d.Message)
		if strings.Contains(strings.ToLower(msg), "not found") {
			return ErrNotFound
		}
		return fmt.Errorf("%w: %s", ErrProtocol, clip(msg, 160))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%w: JSON illisible", ErrProtocol)
	}
	return nil
}

// Search cherche des paquets par texte, bots en ligne seulement.
func (c *Client) Search(ctx context.Context, term string, max int) ([]Packet, error) {
	q := url.Values{"searchTerm": {term}, "showOfflineBots": {"false"}, "maxResults": {strconv.Itoa(max)}, "page": {"1"}, "sortBy": {"Name"}, "sort": {"asc"}}
	var res objects[Packet]
	if err := c.rest(ctx, http.MethodGet, "packets", q, &res); err != nil {
		return nil, err
	}
	return res.Results, nil
}

// Packet lit un paquet.
func (c *Client) Packet(ctx context.Context, guid string) (Packet, error) {
	var p Packet
	err := c.rest(ctx, http.MethodGet, "packets/"+url.PathEscape(guid), nil, &p)
	if err == nil && p.GUID == "" {
		return p, ErrNotFound
	}
	return p, err
}

// Bot lit un bot.
func (c *Client) Bot(ctx context.Context, guid string) (Bot, error) {
	var b Bot
	err := c.rest(ctx, http.MethodGet, "bots/"+url.PathEscape(guid), nil, &b)
	if err == nil && b.GUID == "" {
		return b, ErrNotFound
	}
	return b, err
}

// Channel lit un canal.
func (c *Client) Channel(ctx context.Context, guid string) (Node, error) {
	var n Node
	err := c.rest(ctx, http.MethodGet, "channels/"+url.PathEscape(guid), nil, &n)
	return n, err
}

// Server lit un serveur.
func (c *Client) Server(ctx context.Context, guid string) (Node, error) {
	var n Node
	err := c.rest(ctx, http.MethodGet, "servers/"+url.PathEscape(guid), nil, &n)
	return n, err
}

// Servers liste les serveurs IRC.
func (c *Client) Servers(ctx context.Context) ([]Node, error) {
	var res objects[Node]
	err := c.rest(ctx, http.MethodGet, "servers", nil, &res)
	return res.Results, err
}

// Channels liste les canaux IRC.
func (c *Client) Channels(ctx context.Context) ([]Node, error) {
	var res objects[Node]
	err := c.rest(ctx, http.MethodGet, "channels", nil, &res)
	return res.Results, err
}

// --- compatibilité SABnzbd ---

// Job est une tâche de téléchargement de Grabscher (file ou historique).
type Job struct {
	ID       string
	Name     string
	Category string
	Status   string // Queued, Downloading, Moving, Completed, Failed
	Size     int64
	Left     int64
	Fail     string
	Storage  string // chemin final vu par Grabscher
	Done     time.Time
}

// Active indique une tâche encore en file ou en cours.
func (j Job) Active() bool {
	return j.Status == "Queued" || j.Status == "Downloading" || j.Status == "Moving"
}

func (c *Client) sab(ctx context.Context, form url.Values, out any) error {
	form.Set("apikey", c.Key)
	form.Set("output", "json")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+"/sabnzbd/api", strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("%w: adresse invalide", ErrProtocol)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.hc().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %s", ErrUnreachable, netReason(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return fmt.Errorf("%w: réponse interrompue", ErrUnreachable)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: HTTP %d", ErrProtocol, resp.StatusCode)
	}
	var e struct {
		Status *bool  `json:"status"`
		Error  string `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Status != nil && !*e.Status {
		if strings.Contains(strings.ToLower(e.Error), "api key") {
			return ErrAuth
		}
		return fmt.Errorf("%w: %s", ErrProtocol, clip(e.Error, 200))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%w: JSON illisible", ErrProtocol)
	}
	return nil
}

// Version renvoie la version de compatibilité annoncée (test de connexion
// de l'API SABnzbd, clé comprise).
func (c *Client) Version(ctx context.Context) (string, error) {
	var out struct {
		Queue struct {
			Status string `json:"status"`
		} `json:"queue"`
	}
	if err := c.sab(ctx, url.Values{"mode": {"queue"}, "limit": {"1"}}, &out); err != nil {
		return "", err
	}
	var v struct {
		Version string `json:"version"`
	}
	if err := c.sab(ctx, url.Values{"mode": {"version"}}, &v); err != nil {
		return "", err
	}
	return v.Version, nil
}

// CompleteDir renvoie le dossier de téléchargement de Grabscher (vu par lui).
func (c *Client) CompleteDir(ctx context.Context) (string, error) {
	var out struct {
		Config struct {
			Misc struct {
				CompleteDir string `json:"complete_dir"`
			} `json:"misc"`
		} `json:"config"`
	}
	if err := c.sab(ctx, url.Values{"mode": {"get_config"}}, &out); err != nil {
		return "", err
	}
	return out.Config.Misc.CompleteDir, nil
}

// Grab crée la tâche d'un paquet. Grabscher ne télécharge rien d'extérieur :
// il lit seulement le GUID dans son propre format de lien Newznab, et rend
// la tâche existante si le paquet est déjà demandé.
func (c *Client) Grab(ctx context.Context, packetGUID, category string) (string, error) {
	link := "http://grabscher.invalid/newznab/api?t=get&id=" + url.QueryEscape(packetGUID)
	var out struct {
		IDs []string `json:"nzo_ids"`
	}
	if err := c.sab(ctx, url.Values{"mode": {"addurl"}, "name": {link}, "cat": {category}}, &out); err != nil {
		return "", err
	}
	if len(out.IDs) == 0 || out.IDs[0] == "" {
		return "", fmt.Errorf("%w: aucune tâche créée", ErrProtocol)
	}
	return out.IDs[0], nil
}

// Jobs renvoie la file et l'historique, par identifiant.
func (c *Client) Jobs(ctx context.Context) (map[string]Job, error) {
	var q struct {
		Queue struct {
			Slots []struct {
				ID     string `json:"nzo_id"`
				Name   string `json:"filename"`
				Cat    string `json:"cat"`
				Status string `json:"status"`
				MB     string `json:"mb"`
				MBLeft string `json:"mbleft"`
			} `json:"slots"`
		} `json:"queue"`
	}
	if err := c.sab(ctx, url.Values{"mode": {"queue"}, "limit": {"1000"}}, &q); err != nil {
		return nil, err
	}
	var h struct {
		History struct {
			Slots []struct {
				ID        string `json:"nzo_id"`
				Name      string `json:"name"`
				Cat       string `json:"category"`
				Bytes     int64  `json:"bytes"`
				Status    string `json:"status"`
				Fail      string `json:"fail_message"`
				Storage   string `json:"storage"`
				Completed int64  `json:"completed"`
			} `json:"slots"`
		} `json:"history"`
	}
	if err := c.sab(ctx, url.Values{"mode": {"history"}, "limit": {"1000"}}, &h); err != nil {
		return nil, err
	}
	out := map[string]Job{}
	for _, s := range h.History.Slots {
		j := Job{ID: s.ID, Name: s.Name, Category: s.Cat, Status: s.Status, Size: s.Bytes, Fail: s.Fail, Storage: s.Storage}
		if s.Completed > 0 {
			j.Done = time.Unix(s.Completed, 0)
		}
		out[s.ID] = j
	}
	for _, s := range q.Queue.Slots {
		out[s.ID] = Job{ID: s.ID, Name: s.Name, Category: s.Cat, Status: s.Status, Size: mb(s.MB), Left: mb(s.MBLeft)}
	}
	return out, nil
}

// Cancel arrête une tâche en cours : Grabscher efface le fichier partiel.
func (c *Client) Cancel(ctx context.Context, id string) error {
	return c.sab(ctx, url.Values{"mode": {"queue"}, "name": {"delete"}, "value": {id}}, nil)
}

// Forget retire une tâche terminée de l'historique ; avec files, Grabscher
// efface aussi son fichier, seulement s'il est encore dans son dossier.
func (c *Client) Forget(ctx context.Context, id string, files bool) error {
	f := url.Values{"mode": {"history"}, "name": {"delete"}, "value": {id}}
	if files {
		f.Set("del_files", "1")
	}
	return c.sab(ctx, f, nil)
}

func mb(v string) int64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || f < 0 {
		return 0
	}
	return int64(f * (1 << 20))
}

func netReason(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "connection refused"):
		return "connexion refusée"
	case strings.Contains(s, "no such host"):
		return "nom d'hôte inconnu"
	case strings.Contains(s, "timeout"), strings.Contains(s, "deadline"):
		return "délai dépassé"
	}
	return "erreur réseau"
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
