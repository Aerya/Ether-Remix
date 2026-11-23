package silo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type siloVideoOffPolicy struct{ viewerID int64 }

func (p siloVideoOffPolicy) SetMaxRating(context.Context, *frontend.Viewer, string) error { return nil }
func (p siloVideoOffPolicy) Allowed(*core.User, *core.Title) bool                         { return true }
func (p siloVideoOffPolicy) DiscoveryAllowed(*core.User, *core.Title) bool                { return true }
func (p siloVideoOffPolicy) TitleFacts(*core.User) core.TitleFacts                        { return 0 }
func (p siloVideoOffPolicy) VideoTranscodingDisabled(u *core.User) bool {
	return u != nil && u.ID == p.viewerID
}

func TestPlaybackErrorVideoTranscodingOff(t *testing.T) {
	rec := httptest.NewRecorder()
	playbackError(rec, frontend.ErrVideoTranscodingDisabled)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), frontend.MsgVideoTranscodingOff) {
		t.Fatalf("playbackError = %d %q", rec.Code, rec.Body.String())
	}

	inst := &instance{host: frontend.Host{ViewerPolicy: siloVideoOffPolicy{viewerID: 2}}}
	friendRec := httptest.NewRecorder()
	inst.playbackError(friendRec, &core.Viewer{ID: 2}, frontend.ErrVideoTranscodingDisabled)
	if friendRec.Code != http.StatusUnprocessableEntity || !strings.Contains(friendRec.Body.String(), frontend.MsgViewerVideoTranscodingOff) {
		t.Fatalf("per-viewer playbackError = %d %q, want %q", friendRec.Code, friendRec.Body.String(), frontend.MsgViewerVideoTranscodingOff)
	}
	ownerRec := httptest.NewRecorder()
	inst.playbackError(ownerRec, &core.Viewer{ID: 1, IsOwner: true}, frontend.ErrVideoTranscodingDisabled)
	if ownerRec.Code != http.StatusUnprocessableEntity || !strings.Contains(ownerRec.Body.String(), frontend.MsgVideoTranscodingOff) {
		t.Fatalf("server-wide playbackError = %d %q, want %q", ownerRec.Code, ownerRec.Body.String(), frontend.MsgVideoTranscodingOff)
	}
	langErr := &frontend.RequiredAudioLanguageError{Language: "English"}
	langRec := httptest.NewRecorder()
	playbackError(langRec, langErr)
	if langRec.Code != http.StatusUnprocessableEntity || !strings.Contains(langRec.Body.String(), langErr.Error()) {
		t.Fatalf("playbackError(RequiredAudioLanguageError) = %d %q, want %q", langRec.Code, langRec.Body.String(), langErr.Error())
	}
}
