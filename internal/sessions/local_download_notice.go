package sessions

// Ajout Ether-Remix : quand une lecture attend un téléchargement sur le
// client torrent du propriétaire (PSE_TORRENT), Jellyfin et Emby reçoivent
// un message à l'écran (DisplayMessage), là où ils n'afficheraient qu'une
// erreur générique. Plex reçoit le même texte par son message de fin.

import (
	"context"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// Plan voir plan ; prévient le lecteur d'un téléchargement en cours.
func (b *Broker) Plan(ctx context.Context, req frontend.PlanRequest) (frontend.PlayPlan, error) {
	p, err := b.plan(ctx, req)
	if err != nil {
		b.localDownloadNotice(ctx, req.Viewer, req.Protocol, req.Device, err)
	}
	return p, err
}

// Open voir open ; prévient le lecteur d'un téléchargement en cours.
func (b *Broker) Open(ctx context.Context, req frontend.OpenRequest) (frontend.Session, error) {
	s, err := b.open(ctx, req)
	if err != nil {
		b.localDownloadNotice(ctx, req.Viewer, req.Protocol, req.Device, err)
	}
	return s, err
}

// localNoticeEvery évite de répéter le message à chaque nouvel essai du lecteur.
const localNoticeEvery = 30 * time.Second

var localNotices struct {
	sync.Mutex
	last map[string]time.Time
}

func (b *Broker) localDownloadNotice(ctx context.Context, v *core.Viewer, protocol frontend.Protocol, dev core.Device, err error) {
	if v == nil || v.ID <= 0 || dev.ID == "" || protocol == "plex" || b.deps.Events == nil {
		return
	}
	msg := core.LocalDownloadMessage(err, "")
	if msg == "" {
		return
	}
	key := string(protocol) + "\x00" + dev.ID
	now := time.Now()
	localNotices.Lock()
	if localNotices.last == nil || len(localNotices.last) > 1000 {
		localNotices.last = map[string]time.Time{}
	}
	if at, ok := localNotices.last[key]; ok && now.Sub(at) < localNoticeEvery {
		localNotices.Unlock()
		return
	}
	localNotices.last[key] = now
	localNotices.Unlock()
	cmd := frontend.Command{Kind: frontend.CommandMessage, Message: msg}
	b.publish(context.WithoutCancel(ctx), frontend.Event{Type: "SessionCommand", ViewerID: v.ID, ActorViewerID: v.ID,
		TargetProtocol: protocol, TargetDeviceID: dev.ID, Command: &cmd})
}
