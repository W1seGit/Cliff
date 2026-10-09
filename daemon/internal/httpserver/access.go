package httpserver

import (
	"net/http"

	"github.com/W1seGit/Cliff/daemon/internal/process"
	"github.com/W1seGit/Cliff/daemon/internal/store"
)

// authenticatedUser returns the signed-in user, or writes the 401 reply.
func (h apiHandler) authenticatedUser(w http.ResponseWriter, r *http.Request) (store.User, bool) {
	needsSetup, err := h.needsSetup(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return store.User{}, false
	}
	if needsSetup {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return store.User{}, false
	}
	user, ok, err := h.currentUser(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return store.User{}, false
	}
	if !ok {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return store.User{}, false
	}
	return user, true
}

// requireAdmin lets only admins through.
func (h apiHandler) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := h.authenticatedUser(w, r)
		if !ok {
			return
		}
		if user.Role != store.RoleAdmin {
			writeError(w, http.StatusForbidden, "Only an admin can do that")
			return
		}
		next(w, r)
	}
}

// requirePerm lets a user through when they hold perm on the server named by
// the {id} in the route. Admins always pass.
func (h apiHandler) requirePerm(perm string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := h.authenticatedUser(w, r)
		if !ok {
			return
		}
		allowed, err := h.store.Allowed(r.Context(), user, r.PathValue("id"), perm)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !allowed {
			writeError(w, http.StatusForbidden, "You do not have permission to do that on this server")
			return
		}
		next(w, r)
	}
}

// visibleServers drops the servers a member may not see.
func (h apiHandler) visibleServers(r *http.Request, user store.User, servers []store.Server) ([]store.Server, error) {
	if user.Role == store.RoleAdmin {
		return servers, nil
	}
	grants, err := h.store.Permissions(r.Context(), user.ID)
	if err != nil {
		return nil, err
	}
	visible := make([]store.Server, 0, len(servers))
	for _, server := range servers {
		if store.GrantsAllow(grants, server.ID, store.PermView) {
			visible = append(visible, server)
		}
	}
	return visible, nil
}

// visibleRuntime removes other servers' details from a runtime status.
func (h apiHandler) visibleRuntime(r *http.Request, user store.User, status process.Status) (process.Status, error) {
	if user.Role == store.RoleAdmin {
		return status, nil
	}
	grants, err := h.store.Permissions(r.Context(), user.ID)
	if err != nil {
		return status, err
	}
	allowed := map[string]process.Status{}
	for serverID, serverStatus := range status.Servers {
		if store.GrantsAllow(grants, serverID, store.PermView) {
			allowed[serverID] = serverStatus
		}
	}
	if status.RunningServerID != "" && !store.GrantsAllow(grants, status.RunningServerID, store.PermView) {
		status = process.Status{Lifecycle: process.LifecycleStopped}
	}
	status.Servers = allowed
	return status, nil
}
