package vela

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type velaVideoOffPolicy struct{ viewerID int64 }

func (p velaVideoOffPolicy) SetMaxRating(context.Context, *frontend.Viewer, string) error { return nil }
func (p velaVideoOffPolicy) Allowed(*core.User, *core.Title) bool                         { return true }
func (p velaVideoOffPolicy) DiscoveryAllowed(*core.User, *core.Title) bool                { return true }
func (p velaVideoOffPolicy) TitleFacts(*core.User) core.TitleFacts                        { return 0 }
func (p velaVideoOffPolicy) VideoTranscodingDisabled(u *core.User) bool {
	return u != nil && u.ID == p.viewerID
}

func TestFailForVideoTranscodingOffWording(t *testing.T) {
	inst := &instance{host: frontend.Host{ViewerPolicy: velaVideoOffPolicy{viewerID: 2}}}

	check := func(v *core.Viewer, want string) {
		t.Helper()
		rec := httptest.NewRecorder()
		inst.failFor(rec, v, frontend.ErrVideoTranscodingDisabled)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", rec.Code)
		}
		var env struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("json: %v", err)
		}
		if env.Error.Message != want {
			t.Fatalf("message = %q, want %q", env.Error.Message, want)
		}
	}

	check(&core.Viewer{ID: 2}, frontend.MsgViewerVideoTranscodingOff)
	check(&core.Viewer{ID: 1, IsOwner: true}, frontend.MsgVideoTranscodingOff)

	langErr := &frontend.RequiredAudioLanguageError{Language: "English"}
	langRec := httptest.NewRecorder()
	inst.failFor(langRec, &core.Viewer{ID: 1, IsOwner: true}, langErr)
	if langRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", langRec.Code)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(langRec.Body.Bytes(), &env); err != nil {
		t.Fatalf("json: %v", err)
	}
	if env.Error.Code != "required_audio_language" || env.Error.Message != langErr.Error() {
		t.Fatalf("vela error = %+v, want required_audio_language / %q", env.Error, langErr.Error())
	}
}
