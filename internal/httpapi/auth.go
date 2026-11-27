package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"mihomo-manager/internal/manager"
	"mihomo-manager/internal/model"
)

const sessionCookieName = "graydeck_session"

func (r *Router) handleAuthStatus(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	username, authenticated := r.currentUser(req)
	writeJSON(w, http.StatusOK, model.AuthStatus{
		Enabled:       r.service.AuthEnabled(),
		Authenticated: authenticated,
		Username:      username,
	})
}

func (r *Router) handleAuthLogin(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var payload struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	token, err := r.service.CreateSession(payload.Username, payload.Password)
	if errors.Is(err, manager.ErrInvalidCredentials) {
		http.Error(w, "用户名或密码错误", http.StatusUnauthorized)
		return
	}

	if err != nil {
		http.Error(w, "保存登录会话失败", http.StatusServiceUnavailable)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   req.TLS != nil,
		MaxAge:   int(manager.SessionLifetime.Seconds()),
	})

	writeJSON(w, http.StatusOK, model.AuthStatus{
		Enabled:       r.service.AuthEnabled(),
		Authenticated: true,
		Username:      strings.TrimSpace(payload.Username),
	})
}

func (r *Router) handleAuthLogout(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if err := r.service.DeleteSession(r.sessionToken(req)); err != nil {
		http.Error(w, "注销会话失败", http.StatusServiceUnavailable)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   req.TLS != nil,
		MaxAge:   -1,
	})

	writeJSON(w, http.StatusOK, model.AuthStatus{
		Enabled:       r.service.AuthEnabled(),
		Authenticated: false,
	})
}

func (r *Router) requireAuthAPI(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if r.isAuthenticated(req) {
			next(w, req)
			return
		}

		http.Error(w, "未登录", http.StatusUnauthorized)
	}
}

func (r *Router) isAuthenticated(req *http.Request) bool {
	if !r.service.AuthEnabled() {
		return true
	}

	_, ok := r.currentUser(req)
	return ok
}

func (r *Router) currentUser(req *http.Request) (string, bool) {
	if !r.service.AuthEnabled() {
		return "", true
	}

	return r.service.SessionUser(r.sessionToken(req))
}

func (r *Router) sessionToken(req *http.Request) string {
	cookie, err := req.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(cookie.Value)
}
