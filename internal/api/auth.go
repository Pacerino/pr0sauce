package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Pacerino/pr0sauce/internal/db"
	"github.com/jackc/pgx/v5"
)

const (
	headerInternalToken = "X-Internal-Token"
	headerSessionToken  = "X-Session-Token"

	sessionTTL     = 30 * 24 * time.Hour
	sessionRefresh = 15 * 24 * time.Hour

	// adminBootstrapLock serialises the "first admin" decision across API
	// instances via a transaction-scoped Postgres advisory lock.
	adminBootstrapLock = 0x70723073_61646d // "pr0s" "adm"

	maxAuthBody = 4 << 10
	maxNameLen  = 64
)

type ctxKey int

const sessionKey ctxKey = 0

// session is the resolved identity of an internal request; nil user means
// no (valid) session was presented.
type session struct {
	user *db.User
	hash []byte
}

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

type userView struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Role   string `json:"role"`
	Banned bool   `json:"banned,omitempty"`
}

func toView(u db.User) userView {
	return userView{ID: u.ID, Name: u.Name, Role: u.Role, Banned: u.BannedAt != nil}
}

// internal guards a handler with the shared X-Internal-Token and resolves the
// optional X-Session-Token into the request context. Every 401 carries the
// same body so a caller cannot tell which credential was wrong.
func (s *server) internal(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get(headerInternalToken)
		if s.internalToken == "" || subtle.ConstantTimeCompare([]byte(got), []byte(s.internalToken)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		sess := session{}
		if tok := r.Header.Get(headerSessionToken); tok != "" {
			hash := hashToken(tok)
			u, err := s.q.GetSessionUser(r.Context(), db.GetSessionUserParams{TokenHash: hash, Now: s.now()})
			switch {
			case err == nil:
				sess = session{user: &u, hash: hash}
			case errors.Is(err, pgx.ErrNoRows):
			default:
				s.internalError(w, r, err)
				return
			}
		}
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey, sess)))
	})
}

func sessionFrom(ctx context.Context) session {
	sess, _ := ctx.Value(sessionKey).(session)
	return sess
}

func isWrite(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// requireUser needs a valid session. Banned users may read but any write is 403.
func (s *server) requireUser(h func(http.ResponseWriter, *http.Request, db.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := sessionFrom(r.Context())
		if sess.user == nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if sess.user.BannedAt != nil && isWrite(r) {
			writeError(w, http.StatusForbidden, "Gesperrt")
			return
		}
		h(w, r, *sess.user)
	}
}

// requireMod needs moderator or admin. Banned moderators lose the role's powers entirely.
func (s *server) requireMod(h func(http.ResponseWriter, *http.Request, db.User)) http.HandlerFunc {
	return s.requireRole(h, "moderator", "admin")
}

// requireAdmin needs admin.
func (s *server) requireAdmin(h func(http.ResponseWriter, *http.Request, db.User)) http.HandlerFunc {
	return s.requireRole(h, "admin")
}

func (s *server) requireRole(h func(http.ResponseWriter, *http.Request, db.User), roles ...string) http.HandlerFunc {
	return s.requireUser(func(w http.ResponseWriter, r *http.Request, u db.User) {
		if u.BannedAt != nil {
			writeError(w, http.StatusForbidden, "Gesperrt")
			return
		}
		for _, role := range roles {
			if u.Role == role {
				h(w, r, u)
				return
			}
		}
		writeError(w, http.StatusForbidden, "Verboten")
	})
}

type createSessionReq struct {
	Pr0UserID int64  `json:"pr0UserId"`
	Name      string `json:"name"`
}

func (s *server) createSession(w http.ResponseWriter, r *http.Request) {
	var req createSessionReq
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAuthBody))
	if err := dec.Decode(&req); err != nil || req.Pr0UserID <= 0 ||
		strings.TrimSpace(req.Name) == "" || len(req.Name) > maxNameLen {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	qt := s.q.WithTx(tx)
	user, err := qt.UpsertUserLogin(ctx, db.UpsertUserLoginParams{Pr0UserID: req.Pr0UserID, Name: req.Name})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if s.adminName != "" && strings.EqualFold(req.Name, s.adminName) && user.Role != "admin" {
		// Serialise concurrent first logins: only one sees zero admins.
		if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", int64(adminBootstrapLock)); err != nil {
			s.internalError(w, r, err)
			return
		}
		n, err := qt.CountAdmins(ctx)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		if n == 0 {
			if err := qt.SetRole(ctx, db.SetRoleParams{ID: user.ID, Role: "admin"}); err != nil {
				s.internalError(w, r, err)
				return
			}
			user.Role = "admin"
		}
	}
	token, err := newToken()
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := qt.CreateSession(ctx, db.CreateSessionParams{
		TokenHash: hashToken(token), UserID: user.ID, ExpiresAt: s.now().Add(sessionTTL),
	}); err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "user": toView(user)})
}

func (s *server) deleteSession(w http.ResponseWriter, r *http.Request, _ db.User) {
	if err := s.q.DeleteSession(r.Context(), sessionFrom(r.Context()).hash); err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) me(w http.ResponseWriter, r *http.Request, u db.User) {
	// Sliding expiry: one conditional statement, so no read-modify-write race.
	now := s.now()
	_, err := s.pool.Exec(r.Context(),
		`update sessions set expires_at = $2 where token_hash = $1 and expires_at < $3`,
		sessionFrom(r.Context()).hash, now.Add(sessionTTL), now.Add(sessionRefresh))
	if err != nil {
		s.log.Warn("session touch failed", "err", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": u.ID, "name": u.Name, "role": u.Role, "banned": u.BannedAt != nil,
	})
}
