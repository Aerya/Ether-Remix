package admin

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plextv"
	"github.com/braedonsaunders/ether/internal/plexwatch"
)

type PlexCandidateAccount struct {
	ServerID              int64    `json:"serverId"`
	ServerName            string   `json:"serverName"`
	AccountID             int64    `json:"accountId"`
	AccountName           string   `json:"accountName"`
	IsOwner               bool     `json:"isOwner"`
	MatchedViewerID       int64    `json:"matchedViewerId"`
	MatchedViewerName     string   `json:"matchedViewerName,omitempty"`
	MatchedViewerUsername string   `json:"matchedViewerUsername,omitempty"`
	SuggestedViewerID     int64    `json:"suggestedViewerId,omitempty"`
	SuggestedViewerName   string   `json:"suggestedViewerName,omitempty"`
	PlexUsername          string   `json:"plexUsername,omitempty"`
	PlexRelation          string   `json:"plexRelation,omitempty"`
	PlexSources           []string `json:"plexSources,omitempty"`
}

type PlexCandidateServer struct {
	ServerID           int64                  `json:"serverId"`
	ServerName         string                 `json:"serverName"`
	PlexDirectoryTotal int                    `json:"plexDirectoryTotal,omitempty"`
	FilteredOutCount   int                    `json:"filteredOutCount,omitempty"`
	Accounts           []PlexCandidateAccount `json:"accounts"`
}

type ImportPlexViewerRequest struct {
	ServerID  int64  `json:"serverId"`
	AccountID int64  `json:"accountId"`
	Name      string `json:"name,omitempty"`
}

type MatchPlexViewerRequest struct {
	ServerID  int64 `json:"serverId"`
	AccountID int64 `json:"accountId"`
	ViewerID  int64 `json:"viewerId"`
}

func (h *Handler) viewersPlexImportRoutes(api func(pattern string, fn http.HandlerFunc)) {
	api("GET /api/viewers/plex-candidates", h.handlePlexCandidates)
	api("POST /api/viewers/import-plex", h.handleImportPlexViewer)
	api("POST /api/viewers/match-plex", h.handleMatchPlexViewer)
	api("POST /api/viewers/import-plex/all", h.handleImportAllPlexViewers)
}

func cleanDisplayName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Plex Viewer"
	}
	r := []rune(name)
	if len(r) > 120 {
		name = strings.TrimSpace(string(r[:120]))
	}
	if name == "" {
		return "Plex Viewer"
	}
	return name
}

// findExactViewer checks for an unambiguous existing match:
// 1. An account already mapped to a viewer (acct.UserID > 0).
// 2. The PMS server owner matched to the owner viewer.
// 3. Exact stable PlexID match (when known).
func findExactViewer(viewers []ViewerRecord, acct plexwatch.PMSAccount) *ViewerRecord {
	// 1. Existing mapping
	if acct.UserID > 0 {
		for i := range viewers {
			if viewers[i].ID == acct.UserID {
				return &viewers[i]
			}
		}
	}
	// 2. Server owner
	if acct.Owner {
		for i := range viewers {
			if viewers[i].IsOwner {
				return &viewers[i]
			}
		}
	}
	// 3. Stable Plex ID
	if !acct.Owner && acct.ID > 0 {
		for i := range viewers {
			if viewers[i].PlexID > 0 && viewers[i].PlexID == acct.ID {
				return &viewers[i]
			}
		}
	}
	return nil
}

// suggestMatchingViewer checks in strict priority order:
// 1. Exact match (owner or mapped).
// 2. Exact stable Plex ID match across all viewers.
// 3. Name or email heuristics (title, username, email).
// Heuristics are ONLY used to populate candidate suggestions for the owner to review and confirm,
// NEVER to automatically adopt or merge viewers during import.
func suggestMatchingViewer(viewers []ViewerRecord, acct plexwatch.PMSAccount) *ViewerRecord {
	if exact := findExactViewer(viewers, acct); exact != nil {
		return exact
	}
	targetName := strings.TrimSpace(acct.Name)
	if targetName == "" {
		return nil
	}
	globalID := fallbackPMSAccountPlexID(acct)
	for i := range viewers {
		v := &viewers[i]
		if v.IsOwner && !acct.Owner {
			continue
		}
		if globalID > 0 && v.PlexID > 0 && v.PlexID != globalID {
			continue
		}
		if v.Title != "" && strings.EqualFold(strings.TrimSpace(v.Title), targetName) {
			return v
		}
		if v.Username != "" && strings.EqualFold(strings.TrimSpace(v.Username), targetName) {
			return v
		}
		if v.Email != "" && strings.EqualFold(strings.TrimSpace(v.Email), targetName) {
			return v
		}
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func compactName(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), ""))
}

// resolvePlexUser matches a PMS account against the owner's plex.tv friends and
// home users in strict priority order via plextv.ResolveSharedUser.
func resolvePlexUser(acct plexwatch.PMSAccount, plexUsers []plextv.SharedUser) *plextv.SharedUser {
	if acct.Owner || len(plexUsers) == 0 {
		return nil
	}
	if acct.ID >= minGlobalPlexAccountID {
		return plextv.ResolveSharedUser(plexUsers, acct.ID, "")
	}
	return plextv.ResolveSharedUser(plexUsers, acct.ID, acct.Name)
}

func findViewerByPlexID(viewers []ViewerRecord, plexID int64) *ViewerRecord {
	if plexID <= 0 {
		return nil
	}
	for i := range viewers {
		if viewers[i].PlexID == plexID {
			return &viewers[i]
		}
	}
	return nil
}

// minGlobalPlexAccountID guards against treating PMS local/synthetic sequential
// account IDs (e.g. owner ID 1 or small local Ether viewer IDs emitted by
// internal/frontends/plex/accounts.go) as global plex.tv user IDs.
// Real non-owner plex.tv accounts stored in PMS /accounts carry their global plex.tv ID.
const minGlobalPlexAccountID int64 = 10000

func fallbackPMSAccountPlexID(acct plexwatch.PMSAccount) int64 {
	if acct.Owner || acct.ID < minGlobalPlexAccountID {
		return 0
	}
	return acct.ID
}

func findSyncedFriendViewer(viewers []ViewerRecord, acct plexwatch.PMSAccount, resolved *plextv.SharedUser) *ViewerRecord {
	if m := findExactViewer(viewers, acct); m != nil {
		return m
	}
	var expectedPlexID int64
	if resolved != nil && resolved.PlexID > 0 {
		expectedPlexID = resolved.PlexID
		if m := findViewerByPlexID(viewers, expectedPlexID); m != nil {
			return m
		}
	}
	if expectedPlexID == 0 {
		expectedPlexID = fallbackPMSAccountPlexID(acct)
	}
	if expectedPlexID > 0 {
		// findExactViewer and findViewerByPlexID already checked exact PlexID;
		// never fall through to name matching across different PlexIDs.
		return nil
	}
	compatible := func(v ViewerRecord) bool {
		return v.Origin == "friend" && v.PlexID > 0
	}
	target := strings.TrimSpace(acct.Name)
	unique := func(pred func(v ViewerRecord) bool) (*ViewerRecord, bool) {
		var matched *ViewerRecord
		for i := range viewers {
			v := viewers[i]
			if !compatible(v) || !pred(v) {
				continue
			}
			if matched != nil && matched.ID != v.ID {
				return nil, true
			}
			matched = &viewers[i]
		}
		return matched, false
	}
	if m, amb := unique(func(v ViewerRecord) bool {
		if target != "" && (strings.EqualFold(strings.TrimSpace(v.Username), target) || strings.EqualFold(strings.TrimSpace(v.Email), target)) {
			return true
		}
		if resolved != nil {
			if resolved.Username != "" && strings.EqualFold(strings.TrimSpace(v.Username), strings.TrimSpace(resolved.Username)) {
				return true
			}
			if resolved.Email != "" && strings.EqualFold(strings.TrimSpace(v.Email), strings.TrimSpace(resolved.Email)) {
				return true
			}
		}
		return false
	}); m != nil || amb {
		return m
	}
	if target != "" {
		if m, amb := unique(func(v ViewerRecord) bool {
			return v.Title != "" && strings.EqualFold(strings.TrimSpace(v.Title), target)
		}); m != nil || amb {
			return m
		}
		wantCompact := compactName(target)
		if m, _ := unique(func(v ViewerRecord) bool {
			return (v.Username != "" && compactName(v.Username) == wantCompact) ||
				(v.Title != "" && compactName(v.Title) == wantCompact)
		}); m != nil {
			return m
		}
	}
	return nil
}

func (h *Handler) ensureResolvedViewer(ctx context.Context, viewers *[]ViewerRecord, resolved *plextv.SharedUser, fallbackName string) (int64, string) {
	if resolved == nil || resolved.PlexID <= 0 {
		return 0, ""
	}
	if existing := findViewerByPlexID(*viewers, resolved.PlexID); existing != nil {
		if existing.IsOwner {
			return existing.ID, "matched"
		}
		return existing.ID, "imported"
	}
	if h.deps.EnsurePlexFriend == nil {
		return 0, ""
	}
	friend := *resolved
	if strings.TrimSpace(friend.Title) == "" {
		friend.Title = strings.TrimSpace(friend.FriendlyName)
	}
	id, err := h.deps.EnsurePlexFriend(ctx, friend)
	if err != nil {
		h.log.Warn("ensure plex friend error", "plexId", resolved.PlexID, "err", err)
		return 0, ""
	}
	if id <= 0 {
		return 0, ""
	}
	*viewers = append(*viewers, ViewerRecord{
		User: core.User{
			ID:       id,
			PlexID:   resolved.PlexID,
			Username: resolved.Username,
			Title:    firstNonEmpty(resolved.Title, resolved.FriendlyName, resolved.Username, fallbackName),
			Email:    resolved.Email,
		},
		Origin:  "friend",
		HasPlex: true,
	})
	return id, "imported"
}

func (h *Handler) listPlexUsers(ctx context.Context) []plextv.SharedUser {
	if h.deps.ListPlexUsers == nil {
		return nil
	}
	users, err := h.deps.ListPlexUsers(ctx)
	if err != nil {
		h.log.Warn("list plex.tv users error", "err", err)
		return nil
	}
	return users
}

func (h *Handler) shareViewerPlex(ctx context.Context, usernameOrEmail string, userID int64, batch bool) error {
	return h.shareViewerPlexTarget(ctx, usernameOrEmail, userID, batch)
}

func (h *Handler) shareViewerPlexViewer(ctx context.Context, v *ViewerRecord, fallbackName string, batch bool) error {
	if v == nil {
		return h.shareViewerPlexTarget(ctx, fallbackName, 0, batch)
	}
	target := firstNonEmpty(v.Email, v.Username, v.Title, fallbackName)
	return h.shareViewerPlexTarget(ctx, target, v.PlexID, batch)
}

func (h *Handler) shareViewerPlexTarget(ctx context.Context, usernameOrEmail string, userID int64, batch bool) error {
	usernameOrEmail = strings.TrimSpace(usernameOrEmail)
	if usernameOrEmail == "" && userID <= 0 {
		return errors.New("plextv: username, email or user ID required")
	}
	shareFn := h.deps.SharePlexViewer
	if batch && h.deps.SharePlexViewerNoSync != nil {
		shareFn = h.deps.SharePlexViewerNoSync
	}
	if shareFn != nil {
		err := shareFn(ctx, usernameOrEmail, userID)
		if err != nil {
			h.log.Warn("share plex library error", "account", usernameOrEmail, "userId", userID, "err", err)
		} else {
			h.log.Info("shared server libraries on plex.tv", "account", usernameOrEmail, "userId", userID)
		}
		return err
	}
	if h.deps.Config == nil {
		return errors.New("plextv: config dependency not available")
	}
	cfg := h.deps.Config.Get()
	token := cfg.Plex.Token
	machineID := cfg.Server.MachineIdentifier
	if token == "" {
		return errors.New("plextv: plex token not configured")
	}
	if machineID == "" {
		return errors.New("plextv: server machine identifier not configured")
	}
	client, err := plextv.New(plextv.Options{
		Identity:     plextv.ServerIdentity(cfg, ""),
		HTTPClient:   &http.Client{Timeout: 15 * time.Second},
		RetryBackoff: 200 * time.Millisecond,
	})
	if err != nil {
		h.log.Warn("create plextv client error", "err", err)
		return err
	}
	err = client.ShareServer(ctx, token, usernameOrEmail, userID, nil)
	if err != nil {
		h.log.Warn("share plex library error", "account", usernameOrEmail, "userId", userID, "err", err)
	} else {
		h.log.Info("shared server libraries on plex.tv", "account", usernameOrEmail, "userId", userID)
	}
	return err
}

func classifyPlexCandidate(acct plexwatch.PMSAccount, u *plextv.SharedUser, pmsMachineID, etherMachineID string, hasPlexDirectory bool) (plexUsername, relation string, sources []string) {
	if acct.Owner {
		return "", "owner", []string{"owner"}
	}
	if u == nil {
		if hasPlexDirectory {
			return "", "historical", []string{"pms_history"}
		}
		return "", "", nil
	}
	plexUsername = strings.TrimSpace(u.Username)
	sharedOnEther := etherMachineID != "" && slices.Contains(u.Servers, etherMachineID)
	sharedOnPMS := pmsMachineID != "" && slices.Contains(u.Servers, pmsMachineID)
	pendingOnEther := etherMachineID != "" && slices.Contains(u.PendingServers, etherMachineID)
	pendingOnPMS := pmsMachineID != "" && slices.Contains(u.PendingServers, pmsMachineID)

	if u.Home {
		sources = append(sources, "home")
	}
	if sharedOnEther {
		sources = append(sources, "ether_share")
	}
	if sharedOnPMS {
		sources = append(sources, "pms_share")
	}
	if u.AcceptedShare && !sharedOnEther && !sharedOnPMS {
		sources = append(sources, "accepted_share")
	}
	if u.PendingShare || pendingOnEther || pendingOnPMS {
		sources = append(sources, "pending_invite")
	}
	if u.CommunityFriend {
		sources = append(sources, "community_friend")
	}
	if u.Friend {
		sources = append(sources, "friend")
	}

	switch {
	case u.Home:
		relation = "home"
	case u.AcceptedShare || sharedOnPMS || sharedOnEther:
		relation = "shared"
	case u.PendingShare || pendingOnEther || pendingOnPMS:
		relation = "pending"
	case u.CommunityFriend || u.Friend:
		relation = "friend"
	default:
		relation = "shared"
	}
	return plexUsername, relation, sources
}

// isActivePlexUserWithEmail reports whether a resolved plex.tv account is an
// active (accepted, non-pending-only) Plex user with an email address.
func isActivePlexUserWithEmail(u *plextv.SharedUser, pmsMachineID, etherMachineID string) bool {
	if u == nil {
		return false
	}
	if !plextv.IsEmailAddress(u.Email) {
		return false
	}
	hasAccepted := u.AcceptedShare ||
		(pmsMachineID != "" && slices.Contains(u.Servers, pmsMachineID)) ||
		(etherMachineID != "" && slices.Contains(u.Servers, etherMachineID))
	if hasAccepted {
		return true
	}
	if u.PendingShare || len(u.PendingServers) > 0 || u.Home || u.Friend || u.CommunityFriend {
		return false
	}
	return true
}

func (h *Handler) handlePlexCandidates(w http.ResponseWriter, r *http.Request) {
	access, _ := RequestAccess(r.Context())
	if !access.Owner {
		writeError(w, http.StatusForbidden, "forbidden", "owner required")
		return
	}
	if h.deps.PlexServers == nil {
		writeJSON(w, http.StatusOK, []PlexCandidateServer{})
		return
	}
	servers, err := h.deps.PlexServers.List(r.Context())
	if err != nil {
		h.plexServerError(w, err)
		return
	}

	viewers, _ := h.storedViewers(r.Context())
	viewerByID := make(map[int64]ViewerRecord, len(viewers))
	for _, v := range viewers {
		viewerByID[v.ID] = v
	}

	plexUsers := h.listPlexUsers(r.Context())
	hasPlexDirectory := len(plexUsers) > 0
	var etherMachineID string
	if h.deps.Config != nil {
		etherMachineID = h.deps.Config.Get().Server.MachineIdentifier
	}

	var result []PlexCandidateServer
	for _, srv := range servers {
		accounts, err := h.deps.PlexServers.Accounts(r.Context(), srv.ID)
		if err != nil {
			h.log.Warn("plex candidates accounts error", "server", srv.ID, "err", err)
			continue
		}
		list := make([]PlexCandidateAccount, 0)
		filteredOut := 0
		for _, acct := range accounts {
			var resolvedPlex *plextv.SharedUser
			if !acct.Owner && hasPlexDirectory {
				resolvedPlex = resolvePlexUser(acct, plexUsers)
				if acct.UserID == 0 && !isActivePlexUserWithEmail(resolvedPlex, srv.MachineID, etherMachineID) {
					filteredOut++
					continue
				}
			}

			matchedID := acct.UserID
			var (
				suggestedID     int64
				suggestedName   string
				matchedName     string
				matchedUsername string
			)

			if matchedID > 0 {
				if v, ok := viewerByID[matchedID]; ok {
					matchedName = v.Title
					if matchedName == "" {
						matchedName = v.Username
					}
					matchedUsername = v.Username
				}
			} else {
				// Pure in-memory heuristic suggestion without GET side-effects.
				if suggested := suggestMatchingViewer(viewers, acct); suggested != nil {
					suggestedID = suggested.ID
					suggestedName = suggested.Title
					if suggestedName == "" {
						suggestedName = suggested.Username
					}
				}
			}

			plexUsername, plexRelation, plexSources := classifyPlexCandidate(acct, resolvedPlex, srv.MachineID, etherMachineID, hasPlexDirectory)

			list = append(list, PlexCandidateAccount{
				ServerID:              srv.ID,
				ServerName:            srv.Name,
				AccountID:             acct.ID,
				AccountName:           acct.Name,
				IsOwner:               acct.Owner,
				MatchedViewerID:       matchedID,
				MatchedViewerName:     matchedName,
				MatchedViewerUsername: matchedUsername,
				SuggestedViewerID:     suggestedID,
				SuggestedViewerName:   suggestedName,
				PlexUsername:          plexUsername,
				PlexRelation:          plexRelation,
				PlexSources:           plexSources,
			})
		}
		result = append(result, PlexCandidateServer{
			ServerID:           srv.ID,
			ServerName:         srv.Name,
			PlexDirectoryTotal: len(plexUsers),
			FilteredOutCount:   filteredOut,
			Accounts:           list,
		})
	}
	if result == nil {
		result = []PlexCandidateServer{}
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handleImportPlexViewer(w http.ResponseWriter, r *http.Request) {
	access, _ := RequestAccess(r.Context())
	if !access.Owner || h.deps.PlexServers == nil || h.deps.Invites == nil || h.deps.RefreshViewers == nil {
		writeError(w, http.StatusForbidden, "forbidden", "owner and required services required")
		return
	}
	var body ImportPlexViewerRequest
	if decodeJSON(r, &body) != nil || body.ServerID <= 0 || body.AccountID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid", "invalid server or account ID")
		return
	}
	accounts, err := h.deps.PlexServers.Accounts(r.Context(), body.ServerID)
	if err != nil {
		h.plexServerError(w, err)
		return
	}
	var acct *plexwatch.PMSAccount
	for i := range accounts {
		if accounts[i].ID == body.AccountID {
			acct = &accounts[i]
			break
		}
	}
	if acct == nil {
		writeError(w, http.StatusNotFound, "not_found", "account not found on server")
		return
	}

	displayName := cleanDisplayName(body.Name)
	if displayName == "Plex Viewer" && strings.TrimSpace(acct.Name) != "" {
		displayName = cleanDisplayName(acct.Name)
	}

	viewers, _ := h.storedViewers(r.Context())

	var (
		newViewerID int64
		inviteURL   string
		direction   = 2 // Plex -> Ether
		status      = "imported"
		sharedPlex  bool
		shareErr    string
	)
	if acct.Owner {
		direction = 3 // Bidirectional for owner
	}

	var (
		matched  *ViewerRecord
		resolved *plextv.SharedUser
	)
	// 1. Check if an existing viewer matches EXACTLY (mapping, owner, stable Plex ID)
	if m := findExactViewer(viewers, *acct); m != nil {
		matched = m
		newViewerID = m.ID
		status = "matched"
	}

	// 2. If no exact match and not owner, resolve against Plex.tv users (when available)
	// and attempt to grant library access on Plex.tv first.
	// Sharing on Plex.tv synchronizes authoritative Plex friend membership into auth_users,
	// creating/updating the Plex user record with their global Plex ID before we fall back
	// to creating a local tokenless viewer.
	if newViewerID == 0 && !acct.Owner {
		var ownerPlexID int64
		if h.deps.Config != nil {
			ownerPlexID = h.deps.Config.Get().Plex.OwnerPlexID
		}
		resolved = resolvePlexUser(*acct, h.listPlexUsers(r.Context()))
		if resolved != nil && resolved.PlexID > 0 {
			if existing := findViewerByPlexID(viewers, resolved.PlexID); existing != nil && existing.IsOwner {
				matched = existing
				newViewerID = existing.ID
				status = "matched"
			}
		}
		if newViewerID == 0 {
			shareTarget := acct.Name
			shareUserID := fallbackPMSAccountPlexID(*acct)
			if resolved != nil {
				shareUserID = resolved.PlexID
				shareTarget = firstNonEmpty(resolved.Email, resolved.Username, acct.Name)
			}
			isSelfOwner := ownerPlexID > 0 && shareUserID == ownerPlexID
			if !isSelfOwner && (shareUserID > 0 || strings.TrimSpace(shareTarget) != "") {
				if err := h.shareViewerPlex(r.Context(), shareTarget, shareUserID, false); err != nil {
					shareErr = err.Error()
				} else {
					sharedPlex = true
					if reloaded, err := h.storedViewers(r.Context()); err == nil {
						viewers = reloaded
						if synced := findSyncedFriendViewer(viewers, *acct, resolved); synced != nil {
							newViewerID = synced.ID
							status = "imported"
						}
					}
					if newViewerID == 0 && resolved == nil {
						resolved = resolvePlexUser(*acct, h.listPlexUsers(r.Context()))
					}
				}
			}
			// If resolved on Plex.tv (or verified by a successful share via global PMS ID)
			// but not yet in auth_users, create/update the friend viewer with full Plex profile
			// instead of a blank local invite.
			if newViewerID == 0 && resolved != nil && !isSelfOwner {
				if id, st := h.ensureResolvedViewer(r.Context(), &viewers, resolved, displayName); id > 0 {
					newViewerID = id
					status = st
				}
			}
			if newViewerID == 0 && sharedPlex && shareUserID > 0 && !isSelfOwner {
				if id, st := h.ensureResolvedViewer(r.Context(), &viewers, &plextv.SharedUser{
					PlexID: shareUserID,
					Title:  displayName,
				}, displayName); id > 0 {
					newViewerID = id
					status = st
				}
			}
		}
	}

	// 3. If still no viewer ID (e.g. local managed user without Plex.tv account, share failure,
	// or offline/test environment), create a local passwordless viewer via invite.
	if newViewerID == 0 {
		id, secret, err := h.deps.Invites.CreateViewer(r.Context(), displayName)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid", "could not create viewer: "+err.Error())
			return
		}
		newViewerID = id
		inviteURL = h.inviteURL(secret)
	}

	// 4. Map account in plexwatch.Servers
	_, err = h.deps.PlexServers.MapAccount(r.Context(), body.ServerID, body.AccountID, plexwatch.PMSMappingInput{
		UserID:    newViewerID,
		Direction: direction,
		Interval:  10,
	})
	if err != nil {
		h.plexServerError(w, err)
		return
	}

	// 5. If matched in step 1 and not yet shared on Plex.tv, share library access now.
	// Only share if the matched viewer is a non-owner Plex-authenticated user (PlexID > 0).
	// For local viewers (PlexID == 0), sharing on Plex.tv would sync a duplicate friend into auth_users.
	if !acct.Owner && matched != nil && !matched.IsOwner && matched.PlexID > 0 && !sharedPlex && shareErr == "" {
		if err := h.shareViewerPlexViewer(r.Context(), matched, acct.Name, false); err != nil {
			shareErr = err.Error()
		} else {
			sharedPlex = true
		}
	}

	if err := h.deps.RefreshViewers(r.Context()); err != nil {
		h.log.Warn("refresh viewers error", "err", err)
	}

	res := map[string]any{
		"id":         newViewerID,
		"name":       displayName,
		"inviteUrl":  inviteURL,
		"serverId":   body.ServerID,
		"accountId":  body.AccountID,
		"status":     status,
		"sharedPlex": sharedPlex,
	}
	if shareErr != "" {
		res["shareError"] = shareErr
	}
	writeJSON(w, http.StatusCreated, res)
}

func (h *Handler) handleMatchPlexViewer(w http.ResponseWriter, r *http.Request) {
	access, _ := RequestAccess(r.Context())
	if !access.Owner || h.deps.PlexServers == nil || h.deps.RefreshViewers == nil {
		writeError(w, http.StatusForbidden, "forbidden", "owner required")
		return
	}
	var body MatchPlexViewerRequest
	if decodeJSON(r, &body) != nil || body.ServerID <= 0 || body.AccountID <= 0 || body.ViewerID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid", "invalid IDs")
		return
	}

	accounts, err := h.deps.PlexServers.Accounts(r.Context(), body.ServerID)
	if err != nil {
		h.plexServerError(w, err)
		return
	}
	var (
		isOwner  bool
		acctName string
		found    bool
	)
	for _, a := range accounts {
		if a.ID == body.AccountID {
			isOwner = a.Owner
			acctName = a.Name
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "not_found", "account not found on server")
		return
	}
	direction := 2
	if isOwner {
		direction = 3 // Bidirectional for owner
	}

	_, err = h.deps.PlexServers.MapAccount(r.Context(), body.ServerID, body.AccountID, plexwatch.PMSMappingInput{
		UserID:    body.ViewerID,
		Direction: direction,
		Interval:  10,
	})
	if err != nil {
		h.plexServerError(w, err)
		return
	}

	// Grant access on Plex.tv if not owner and the viewer is a non-owner Plex-authenticated user (PlexID > 0).
	// For local viewers (PlexID == 0), sharing on Plex.tv would sync a duplicate friend into auth_users.
	var (
		sharedPlex bool
		shareErr   string
	)
	var matchedViewer *ViewerRecord
	if viewers, err := h.storedViewers(r.Context()); err == nil {
		for i := range viewers {
			if viewers[i].ID == body.ViewerID {
				matchedViewer = &viewers[i]
				break
			}
		}
	}
	if !isOwner && matchedViewer != nil && !matchedViewer.IsOwner && matchedViewer.PlexID > 0 {
		if err := h.shareViewerPlexViewer(r.Context(), matchedViewer, acctName, false); err != nil {
			shareErr = err.Error()
		} else {
			sharedPlex = true
		}
	}

	if err := h.deps.RefreshViewers(r.Context()); err != nil {
		h.log.Warn("refresh viewers error", "err", err)
	}
	res := map[string]any{
		"ok":         true,
		"viewerId":   body.ViewerID,
		"serverId":   body.ServerID,
		"accountId":  body.AccountID,
		"sharedPlex": sharedPlex,
	}
	if shareErr != "" {
		res["shareError"] = shareErr
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) handleImportAllPlexViewers(w http.ResponseWriter, r *http.Request) {
	access, _ := RequestAccess(r.Context())
	if !access.Owner || h.deps.PlexServers == nil || h.deps.Invites == nil || h.deps.RefreshViewers == nil {
		writeError(w, http.StatusForbidden, "forbidden", "owner required")
		return
	}
	servers, err := h.deps.PlexServers.List(r.Context())
	if err != nil {
		h.plexServerError(w, err)
		return
	}

	type bulkImportResultItem struct {
		ID         int64  `json:"id,omitempty"`
		Name       string `json:"name"`
		InviteURL  string `json:"inviteUrl,omitempty"`
		ServerID   int64  `json:"serverId"`
		AccountID  int64  `json:"accountId"`
		Status     string `json:"status"` // "imported", "matched", "skipped", "failed"
		Reason     string `json:"reason,omitempty"`
		SharedPlex bool   `json:"sharedPlex"`
		ShareError string `json:"shareError,omitempty"`
		Error      string `json:"error,omitempty"`
	}

	type deferredImport struct {
		resultIdx   int
		serverID    int64
		acct        plexwatch.PMSAccount
		name        string
		direction   int
		shareUserID int64
		shareErr    string
	}

	var (
		results        []bulkImportResultItem
		deferred       []deferredImport
		sharedPlexIDs  = make(map[int64]bool)
		sharedAny      bool
		rateLimitedErr error
	)
	viewers, _ := h.storedViewers(r.Context())
	plexUsers := h.listPlexUsers(r.Context())
	hasPlexDirectory := len(plexUsers) > 0
	var (
		etherMachineID string
		ownerPlexID    int64
	)
	if h.deps.Config != nil {
		cfg := h.deps.Config.Get()
		etherMachineID = cfg.Server.MachineIdentifier
		ownerPlexID = cfg.Plex.OwnerPlexID
	}

	finalize := func(serverID int64, acct plexwatch.PMSAccount, name string, direction int, vID int64, status string, matched *ViewerRecord, sharedPlex bool, shareErr string) bulkImportResultItem {
		var inviteURL string
		// Fall back to local invite viewer if not matched/resolved.
		if vID == 0 {
			id, secret, err := h.deps.Invites.CreateViewer(r.Context(), name)
			if err != nil {
				return bulkImportResultItem{
					Name:      name,
					ServerID:  serverID,
					AccountID: acct.ID,
					Status:    "failed",
					Error:     "could not create viewer: " + err.Error(),
				}
			}
			vID = id
			inviteURL = h.inviteURL(secret)
			viewers = append(viewers, ViewerRecord{User: core.User{ID: id, Title: name}})
		}

		if _, err := h.deps.PlexServers.MapAccount(r.Context(), serverID, acct.ID, plexwatch.PMSMappingInput{
			UserID:    vID,
			Direction: direction,
			Interval:  10,
		}); err != nil {
			return bulkImportResultItem{
				ID:        vID,
				Name:      name,
				ServerID:  serverID,
				AccountID: acct.ID,
				Status:    "failed",
				Error:     "map account failed: " + err.Error(),
			}
		}

		// If matched in step 1 and not yet shared on Plex.tv, share now.
		// Only share if the matched viewer is a non-owner Plex-authenticated user (PlexID > 0).
		// For local viewers (PlexID == 0), sharing on Plex.tv would sync a duplicate friend into auth_users.
		if !acct.Owner && matched != nil && !matched.IsOwner && matched.PlexID > 0 && !sharedPlex && shareErr == "" {
			if sharedPlexIDs[matched.PlexID] {
				sharedPlex = true
			} else if rateLimitedErr != nil {
				shareErr = rateLimitedErr.Error()
			} else if err := h.shareViewerPlexViewer(r.Context(), matched, acct.Name, true); err != nil {
				shareErr = err.Error()
				if errors.Is(err, plextv.ErrRateLimited) {
					rateLimitedErr = err
				}
			} else {
				sharedPlex = true
				sharedAny = true
				sharedPlexIDs[matched.PlexID] = true
			}
		}

		return bulkImportResultItem{
			ID:         vID,
			Name:       name,
			InviteURL:  inviteURL,
			ServerID:   serverID,
			AccountID:  acct.ID,
			Status:     status,
			SharedPlex: sharedPlex,
			ShareError: shareErr,
		}
	}

	for _, srv := range servers {
		accounts, err := h.deps.PlexServers.Accounts(r.Context(), srv.ID)
		if err != nil {
			h.log.Warn("list server accounts error during bulk import", "serverId", srv.ID, "err", err)
			continue
		}
		for _, acct := range accounts {
			name := cleanDisplayName(acct.Name)
			if acct.UserID > 0 {
				results = append(results, bulkImportResultItem{
					ID:        acct.UserID,
					Name:      name,
					ServerID:  srv.ID,
					AccountID: acct.ID,
					Status:    "skipped",
					Reason:    "already_mapped",
				})
				continue
			}
			var preResolved *plextv.SharedUser
			if !acct.Owner && hasPlexDirectory {
				preResolved = resolvePlexUser(acct, plexUsers)
				if !isActivePlexUserWithEmail(preResolved, srv.MachineID, etherMachineID) {
					results = append(results, bulkImportResultItem{
						Name:      name,
						ServerID:  srv.ID,
						AccountID: acct.ID,
						Status:    "skipped",
						Reason:    "ineligible_plex_account",
					})
					continue
				}
			}

			var (
				vID        int64
				status     = "imported"
				direction  = 2
				sharedPlex bool
				shareErr   string
				matched    *ViewerRecord
				resolved   = preResolved
			)
			if acct.Owner {
				direction = 3
			}

			// 1. Check for EXACT match among existing viewers (mapping, owner, stable Plex ID).
			if m := findExactViewer(viewers, acct); m != nil {
				matched = m
				vID = m.ID
				status = "matched"
			}

			// 2. If not matched and not owner, resolve against Plex.tv users and grant library access on Plex.tv.
			if vID == 0 && !acct.Owner {
				if resolved == nil {
					resolved = resolvePlexUser(acct, plexUsers)
				}
				if resolved != nil && resolved.PlexID > 0 {
					if existing := findViewerByPlexID(viewers, resolved.PlexID); existing != nil && existing.IsOwner {
						matched = existing
						vID = existing.ID
						status = "matched"
					}
				}
				if vID == 0 {
					shareTarget := acct.Name
					shareUserID := fallbackPMSAccountPlexID(acct)
					if resolved != nil {
						shareUserID = resolved.PlexID
						shareTarget = firstNonEmpty(resolved.Email, resolved.Username, acct.Name)
					}
					isSelfOwner := ownerPlexID > 0 && shareUserID == ownerPlexID
					if isSelfOwner {
						// Never share the server with its own owner account.
					} else if shareUserID > 0 && sharedPlexIDs[shareUserID] {
						sharedPlex = true
					} else if rateLimitedErr != nil {
						shareErr = rateLimitedErr.Error()
					} else if shareUserID > 0 || strings.TrimSpace(shareTarget) != "" {
						if err := h.shareViewerPlex(r.Context(), shareTarget, shareUserID, true); err != nil {
							shareErr = err.Error()
							if errors.Is(err, plextv.ErrRateLimited) {
								rateLimitedErr = err
							}
						} else {
							sharedPlex = true
							sharedAny = true
							if shareUserID > 0 {
								sharedPlexIDs[shareUserID] = true
							}
							// When using a legacy single-call SharePlexViewer hook (e.g. tests),
							// check ViewerDirectory for a newly synced friend row.
							if h.deps.SharePlexViewerNoSync == nil {
								if reloaded, err := h.storedViewers(r.Context()); err == nil {
									viewers = reloaded
									if synced := findSyncedFriendViewer(viewers, acct, resolved); synced != nil {
										vID = synced.ID
										status = "imported"
									}
								}
							}
						}
					}
					// Defer post-share resolution so ListPlexUsers runs at most once
					// for the entire batch instead of once per unresolved account.
					if vID == 0 && resolved == nil && sharedPlex && !isSelfOwner {
						idx := len(results)
						results = append(results, bulkImportResultItem{})
						deferred = append(deferred, deferredImport{
							resultIdx:   idx,
							serverID:    srv.ID,
							acct:        acct,
							name:        name,
							direction:   direction,
							shareUserID: shareUserID,
							shareErr:    shareErr,
						})
						continue
					}
					if vID == 0 && resolved != nil && !isSelfOwner {
						if id, st := h.ensureResolvedViewer(r.Context(), &viewers, resolved, name); id > 0 {
							vID = id
							status = st
						}
					}
				}
			}

			results = append(results, finalize(srv.ID, acct, name, direction, vID, status, matched, sharedPlex, shareErr))
		}
	}

	if len(deferred) > 0 {
		plexUsers = h.listPlexUsers(r.Context())
		for _, d := range deferred {
			var (
				vID    int64
				status = "imported"
			)
			if resolved := resolvePlexUser(d.acct, plexUsers); resolved != nil {
				if id, st := h.ensureResolvedViewer(r.Context(), &viewers, resolved, d.name); id > 0 {
					vID = id
					status = st
				}
			} else if d.shareUserID > 0 {
				if id, st := h.ensureResolvedViewer(r.Context(), &viewers, &plextv.SharedUser{
					PlexID: d.shareUserID,
					Title:  d.name,
				}, d.name); id > 0 {
					vID = id
					status = st
				}
			}
			results[d.resultIdx] = finalize(d.serverID, d.acct, d.name, d.direction, vID, status, nil, true, d.shareErr)
		}
	}

	if sharedAny && h.deps.SyncPlexFriends != nil {
		if err := h.deps.SyncPlexFriends(r.Context()); err != nil {
			h.log.Warn("sync plex friends error after bulk import", "err", err)
		}
	}

	if err := h.deps.RefreshViewers(r.Context()); err != nil {
		h.log.Warn("refresh viewers error after bulk import", "err", err)
	}
	if results == nil {
		results = []bulkImportResultItem{}
	}
	writeJSON(w, http.StatusOK, results)
}
