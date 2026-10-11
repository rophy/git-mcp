package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/rophy/git-mcp/internal/config"
)

type Identity struct {
	Service string
	Issuer  string
	Subject string
}

// kidRefetchInterval limits how often an unknown kid can force a JWKS fetch.
const kidRefetchInterval = time.Minute

type issuerEntry struct {
	name    string
	iss     string
	jwksURI string

	mu          sync.Mutex
	lastRefetch time.Time
}

// allowRefetch reports whether an unknown kid may force a JWKS fetch now.
func (e *issuerEntry) allowRefetch() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if time.Since(e.lastRefetch) < kidRefetchInterval {
		return false
	}
	e.lastRefetch = time.Now()
	return true
}

type Authenticator struct {
	audience string
	issuers  map[string]*issuerEntry // keyed by iss claim value
	services map[string]config.ServiceConfig
	cache    *jwk.Cache
}

// NewAuthenticator creates an Authenticator. The JWKS cache stops refreshing when ctx is done.
func NewAuthenticator(ctx context.Context, cfg *config.Config) (*Authenticator, error) {
	cache := jwk.NewCache(ctx)
	issuers := make(map[string]*issuerEntry, len(cfg.Auth.Issuers))

	for _, ic := range cfg.Auth.Issuers {
		if err := cache.Register(ic.JWKSURI, jwk.WithMinRefreshInterval(5*time.Minute)); err != nil {
			return nil, fmt.Errorf("register JWKS for issuer %q: %w", ic.Name, err)
		}
		issuers[ic.Iss] = &issuerEntry{name: ic.Name, iss: ic.Iss, jwksURI: ic.JWKSURI}
	}

	return &Authenticator{
		audience: cfg.Auth.Audience,
		issuers:  issuers,
		services: cfg.Services,
		cache:    cache,
	}, nil
}

func (a *Authenticator) Authenticate(tokenString string) (*Identity, error) {
	parser := gojwt.NewParser(gojwt.WithoutClaimsValidation())
	unverified, _, err := parser.ParseUnverified(tokenString, gojwt.MapClaims{})
	if err != nil {
		return nil, fmt.Errorf("parse token: %w", err)
	}

	claims, ok := unverified.Claims.(gojwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("unexpected claims type")
	}

	iss, _ := claims["iss"].(string)
	if iss == "" {
		return nil, fmt.Errorf("missing iss claim")
	}

	issEntry, ok := a.issuers[iss]
	if !ok {
		return nil, fmt.Errorf("unknown issuer: %s", iss)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	keySet, err := a.cache.Get(ctx, issEntry.jwksURI)
	if err != nil {
		return nil, fmt.Errorf("fetch JWKS for %s: %w", issEntry.name, err)
	}

	verified, err := gojwt.Parse(tokenString, func(token *gojwt.Token) (any, error) {
		kid, _ := token.Header["kid"].(string)
		if kid == "" {
			return nil, fmt.Errorf("missing kid header")
		}
		key, found := keySet.LookupKeyID(kid)
		if !found && issEntry.allowRefetch() {
			// The issuer may have rotated its keys.
			if refreshed, err := a.cache.Refresh(ctx, issEntry.jwksURI); err == nil {
				key, found = refreshed.LookupKeyID(kid)
			}
		}
		if !found {
			return nil, fmt.Errorf("key %s not found in JWKS", kid)
		}
		var rawKey any
		if err := key.Raw(&rawKey); err != nil {
			return nil, fmt.Errorf("extract key: %w", err)
		}
		return rawKey, nil
	},
		gojwt.WithAudience(a.audience),
		gojwt.WithExpirationRequired(),
		gojwt.WithValidMethods([]string{"RS256", "RS384", "RS512", "ES256", "ES384", "ES512", "PS256", "PS384", "PS512"}),
	)
	if err != nil {
		if errors.Is(err, gojwt.ErrTokenExpired) {
			return nil, fmt.Errorf("token expired: %w", err)
		}
		return nil, fmt.Errorf("validate token: %w", err)
	}

	verifiedClaims, ok := verified.Claims.(gojwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("unexpected claims type after verification")
	}

	sub, _ := verifiedClaims["sub"].(string)
	if sub == "" {
		return nil, fmt.Errorf("missing sub claim")
	}

	serviceName := a.findService(issEntry.name, sub)
	if serviceName == "" {
		return nil, fmt.Errorf("no service matching issuer=%s sub=%s", issEntry.name, sub)
	}

	return &Identity{Service: serviceName, Issuer: issEntry.name, Subject: sub}, nil
}

func (a *Authenticator) findService(issuerName, subject string) string {
	for name, svc := range a.services {
		if svc.Issuer == issuerName && svc.Subject == subject {
			return name
		}
	}
	return ""
}

func (a *Authenticator) CheckRepoAccess(service, repo string) bool {
	svc, ok := a.services[service]
	if !ok {
		return false
	}
	return svc.AllowsRepo(repo)
}
