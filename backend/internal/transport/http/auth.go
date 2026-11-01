package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"uip-meeting-backend/internal/domain"
)

const cookieName = "uip_session"

type User = domain.User

type authContextKey struct{}

func withUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, authContextKey{}, u)
}

func userFrom(r *http.Request) (User, bool) {
	u, ok := r.Context().Value(authContextKey{}).(User)
	return u, ok
}

func jwtSecret() []byte {
	secret := os.Getenv("AUTH_SECRET")
	if len(secret) < 32 {
		return nil
	}
	return []byte(secret)
}

func issueJWT(sessionID, userID string, expires time.Time) (string, error) {
	secret := jwtSecret()
	if len(secret) == 0 {
		return "", errors.New("AUTH_SECRET must contain at least 32 characters")
	}
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	payload, _ := json.Marshal(map[string]any{"sid": sessionID, "sub": userID, "iat": time.Now().Unix(), "exp": expires.Unix()})
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func parseJWT(token string) (sid, sub string, err error) {
	parts := strings.Split(token, ".")
	secret := jwtSecret()
	if len(parts) != 3 || len(secret) == 0 {
		return "", "", errors.New("invalid token")
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	expected, e1 := base64.RawURLEncoding.DecodeString(parts[2])
	if e1 != nil || subtle.ConstantTimeCompare(expected, mac.Sum(nil)) != 1 {
		return "", "", errors.New("invalid token")
	}
	payload, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return "", "", e
	}
	var claims struct {
		SID string `json:"sid"`
		Sub string `json:"sub"`
		Exp int64  `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.SID == "" || claims.Sub == "" || claims.Exp <= time.Now().Unix() {
		return "", "", errors.New("invalid token")
	}
	return claims.SID, claims.Sub, nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(cookieName)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		sid, sub, err := parseJWT(cookie.Value)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		var user User
		var hash string
		var expires string
		var revoked *string
		err = s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT u.id,u.name,u.email,u.role,u.job_title,u.department_id,u.page_access,u.active,s.token_hash,s.expires_at,s.revoked_at FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.id=? AND s.user_id=?`), sid, sub).Scan(
			&user.ID, &user.Name, &user.Email, &user.Role, &user.JobTitle, &user.DepartmentID, newJSONList(&user.PageAccess), &user.Active, &hash, &expires, &revoked)
		if err != nil || !user.Active || revoked != nil || hash != tokenHash(cookie.Value) {
			next.ServeHTTP(w, r)
			return
		}
		exp, e := time.Parse(time.RFC3339Nano, expires)
		if e != nil || !exp.After(time.Now()) {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(withUser(r.Context(), user)))
	})
}

type jsonListScanner struct{ target *[]string }

func newJSONList(target *[]string) *jsonListScanner { return &jsonListScanner{target: target} }
func (s *jsonListScanner) Scan(src any) error {
	var raw []byte
	switch v := src.(type) {
	case string:
		raw = []byte(v)
	case []byte:
		raw = v
	case nil:
		*s.target = []string{}
		return nil
	default:
		return errors.New("unexpected list value")
	}
	if len(raw) == 0 {
		*s.target = []string{}
		return nil
	}
	return json.Unmarshal(raw, s.target)
}

func checkPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
