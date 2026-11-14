package xdcc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// GUIDPrefix marque les releases de la source XDCC.
const GUIDPrefix = "xdcc:"

// PlaceholderURL est le lien enregistré avec une release : il ne sert qu'à
// la reconnaître. La préparation le remplace par un lien local à jeton.
const PlaceholderURL = "http://127.0.0.1/xdcc/"

// PacketOf renvoie le GUID Grabscher d'une release XDCC.
func PacketOf(rel core.Release) (string, bool) {
	g, ok := strings.CutPrefix(rel.GUID, GUIDPrefix)
	if !ok || len(g) != 36 {
		return "", false
	}
	return g, true
}

func notice(err error, fr, en string) error {
	return &core.LocalNoticeError{FR: fr, EN: en, Err: err}
}

// errPreparing : la préparation continue après l'attente du lecteur. Pour
// Ether c'est un téléchargement en file (ErrDownloadQueued) et une source à
// la demande encore en préparation (ErrPrepareSlow) : aucune autre source
// XDCC n'est ouverte dans le même essai, la suivante la trouve prête.
var errPreparing = fmt.Errorf("xdcc: %w; %w", core.ErrDownloadQueued, core.ErrPrepareSlow)

// prepareWait borne l'attente dans la sonde d'Ether (20 s) et le budget
// d'une vérification à la demande (22 s), lecture de l'en-tête comprise.
const prepareWait = 14 * time.Second

// Prepare prépare une release XDCC avant la sonde d'Ether. Un nouveau
// téléchargement ne démarre que pour une lecture (jamais pour l'indexation,
// une vérification de disponibilité ou un préchargement).
func (s *Service) Prepare(ctx context.Context, rel core.Release) (core.Release, bool, error) {
	packet, ok := PacketOf(rel)
	if !ok {
		return rel, false, nil
	}
	if s == nil || s.Client == nil {
		return rel, true, fmt.Errorf("xdcc: source désactivée : %w", core.ErrSourceUnavailable)
	}
	allow := core.OnDemandStreamsAllowed(ctx) && !core.PSEAvailabilityCheck(ctx) && !core.IsBackgroundCheck(ctx)
	t, err := s.ensure(ctx, packet, allow, startOptions{keep: s.Settings.KeepOnPlay(), name: rel.Name, size: rel.Size})
	if err != nil {
		return rel, true, err
	}
	wait := prepareWait
	if dl, ok := ctx.Deadline(); ok {
		wait = min(wait, time.Until(dl)-6*time.Second)
	}
	deadline := s.now().Add(max(wait, 0))
	for {
		ready, perr := s.ready(ctx, t.Packet)
		if perr != nil {
			return rel, true, perr
		}
		if ready {
			u, origin, err := s.server.URL(t.Packet)
			if err != nil {
				return rel, true, fmt.Errorf("xdcc: serveur local : %w: %w", core.ErrSourceUnavailable, err)
			}
			rel.StreamURL, rel.StreamOrigin, rel.StreamHeaders = u, origin, nil
			return rel, true, nil
		}
		if !s.now().Before(deadline) {
			return rel, true, s.pendingNotice(t.Packet)
		}
		select {
		case <-ctx.Done():
			return rel, true, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// ready indique si le paquet se lit maintenant ; une erreur met fin à
// l'attente (échec, configuration).
func (s *Service) ready(ctx context.Context, packet string) (bool, error) {
	_ = s.refresh(ctx, false)
	src, err := s.source(ctx, packet)
	if err != nil {
		return false, err
	}
	if src.done {
		return true, nil
	}
	if !s.Settings.Progressive() || src.local == "" {
		return false, nil
	}
	if src.available < min(s.Settings.Buffer, src.size) {
		return false, nil
	}
	f, err := os.Open(src.local)
	if err != nil {
		return false, nil
	}
	defer f.Close()
	switch Sniff(f, src.available) {
	case StreamOK:
		return true, nil
	case StreamNo:
		s.mu.Lock()
		if l := s.lives[packet]; l != nil {
			l.botMsg = "" // la notice explique le format
		}
		s.mu.Unlock()
		return false, notice(errPreparing,
			"Ce format (MP4 non optimisé, AVI…) ne se lit qu'une fois complet : le téléchargement XDCC continue. Relancez la lecture une fois terminé.",
			"This format (non-optimised MP4, AVI…) only plays once complete: the XDCC download continues. Play again once it finishes.")
	}
	return false, nil
}

// source décrit où lire un paquet maintenant.
type source struct {
	local     string // chemin vu par Remix ("" : invisible)
	size      int64
	available int64
	done      bool
	speed     int64
}

// errNotVisible : Remix ne voit pas les dossiers de Grabscher.
func errNotVisible(fr, en string) error {
	return notice(fmt.Errorf("xdcc: %s invisible pour Remix (volume ou correspondance des dossiers) : %w", fr, core.ErrSourceUnavailable),
		"Remix ne voit pas le "+fr+" de Grabscher : vérifiez le volume partagé (Sources → XDCC).",
		"Remix cannot see Grabscher's "+en+": check the shared volume (Sources → XDCC).")
}

func (s *Service) source(ctx context.Context, packet string) (source, error) {
	s.mu.Lock()
	t := s.tasks[packet]
	if t == nil {
		s.mu.Unlock()
		return source{}, fmt.Errorf("xdcc: paquet inconnu : %w", core.ErrSourceUnavailable)
	}
	cp := *t
	l := s.lives[packet]
	s.mu.Unlock()
	switch cp.State {
	case StateFailed:
		return source{}, failedNotice(cp)
	case StateCancelled, StateRemoved:
		return source{}, fmt.Errorf("xdcc: téléchargement arrêté : %w", core.ErrSourceUnavailable)
	case StateCompleted:
		local, ok := s.Settings.Local(cp.Storage)
		if !ok || localSize(local) != cp.Size {
			return source{}, errNotVisible("dossier de téléchargement", "download folder")
		}
		return source{local: local, size: cp.Size, available: cp.Size, done: true}, nil
	}
	src := source{size: cp.Size}
	if l != nil {
		src.speed = l.speed
	}
	// Dossier temporaire invisible : pas de lecture progressive, la lecture
	// attend le fichier complet (comme le mode téléchargement).
	if dir, ok := s.tempDir(ctx); ok && s.Settings.Progressive() {
		if p, size := findPartial(dir, cp.Name, cp.Size); p != "" {
			src.local, src.size, src.available = p, size, min(localSize(p), size)
		}
	}
	return src, nil
}

func failedNotice(t Task) error {
	reason := strings.TrimSpace(t.Fail)
	fr, en := "Le téléchargement XDCC a échoué.", "The XDCC download failed."
	low := strings.ToLower(reason)
	switch {
	case strings.Contains(low, "passive"):
		fr, en = "Le bot XDCC n'accepte que le DCC passif, non configuré sur Grabscher.", "The XDCC bot only offers passive DCC, which Grabscher is not set up for."
	case strings.Contains(low, "did not answer") || strings.Contains(low, "silent"):
		fr, en = "Le bot XDCC ne répond pas.", "The XDCC bot does not answer."
	case strings.Contains(low, "offline"):
		fr, en = "Le bot XDCC est hors ligne.", "The XDCC bot is offline."
	case strings.Contains(low, "denied") || strings.Contains(low, "refus"):
		fr, en = "Le bot XDCC a refusé la demande.", "The XDCC bot refused the request."
	case strings.Contains(low, "could not open") || strings.Contains(low, "connection"):
		fr, en = "Le transfert XDCC n'a pas pu s'ouvrir chez le bot.", "The XDCC transfer could not be opened on the bot."
	}
	return notice(fmt.Errorf("xdcc: %s : %w", clip(reason, 200), core.ErrSourceUnavailable), fr, en)
}

// pendingNotice décrit au spectateur où en est un paquet pas encore lisible.
func (s *Service) pendingNotice(packet string) error {
	s.mu.Lock()
	t := s.tasks[packet]
	var cp Task
	if t != nil {
		cp = *t
	}
	l := s.lives[packet]
	var lv live
	if l != nil {
		lv = *l
	}
	s.mu.Unlock()
	bot := cp.Bot
	if bot == "" {
		bot = "?"
	}
	switch {
	case cp.State == StateQueued && l != nil && !lv.botOnline:
		return notice(errPreparing,
			"Le bot XDCC "+bot+" est hors ligne pour l'instant : la demande reste en attente.",
			"The XDCC bot "+bot+" is offline for now: the request stays queued.")
	case cp.State == StateQueued && lv.queuePos > 0:
		return notice(errPreparing,
			fmt.Sprintf("En file d'attente chez le bot XDCC %s (position %d). Relancez la lecture plus tard.", bot, lv.queuePos),
			fmt.Sprintf("Queued on the XDCC bot %s (position %d). Play again later.", bot, lv.queuePos))
	case cp.State == StateQueued || lv.current <= 0:
		return notice(errPreparing,
			"Demandé au bot XDCC "+bot+" : le transfert commence. Relancez la lecture dans un instant.",
			"Requested from the XDCC bot "+bot+": the transfer is starting. Play again in a moment.")
	}
	pct := 0
	if cp.Size > 0 {
		pct = min(int(lv.current*100/cp.Size), 99)
	}
	eta := ""
	if lv.eta > 0 {
		eta = roundDuration(lv.eta)
	}
	if s.streamable(context.Background()) {
		return notice(errPreparing,
			fmt.Sprintf("Transfert XDCC en cours (%d %%) : la lecture pourra commencer dans un instant. Relancez-la.", pct),
			fmt.Sprintf("XDCC transfer running (%d%%): playback can start in a moment. Play again.", pct))
	}
	fr := fmt.Sprintf("Téléchargement XDCC en cours : %d %%", pct)
	en := fmt.Sprintf("XDCC download running: %d%%", pct)
	if eta != "" {
		fr += ", encore " + eta + " environ"
		en += ", about " + eta + " left"
	}
	return notice(errPreparing, fr+". Relancez la lecture une fois terminé.", en+". Play again once it finishes.")
}

func roundDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "1 min"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Round(time.Minute)/time.Minute))
	default:
		return fmt.Sprintf("%d h %02d", int(d/time.Hour), int(d%time.Hour/time.Minute))
	}
}

// IsNotice indique une erreur portant un message XDCC.
func IsNotice(err error) bool {
	var n *core.LocalNoticeError
	return errors.As(err, &n)
}
