package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"sharepier-api/internal/config"
)

var ErrInvalidCredentials = errors.New("invalid credentials")
var ErrUnauthorized = errors.New("unauthorized")

type contextKey string

const userContextKey contextKey = "auth.user"

type Service struct {
	db                *sql.DB
	logger            *slog.Logger
	sessionCookieName string
	sessionTTL        time.Duration
	secureCookies     bool
}

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

func NewService(database *sql.DB, cfg config.Config, logger *slog.Logger) *Service {
	return &Service{
		db:                database,
		logger:            logger,
		sessionCookieName: cfg.SessionCookie,
		sessionTTL:        cfg.SessionTTL,
		secureCookies:     cfg.SecureCookies,
	}
}

func (s *Service) Login(ctx context.Context, username, password string) (User, string, time.Time, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return User{}, "", time.Time{}, ErrInvalidCredentials
	}

	var user User
	var passwordHash string
	err := s.db.QueryRowContext(
		ctx,
		`select id, username, password_hash, role from users where username = $1`,
		username,
	).Scan(&user.ID, &user.Username, &passwordHash, &user.Role)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, "", time.Time{}, ErrInvalidCredentials
		}
		return User{}, "", time.Time{}, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
		return User{}, "", time.Time{}, ErrInvalidCredentials
	}

	token, err := generateSessionToken()
	if err != nil {
		return User{}, "", time.Time{}, err
	}

	expiresAt := time.Now().UTC().Add(s.sessionTTL)
	if _, err := s.db.ExecContext(
		ctx,
		`insert into sessions (user_id, token_hash, expires_at) values ($1, $2, $3)`,
		user.ID,
		hashToken(token),
		expiresAt,
	); err != nil {
		return User{}, "", time.Time{}, err
	}

	return user, token, expiresAt, nil
}

func (s *Service) CurrentUser(ctx context.Context, token string) (User, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return User{}, ErrUnauthorized
	}

	var user User
	err := s.db.QueryRowContext(
		ctx,
		`select u.id, u.username, u.role
		from sessions s
		join users u on u.id = s.user_id
		where s.token_hash = $1 and s.expires_at > now()`,
		hashToken(token),
	).Scan(&user.ID, &user.Username, &user.Role)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrUnauthorized
		}
		return User{}, err
	}

	return user, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}

	if _, err := s.db.ExecContext(ctx, `delete from sessions where token_hash = $1`, hashToken(token)); err != nil {
		return err
	}

	return nil
}

func (s *Service) SessionTokenFromRequest(r *http.Request) string {
	cookie, err := r.Cookie(s.sessionCookieName)
	if err != nil {
		return ""
	}

	return cookie.Value
}

func (s *Service) SetSessionCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
	})
}

func (s *Service) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
}

func WithUser(r *http.Request, user User) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), userContextKey, user))
}

func UserFromContext(ctx context.Context) (User, bool) {
	user, ok := ctx.Value(userContextKey).(User)
	return user, ok
}

func generateSessionToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
