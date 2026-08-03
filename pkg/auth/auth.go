package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/lattice-ai/lattice/pkg/plugins"
	"golang.org/x/time/rate"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrRateLimited  = errors.New("rate limited")
)

// APIKeyAuth validates static API keys and optional JWT bearer tokens.
type APIKeyAuth struct {
	keys      map[string]*plugins.Identity
	jwtSecret []byte
	limiters  sync.Map // subject -> *rate.Limiter
	mu        sync.RWMutex
	audit     []AuditEntry
}

// AuditEntry records authz events.
type AuditEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Subject   string    `json:"subject"`
	Action    string    `json:"action"`
	Resource  string    `json:"resource"`
	Allowed   bool      `json:"allowed"`
	IP        string    `json:"ip,omitempty"`
}

// NewAPIKeyAuth builds auth from key list. Format: key or key:role1|role2:tenant:rpm
func NewAPIKeyAuth(keys []string, jwtSecret string) *APIKeyAuth {
	a := &APIKeyAuth{
		keys:      map[string]*plugins.Identity{},
		jwtSecret: []byte(jwtSecret),
	}
	for i, raw := range keys {
		parts := strings.Split(raw, ":")
		key := parts[0]
		id := &plugins.Identity{
			Subject:  fmt.Sprintf("key-%d", i+1),
			Tenant:   "default",
			Roles:    []string{"user"},
			QuotaRPM: 60,
		}
		if len(parts) > 1 && parts[1] != "" {
			id.Roles = strings.Split(parts[1], "|")
		}
		if len(parts) > 2 && parts[2] != "" {
			id.Tenant = parts[2]
		}
		if len(parts) > 3 {
			fmt.Sscanf(parts[3], "%d", &id.QuotaRPM)
		}
		a.keys[key] = id
		a.keys[hashKey(key)] = id
	}
	return a
}

func (a *APIKeyAuth) Name() string { return "api_key_jwt" }

func (a *APIKeyAuth) Authenticate(ctx context.Context, apiKey, bearer string) (*plugins.Identity, error) {
	if apiKey != "" {
		if id, ok := a.keys[apiKey]; ok {
			return id, nil
		}
		if id, ok := a.keys[hashKey(apiKey)]; ok {
			return id, nil
		}
	}
	if bearer != "" {
		return a.parseJWT(bearer)
	}
	return nil, ErrUnauthorized
}

func (a *APIKeyAuth) Authorize(ctx context.Context, id *plugins.Identity, action string) error {
	if id == nil {
		return ErrUnauthorized
	}
	for _, r := range id.Roles {
		if r == "admin" || r == "user" {
			a.record(id.Subject, action, "*", true)
			return nil
		}
	}
	a.record(id.Subject, action, "*", false)
	return ErrForbidden
}

// AllowRate enforces per-subject RPM.
func (a *APIKeyAuth) AllowRate(id *plugins.Identity) error {
	if id == nil {
		return ErrUnauthorized
	}
	rpm := id.QuotaRPM
	if rpm <= 0 {
		rpm = 60
	}
	v, _ := a.limiters.LoadOrStore(id.Subject, rate.NewLimiter(rate.Every(time.Minute/time.Duration(rpm)), rpm))
	lim := v.(*rate.Limiter)
	if !lim.Allow() {
		return ErrRateLimited
	}
	return nil
}

func (a *APIKeyAuth) parseJWT(tokenStr string) (*plugins.Identity, error) {
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return a.jwtSecret, nil
	})
	if err != nil || !token.Valid {
		return nil, ErrUnauthorized
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, ErrUnauthorized
	}
	id := &plugins.Identity{Roles: []string{"user"}, Tenant: "default", QuotaRPM: 120}
	if s, ok := claims["sub"].(string); ok {
		id.Subject = s
	}
	if t, ok := claims["tenant"].(string); ok {
		id.Tenant = t
	}
	if roles, ok := claims["roles"].([]interface{}); ok {
		id.Roles = nil
		for _, r := range roles {
			if rs, ok := r.(string); ok {
				id.Roles = append(id.Roles, rs)
			}
		}
	}
	return id, nil
}

// IssueJWT creates a signed token for demos / admin tooling.
func (a *APIKeyAuth) IssueJWT(subject, tenant string, roles []string, ttl time.Duration) (string, error) {
	claims := jwt.MapClaims{
		"sub":    subject,
		"tenant": tenant,
		"roles":  roles,
		"exp":    time.Now().Add(ttl).Unix(),
		"iat":    time.Now().Unix(),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString(a.jwtSecret)
}

func (a *APIKeyAuth) record(subject, action, resource string, allowed bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.audit = append(a.audit, AuditEntry{
		Timestamp: time.Now().UTC(),
		Subject:   subject,
		Action:    action,
		Resource:  resource,
		Allowed:   allowed,
	})
	if len(a.audit) > 1000 {
		a.audit = a.audit[len(a.audit)-1000:]
	}
}

// AuditLog returns recent entries.
func (a *APIKeyAuth) AuditLog() []AuditEntry {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]AuditEntry, len(a.audit))
	copy(out, a.audit)
	return out
}

// Middleware wraps HTTP handlers with auth + rate limiting.
func Middleware(a *APIKeyAuth) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" || strings.HasPrefix(r.URL.Path, "/metrics") {
				next.ServeHTTP(w, r)
				return
			}
			apiKey := r.Header.Get("X-API-Key")
			bearer := ""
			if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
				bearer = strings.TrimPrefix(h, "Bearer ")
			}
			id, err := a.Authenticate(r.Context(), apiKey, bearer)
			if err != nil {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			if err := a.AllowRate(id); err != nil {
				http.Error(w, `{"error":"rate_limited"}`, http.StatusTooManyRequests)
				return
			}
			ctx := context.WithValue(r.Context(), ctxKey{}, id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

type ctxKey struct{}

// IdentityFrom extracts the authenticated identity.
func IdentityFrom(ctx context.Context) *plugins.Identity {
	id, _ := ctx.Value(ctxKey{}).(*plugins.Identity)
	return id
}

func hashKey(k string) string {
	sum := sha256.Sum256([]byte(k))
	return hex.EncodeToString(sum[:])
}
