package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/Audi-dask/Overseer/internal/auth"
)

func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	acc, err := s.Store.GetAdminAccount(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"setup_required": acc.SetupRequired,
		"username":       acc.Username,
	})
}

func (s *Server) authSetup(w http.ResponseWriter, r *http.Request) {
	acc, err := s.Store.GetAdminAccount(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if !acc.SetupRequired {
		writeErr(w, 400, auth.ErrSetupDone.Error())
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, "invalid json")
		return
	}
	if err := s.Store.SetAdminPassword(r.Context(), in.Username, in.Password); err != nil {
		if errors.Is(err, auth.ErrWeakPassword) {
			writeErr(w, 400, err.Error())
			return
		}
		writeErr(w, 500, err.Error())
		return
	}
	username := strings.TrimSpace(in.Username)
	if username == "" {
		username = auth.DefaultUsername
	}
	token, exp, err := s.Auth.IssueToken(username)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	log.Printf("admin setup completed: user=%s", username)
	writeJSON(w, 200, map[string]any{
		"token":      token,
		"expires_at": exp,
		"username":   username,
	})
}

func (s *Server) authLogin(w http.ResponseWriter, r *http.Request) {
	acc, err := s.Store.GetAdminAccount(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if acc.SetupRequired {
		writeErr(w, 400, "请先完成管理员初始化")
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, "invalid json")
		return
	}
	username := strings.TrimSpace(in.Username)
	if username == "" {
		username = auth.DefaultUsername
	}
	if !strings.EqualFold(username, acc.Username) || !auth.CheckPassword(acc.PasswordHash, in.Password) {
		writeErr(w, 401, auth.ErrInvalidCredentials.Error())
		return
	}
	token, exp, err := s.Auth.IssueToken(acc.Username)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"token":      token,
		"expires_at": exp,
		"username":   acc.Username,
	})
}

func (s *Server) authMe(w http.ResponseWriter, r *http.Request) {
	claims, _ := r.Context().Value(ctxUserKey{}).(*auth.Claims)
	if claims == nil {
		writeErr(w, 401, "未登录")
		return
	}
	writeJSON(w, 200, map[string]any{"username": claims.Username})
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	claims, _ := r.Context().Value(ctxUserKey{}).(*auth.Claims)
	if claims == nil {
		writeErr(w, 401, "未登录")
		return
	}
	var in struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, "invalid json")
		return
	}
	acc, err := s.Store.GetAdminAccount(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if !auth.CheckPassword(acc.PasswordHash, in.OldPassword) {
		writeErr(w, 400, "当前密码不正确")
		return
	}
	if err := s.Store.SetAdminPassword(r.Context(), acc.Username, in.NewPassword); err != nil {
		if errors.Is(err, auth.ErrWeakPassword) {
			writeErr(w, 400, err.Error())
			return
		}
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}
