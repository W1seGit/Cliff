package httpserver

import (
	"errors"
	"net/http"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/store"
	"github.com/W1seGit/Cliff/daemon/internal/totp"
)

// --- account management (admins) -------------------------------------------

func (h apiHandler) users(w http.ResponseWriter, r *http.Request) {
	accounts, err := h.store.ListAccounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": accounts, "permissions": store.AllPermissions})
}

type accountInput struct {
	Username    string              `json:"username"`
	Password    string              `json:"password"`
	Role        string              `json:"role"`
	Permissions map[string][]string `json:"permissions"`
}

func (h apiHandler) createUser(w http.ResponseWriter, r *http.Request) {
	var input accountInput
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid user body")
		return
	}
	user, err := h.store.CreateAccount(r.Context(), input.Username, input.Password, input.Role)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if user.Role != store.RoleAdmin {
		if err := h.store.SetPermissions(r.Context(), user.ID, input.Permissions); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	grants, _ := h.store.Permissions(r.Context(), user.ID)
	writeJSON(w, http.StatusOK, map[string]store.Account{"user": {User: user, Permissions: grants}})
}

func (h apiHandler) updateUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var raw struct {
		Role        *string             `json:"role"`
		Password    *string             `json:"password"`
		Permissions map[string][]string `json:"permissions"`
	}
	if err := readJSON(r, &raw); err != nil {
		writeError(w, http.StatusBadRequest, "invalid user body")
		return
	}
	if _, ok, err := h.store.GetUser(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	} else if !ok {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if raw.Role != nil {
		if err := h.store.SetAccountRole(r.Context(), id, *raw.Role); err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, store.ErrLastAdmin) {
				status = http.StatusConflict
			}
			writeError(w, status, err.Error())
			return
		}
	}
	if raw.Password != nil && *raw.Password != "" {
		if err := h.store.SetAccountPassword(r.Context(), id, *raw.Password); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if raw.Permissions != nil {
		if err := h.store.SetPermissions(r.Context(), id, raw.Permissions); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	user, _, _ := h.store.GetUser(r.Context(), id)
	grants, _ := h.store.Permissions(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]store.Account{"user": {User: user, Permissions: grants}})
}

func (h apiHandler) deleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if self, ok, _ := h.currentUser(r); ok && self.ID == id {
		writeError(w, http.StatusBadRequest, "You cannot delete your own account")
		return
	}
	if err := h.store.DeleteAccount(r.Context(), id); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, store.ErrLastAdmin) {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// resetUserTwoFactor lets an admin switch off two-factor for someone who lost
// their device and recovery codes.
func (h apiHandler) resetUserTwoFactor(w http.ResponseWriter, r *http.Request) {
	if err := h.store.DisableTOTP(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- two-factor sign-in ------------------------------------------------------

const twoFactorIssuer = "Cliff"

// checkSecondFactor verifies a TOTP code or a recovery code for a user who has
// two-factor enabled. A TOTP code can be used only once.
func (h apiHandler) checkSecondFactor(r *http.Request, user store.User, code string) (bool, error) {
	state, err := h.store.TOTP(r.Context(), user.ID)
	if err != nil {
		return false, err
	}
	if state.Enabled {
		if step, ok := totp.Verify(state.Secret, code, time.Now()); ok {
			return h.store.ClaimTOTPStep(r.Context(), user.ID, step)
		}
	}
	return h.store.UseRecoveryCode(r.Context(), user.ID, code)
}

// twoFactorSetup creates a secret for the signed-in user. It is not enforced
// at sign-in until twoFactorEnable confirms a working code.
func (h apiHandler) twoFactorSetup(w http.ResponseWriter, r *http.Request) {
	user, ok := h.authenticatedUser(w, r)
	if !ok {
		return
	}
	if user.TotpEnabled {
		writeError(w, http.StatusConflict, "Two-factor is already on. Turn it off first to set it up again.")
		return
	}
	// Asking for the password stops someone with only a stolen session from
	// enrolling their own authenticator on this account.
	var input struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if _, err := h.store.Authenticate(r.Context(), user.Username, input.Password); err != nil {
		writeError(w, http.StatusBadRequest, "Password is incorrect")
		return
	}
	secret, err := totp.GenerateSecret()
	if err == nil {
		err = h.store.BeginTOTP(r.Context(), user.ID, secret)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"secret": secret, "uri": totp.URI(secret, twoFactorIssuer, user.Username)})
}

func (h apiHandler) twoFactorEnable(w http.ResponseWriter, r *http.Request) {
	user, ok := h.authenticatedUser(w, r)
	if !ok {
		return
	}
	var input struct {
		Code string `json:"code"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	state, err := h.store.TOTP(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if state.Enabled || state.Secret == "" {
		writeError(w, http.StatusConflict, "Start the two-factor setup first")
		return
	}
	step, valid := totp.Verify(state.Secret, input.Code, time.Now())
	if !valid {
		writeError(w, http.StatusBadRequest, "That code is not right. Check the code in your authenticator app and try again.")
		return
	}
	codes, hashes, err := store.NewRecoveryCodes(8)
	if err == nil {
		err = h.store.EnableTOTP(r.Context(), user.ID, step, hashes)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "recoveryCodes": codes})
}

// twoFactorDisable needs the password and a current code, so a stolen session
// alone cannot remove the second factor.
func (h apiHandler) twoFactorDisable(w http.ResponseWriter, r *http.Request) {
	user, ok := h.authenticatedUser(w, r)
	if !ok {
		return
	}
	var input struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if _, err := h.store.Authenticate(r.Context(), user.Username, input.Password); err != nil {
		writeError(w, http.StatusBadRequest, "Password is incorrect")
		return
	}
	valid, err := h.checkSecondFactor(r, user, input.Code)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !valid {
		writeError(w, http.StatusBadRequest, "That code is not right")
		return
	}
	if err := h.store.DisableTOTP(r.Context(), user.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
