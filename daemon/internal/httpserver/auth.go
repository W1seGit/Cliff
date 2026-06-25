package httpserver

import (
	"net/http"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

const sessionCookieName = "mc_dash_session"

type authPayload struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (h apiHandler) authMe(w http.ResponseWriter, r *http.Request) {
	user, ok, err := h.currentUser(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	needsSetup, err := h.needsSetup(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"user": nil, "needsSetup": needsSetup})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user, "needsSetup": needsSetup})
}

func (h apiHandler) authSetup(w http.ResponseWriter, r *http.Request) {
	var input authPayload
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid setup body")
		return
	}
	user, err := h.store.CreateUser(r.Context(), input.Username, input.Password)
	if err != nil {
		status := http.StatusBadRequest
		if err.Error() == "Initial user already exists" {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]any{"error": err.Error(), "needsSetup": status != http.StatusConflict})
		return
	}
	if err := h.writeSession(w, r, user); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (h apiHandler) authLogin(w http.ResponseWriter, r *http.Request) {
	var input authPayload
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid login body")
		return
	}
	user, err := h.store.Authenticate(r.Context(), input.Username, input.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if err := h.writeSession(w, r, user); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (h apiHandler) authLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err == nil {
		if err := h.store.DeleteSession(r.Context(), cookie.Value); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:    sessionCookieName,
		Value:   "",
		Expires: time.Unix(0, 0),
		Path:    "/",
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h apiHandler) authAccount(w http.ResponseWriter, r *http.Request) {
	user, ok, err := h.currentUser(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	var input struct {
		Username        string `json:"username"`
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid account body")
		return
	}
	nextUser, err := h.store.UpdateUserAccount(r.Context(), user.ID, input.Username, input.CurrentPassword, input.NewPassword)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]store.User{"user": nextUser})
}

func (h apiHandler) writeSession(w http.ResponseWriter, r *http.Request, user store.User) error {
	sessionID, expires, err := h.store.CreateSession(r.Context(), user.ID)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionID,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   false,
		Expires:  expires,
		Path:     "/",
	})
	return nil
}

func (h apiHandler) currentUser(r *http.Request) (store.User, bool, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return store.User{}, false, nil
	}
	return h.store.UserBySession(r.Context(), cookie.Value)
}

func (h apiHandler) needsSetup(r *http.Request) (bool, error) {
	hasUser, err := h.store.HasUser(r.Context())
	if err != nil {
		return false, err
	}
	return !hasUser, nil
}

func (h apiHandler) requireUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		needsSetup, err := h.needsSetup(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if needsSetup {
			writeError(w, http.StatusUnauthorized, "Authentication required")
			return
		}
		if _, ok, err := h.currentUser(r); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		} else if !ok {
			writeError(w, http.StatusUnauthorized, "Authentication required")
			return
		}
		next(w, r)
	}
}
