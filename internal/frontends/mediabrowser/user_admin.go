package mediabrowser

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/frontend/kit/route"
)

func mediaBrowserProblem(w http.ResponseWriter, r *http.Request, status int, detail string) {
	body := map[string]any{
		"type":   "about:blank",
		"title":  http.StatusText(status),
		"status": status,
	}
	if detail != "" {
		body["detail"] = detail
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_ = json.NewEncoder(w).Encode(body)
	}
}

func (i *instance) callerPrincipal(r *http.Request) (principal, bool) {
	p, ok := r.Context().Value(principalKey{}).(principal)
	return p, ok && p.viewer != nil
}

// requireOwnerCaller enforces owner/admin authorization without rejecting
// requests whose {userId} path parameter names another viewer.
func (i *instance) requireOwnerCaller(w http.ResponseWriter, r *http.Request) (*frontend.Viewer, string, bool) {
	p, ok := i.callerPrincipal(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return nil, "", false
	}
	if p.viewer.Delegated || !p.viewer.IsOwner {
		w.WriteHeader(http.StatusForbidden)
		return nil, "", false
	}
	return p.viewer, p.token, true
}

func (i *instance) managedUserDTO(r *http.Request, mv *frontend.ManagedViewer, callerToken string) map[string]any {
	u := mv.User
	dto := i.userDTO(r, &u)
	dto["HasPassword"] = mv.HasLocalPassword
	if i.protocol == "jellyfin" {
		dto["HasConfiguredPassword"] = mv.HasLocalPassword
	}
	if policy, ok := dto["Policy"].(map[string]any); ok {
		policy["IsAdministrator"] = u.IsOwner && !u.Delegated
		policy["IsDisabled"] = !mv.Active || !mv.OwnerEnabled
		if u.Hidden {
			policy["IsHidden"] = true
			if i.protocol == "emby" {
				policy["IsHiddenRemotely"] = true
			}
		}
		if len(u.AllowedLibraries) == 0 {
			policy["EnableAllFolders"] = true
			policy["EnabledFolders"] = []string{}
		} else {
			folders := make([]string, 0, len(u.AllowedLibraries))
			for _, libID := range u.AllowedLibraries {
				if guid, err := ids.GUID(ids.View, int64(libID)); err == nil {
					folders = append(folders, guid)
				}
			}
			policy["EnableAllFolders"] = false
			policy["EnabledFolders"] = folders
		}
	}
	if callerToken != "" && i.host.Identity != nil {
		if !frontend.IsAPIKeyCredential(callerToken) {
			i.attachViewerAvatarTag(r, dto, &u, callerToken)
		}
		if times, err := i.host.Identity.ViewerTimes(i.identityContext(r), callerToken, u.ID); err == nil {
			if !times.LastLogin.IsZero() {
				dto["LastLoginDate"] = wireTime(times.LastLogin)
			}
			if !times.LastActivity.IsZero() {
				dto["LastActivityDate"] = wireTime(times.LastActivity)
			}
		}
	}
	if _, hasAct := dto["LastActivityDate"]; !hasAct && !mv.LastSeen.IsZero() {
		dto["LastActivityDate"] = wireTime(mv.LastSeen)
	}
	return dto
}

func (i *instance) getUserByID(w http.ResponseWriter, r *http.Request) {
	p, ok := i.callerPrincipal(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	rawID := r.PathValue("userId")
	targetID, err := ids.ParseGUID(ids.User, rawID)
	isOwner := !p.viewer.Delegated && p.viewer.IsOwner
	if err != nil {
		if isOwner {
			mediaBrowserProblem(w, r, http.StatusBadRequest, "Invalid user id.")
		} else {
			http.Error(w, "Forbidden", http.StatusForbidden)
		}
		return
	}
	if targetID == p.viewer.ID {
		jsonReply(w, i.userDTOWithTimes(r, p.viewer, p.token))
		return
	}
	if !isOwner {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if i.host.ViewerAdmin == nil {
		mediaBrowserProblem(w, r, http.StatusNotFound, "User not found.")
		return
	}
	mv, err := i.host.ViewerAdmin.GetViewer(r.Context(), targetID)
	if err != nil {
		if errors.Is(err, frontend.ErrNotFound) {
			mediaBrowserProblem(w, r, http.StatusNotFound, "User not found.")
			return
		}
		mediaBrowserProblem(w, r, http.StatusInternalServerError, "Could not load user.")
		return
	}
	jsonReply(w, i.managedUserDTO(r, mv, p.token))
}

func optionalQueryBool(r *http.Request, name string) (bool, bool) {
	vals := route.Query(r.URL.Query(), name)
	if len(vals) != 1 {
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(vals[0])) {
	case "true", "1":
		return true, true
	case "false", "0":
		return false, true
	default:
		return false, false
	}
}

func (i *instance) filteredManagedUsers(r *http.Request, callerToken string) ([]any, error) {
	if i.host.ViewerAdmin == nil {
		return nil, errors.New("viewer admin unavailable")
	}
	viewers, err := i.host.ViewerAdmin.ListViewers(r.Context())
	if err != nil {
		return nil, err
	}
	wantHidden, filterHidden := optionalQueryBool(r, "isHidden")
	wantDisabled, filterDisabled := optionalQueryBool(r, "isDisabled")
	out := make([]any, 0, len(viewers))
	for idx := range viewers {
		dto := i.managedUserDTO(r, &viewers[idx], callerToken)
		policy, _ := dto["Policy"].(map[string]any)
		if filterHidden {
			isHidden, _ := policy["IsHidden"].(bool)
			if isHidden != wantHidden {
				continue
			}
		}
		if filterDisabled {
			isDisabled, _ := policy["IsDisabled"].(bool)
			if isDisabled != wantDisabled {
				continue
			}
		}
		out = append(out, dto)
	}
	return out, nil
}

func (i *instance) listUsers(w http.ResponseWriter, r *http.Request) {
	_, token, ok := i.requireOwnerCaller(w, r)
	if !ok {
		return
	}
	if i.host.ViewerAdmin == nil {
		mediaBrowserProblem(w, r, http.StatusServiceUnavailable, "User administration is unavailable.")
		return
	}
	users, err := i.filteredManagedUsers(r, token)
	if err != nil {
		mediaBrowserProblem(w, r, http.StatusInternalServerError, "Could not list users.")
		return
	}
	jsonReply(w, users)
}

func (i *instance) queryUsers(w http.ResponseWriter, r *http.Request) {
	_, token, ok := i.requireOwnerCaller(w, r)
	if !ok {
		return
	}
	if i.host.ViewerAdmin == nil {
		mediaBrowserProblem(w, r, http.StatusServiceUnavailable, "User administration is unavailable.")
		return
	}
	users, err := i.filteredManagedUsers(r, token)
	if err != nil {
		mediaBrowserProblem(w, r, http.StatusInternalServerError, "Could not list users.")
		return
	}
	total := len(users)
	startIndex := max(queryInt(r, "StartIndex", 0), 0)
	limitValues := route.Query(r.URL.Query(), "Limit")
	hasLimit := len(limitValues) > 0
	limit := max(queryInt(r, "Limit", 0), 0)
	if startIndex >= len(users) {
		users = []any{}
	} else {
		users = users[startIndex:]
		if hasLimit && limit < len(users) {
			users = users[:limit]
		}
	}
	jsonReply(w, i.queryResult(users, total, startIndex))
}

func (i *instance) createUser(w http.ResponseWriter, r *http.Request) {
	_, token, ok := i.requireOwnerCaller(w, r)
	if !ok {
		return
	}
	if i.host.ViewerAdmin == nil {
		mediaBrowserProblem(w, r, http.StatusServiceUnavailable, "User administration is unavailable.")
		return
	}
	var body struct {
		Name     string `json:"Name"`
		Password string `json:"Password"`
	}
	if err := decode(r, &body); err != nil {
		mediaBrowserProblem(w, r, http.StatusBadRequest, "Invalid request body.")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		mediaBrowserProblem(w, r, http.StatusBadRequest, "Name is required.")
		return
	}
	mv, err := i.host.ViewerAdmin.CreateLocalViewer(r.Context(), name, body.Password)
	if err != nil {
		switch {
		case errors.Is(err, frontend.ErrPasswordTooShort):
			mediaBrowserProblem(w, r, http.StatusBadRequest, err.Error())
		case errors.Is(err, frontend.ErrViewerExists):
			mediaBrowserProblem(w, r, http.StatusBadRequest, "A user with that name already exists.")
		default:
			mediaBrowserProblem(w, r, http.StatusBadRequest, "Could not create user.")
		}
		return
	}
	jsonReply(w, i.managedUserDTO(r, mv, token))
}

type policyField struct {
	name string
	val  any
}

func (i *instance) updateUserPolicy(w http.ResponseWriter, r *http.Request) {
	_, token, ok := i.requireOwnerCaller(w, r)
	if !ok {
		return
	}
	if i.host.ViewerAdmin == nil {
		mediaBrowserProblem(w, r, http.StatusServiceUnavailable, "User administration is unavailable.")
		return
	}
	targetID, err := ids.ParseGUID(ids.User, r.PathValue("userId"))
	if err != nil {
		mediaBrowserProblem(w, r, http.StatusBadRequest, "Invalid user id.")
		return
	}
	target, err := i.host.ViewerAdmin.GetViewer(r.Context(), targetID)
	if err != nil {
		if errors.Is(err, frontend.ErrNotFound) {
			mediaBrowserProblem(w, r, http.StatusNotFound, "User not found.")
			return
		}
		mediaBrowserProblem(w, r, http.StatusInternalServerError, "Could not load user.")
		return
	}
	var raw map[string]any
	if err := decode(r, &raw); err != nil || raw == nil {
		mediaBrowserProblem(w, r, http.StatusBadRequest, "Invalid policy payload.")
		return
	}
	fields := make(map[string]policyField, len(raw))
	for k, v := range raw {
		fields[strings.ToLower(k)] = policyField{name: k, val: v}
	}

	// Refuse privilege elevation for non-owners and demotion/disabling of the owner.
	if f, ok := fields["isadministrator"]; ok {
		isAdmin, valid := f.val.(bool)
		if !valid {
			mediaBrowserProblem(w, r, http.StatusBadRequest, "IsAdministrator must be a boolean.")
			return
		}
		if isAdmin && !target.User.IsOwner {
			mediaBrowserProblem(w, r, http.StatusForbidden, "Only the server owner can be an administrator.")
			return
		}
		if !isAdmin && target.User.IsOwner {
			mediaBrowserProblem(w, r, http.StatusForbidden, "The server owner cannot be demoted from administrator.")
			return
		}
	}

	var update frontend.ViewerPolicyUpdate
	if f, ok := fields["isdisabled"]; ok {
		disabled, valid := f.val.(bool)
		if !valid {
			mediaBrowserProblem(w, r, http.StatusBadRequest, "IsDisabled must be a boolean.")
			return
		}
		if disabled && target.User.IsOwner {
			mediaBrowserProblem(w, r, http.StatusForbidden, "The server owner cannot be disabled.")
			return
		}
		if !target.User.IsOwner {
			update.Paused = &disabled
		}
	}

	if f, ok := fields["ishidden"]; ok {
		hidden, valid := f.val.(bool)
		if !valid {
			mediaBrowserProblem(w, r, http.StatusBadRequest, "IsHidden must be a boolean.")
			return
		}
		update.Hidden = &hidden
	}

	enableAllField, hasEnableAll := fields["enableallfolders"]
	enabledFoldersField, hasEnabledFolders := fields["enabledfolders"]
	if hasEnableAll || hasEnabledFolders {
		enableAll := false
		if hasEnableAll {
			v, valid := enableAllField.val.(bool)
			if !valid {
				mediaBrowserProblem(w, r, http.StatusBadRequest, "EnableAllFolders must be a boolean.")
				return
			}
			enableAll = v
		}
		if enableAll {
			all := []frontend.LibraryID{}
			update.AllowedLibraries = &all
		} else if hasEnabledFolders && enabledFoldersField.val != nil {
			rawSlice, valid := enabledFoldersField.val.([]any)
			if !valid {
				mediaBrowserProblem(w, r, http.StatusBadRequest, "EnabledFolders must be an array of folder ids.")
				return
			}
			libs := make([]frontend.LibraryID, 0, len(rawSlice))
			for _, elem := range rawSlice {
				s, ok := elem.(string)
				if !ok {
					mediaBrowserProblem(w, r, http.StatusBadRequest, "EnabledFolders entries must be strings.")
					return
				}
				viewNum, err := ids.ParseGUID(ids.View, strings.TrimSpace(s))
				if err != nil || viewNum <= 0 || ids.PersonalView(viewNum) {
					mediaBrowserProblem(w, r, http.StatusBadRequest, "Invalid library folder id in EnabledFolders.")
					return
				}
				libID := frontend.LibraryID(viewNum)
				if !slices.Contains(libs, libID) {
					libs = append(libs, libID)
				}
			}
			if len(libs) == 0 {
				// Empty AllowedLibraries means all libraries in Ether; keep access
				// intact and log once rather than widening access to all libraries.
				i.warnUnsupportedPolicyField("EnabledFolders(empty)")
			} else {
				update.AllowedLibraries = &libs
			}
		} else if hasEnableAll && !enableAll {
			i.warnUnsupportedPolicyField("EnableAllFolders(false without EnabledFolders)")
		}
	}

	// Ignore any field Ether cannot honour, logging each unsupported field once
	// when the caller requests a value different from the viewer's current policy.
	currentPolicy, _ := i.managedUserDTO(r, target, token)["Policy"].(map[string]any)
	currentLower := make(map[string]any, len(currentPolicy))
	for k, v := range currentPolicy {
		currentLower[strings.ToLower(k)] = v
	}
	for lower, f := range fields {
		switch lower {
		case "isadministrator", "isdisabled", "ishidden", "ishiddenremotely", "enableallfolders", "enabledfolders":
			continue
		}
		if cur, ok := currentLower[lower]; ok && policyValuesEqual(cur, f.val) {
			continue
		}
		i.warnUnsupportedPolicyField(f.name)
	}

	if err := i.host.ViewerAdmin.UpdateViewerPolicy(r.Context(), targetID, update); err != nil {
		switch {
		case errors.Is(err, frontend.ErrNotFound):
			mediaBrowserProblem(w, r, http.StatusNotFound, "User not found.")
		case errors.Is(err, frontend.ErrViewerProtected):
			mediaBrowserProblem(w, r, http.StatusForbidden, "Viewer cannot be modified this way.")
		default:
			mediaBrowserProblem(w, r, http.StatusInternalServerError, "Could not update user policy.")
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (i *instance) warnUnsupportedPolicyField(field string) {
	if i.host.Log == nil || field == "" {
		return
	}
	key := strings.ToLower(field)
	if _, loaded := i.unsupportedPolicyLogged.LoadOrStore(key, struct{}{}); !loaded {
		i.host.Log.Warn("ignoring unsupported MediaBrowser user policy field", "frontend", i.protocol, "field", field)
	}
}

func policyValuesEqual(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	ba, ea := json.Marshal(a)
	bb, eb := json.Marshal(b)
	if ea != nil || eb != nil {
		return reflect.DeepEqual(a, b)
	}
	return string(ba) == string(bb)
}

func (i *instance) updateUserPassword(w http.ResponseWriter, r *http.Request) {
	p, ok := i.callerPrincipal(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if i.host.ViewerAdmin == nil {
		mediaBrowserProblem(w, r, http.StatusServiceUnavailable, "User administration is unavailable.")
		return
	}
	rawID := r.PathValue("userId")
	if rawID == "" {
		if qs := route.Query(r.URL.Query(), "userId"); len(qs) == 1 {
			rawID = qs[0]
		} else if len(qs) > 1 {
			mediaBrowserProblem(w, r, http.StatusBadRequest, "Ambiguous user id.")
			return
		}
	}
	targetID := p.viewer.ID
	if rawID != "" {
		parsed, err := ids.ParseGUID(ids.User, rawID)
		if err != nil {
			mediaBrowserProblem(w, r, http.StatusBadRequest, "Invalid user id.")
			return
		}
		targetID = parsed
	}
	var body struct {
		CurrentPw       string `json:"CurrentPw"`
		CurrentPassword string `json:"CurrentPassword"`
		NewPw           string `json:"NewPw"`
		ResetPassword   bool   `json:"ResetPassword"`
	}
	if err := decode(r, &body); err != nil {
		mediaBrowserProblem(w, r, http.StatusBadRequest, "Invalid password payload.")
		return
	}
	currentPw := body.CurrentPw
	if currentPw == "" {
		currentPw = body.CurrentPassword
	}
	isOwner := !p.viewer.Delegated && p.viewer.IsOwner
	if !isOwner {
		if targetID != p.viewer.ID || body.ResetPassword {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if currentPw == "" || i.host.ViewerAdmin.VerifyViewerPassword(r.Context(), targetID, currentPw) != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}
	if !body.ResetPassword && body.NewPw == "" {
		mediaBrowserProblem(w, r, http.StatusBadRequest, "NewPw is required.")
		return
	}
	if err := i.host.ViewerAdmin.SetViewerPassword(r.Context(), targetID, body.NewPw, body.ResetPassword); err != nil {
		switch {
		case errors.Is(err, frontend.ErrNotFound):
			mediaBrowserProblem(w, r, http.StatusNotFound, "User not found.")
		case errors.Is(err, frontend.ErrPasswordTooShort):
			mediaBrowserProblem(w, r, http.StatusBadRequest, err.Error())
		case errors.Is(err, frontend.ErrViewerProtected):
			mediaBrowserProblem(w, r, http.StatusForbidden, "This viewer's password cannot be cleared.")
		default:
			mediaBrowserProblem(w, r, http.StatusInternalServerError, "Could not update password.")
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (i *instance) deleteUser(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := i.requireOwnerCaller(w, r); !ok {
		return
	}
	if i.host.ViewerAdmin == nil {
		mediaBrowserProblem(w, r, http.StatusServiceUnavailable, "User administration is unavailable.")
		return
	}
	targetID, err := ids.ParseGUID(ids.User, r.PathValue("userId"))
	if err != nil {
		mediaBrowserProblem(w, r, http.StatusBadRequest, "Invalid user id.")
		return
	}
	if err := i.host.ViewerAdmin.RemoveViewer(r.Context(), targetID); err != nil {
		switch {
		case errors.Is(err, frontend.ErrNotFound):
			mediaBrowserProblem(w, r, http.StatusNotFound, "User not found.")
		case errors.Is(err, frontend.ErrViewerProtected):
			mediaBrowserProblem(w, r, http.StatusForbidden, "The server owner cannot be deleted.")
		case errors.Is(err, frontend.ErrViewerPlexManaged):
			mediaBrowserProblem(w, r, http.StatusConflict, "Plex-managed viewers must be unshared on plex.tv.")
		default:
			mediaBrowserProblem(w, r, http.StatusInternalServerError, "Could not delete user.")
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (i *instance) mediaFolders(w http.ResponseWriter, r *http.Request) {
	viewer, _, ok := i.requireOwnerCaller(w, r)
	if !ok {
		return
	}
	if i.host.Experience == nil {
		mediaBrowserProblem(w, r, http.StatusServiceUnavailable, "Libraries are unavailable.")
		return
	}
	views, err := i.host.Experience.Views(r.Context(), viewer)
	if err != nil {
		mediaBrowserProblem(w, r, http.StatusServiceUnavailable, "Libraries are unavailable.")
		return
	}
	items := make([]any, 0, len(views))
	for _, view := range views {
		if folder, ok := i.collectionFolder(r, viewer, view); ok {
			items = append(items, folder)
		}
	}
	jsonReply(w, i.queryResult(items, len(items), 0))
}
