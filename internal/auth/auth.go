// Package auth handles the OIDC login flow and cookie sessions.
package auth

import (
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/religiosa1/dictionary/internal/store"
)

const (
	sessionCookie = "session"
	loginCookie   = "login"
	sessionTTL    = 30 * 24 * time.Hour
	loginTTL      = 10 * time.Minute
)

type Config struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	BaseURL      string // public URL of the app, e.g. https://slownik.example.com
	EditorGroup  string
}

// Auth is the OIDC login and logout flow; sessions are handed over to Sessions.
type Auth struct {
	db          *store.DB
	sessions    *Sessions
	oauth       oauth2.Config
	verifier    *oidc.IDTokenVerifier
	editorGroup string
	baseURL     string
	endSession  string // provider's end_session_endpoint; empty if not supported
}

// New discovers the OIDC provider, so the issuer must be reachable on startup.
func New(ctx context.Context, cfg Config, db *store.DB, sessions *Sessions) (*Auth, error) {
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("error discovering OIDC provider: %w", err)
	}
	var meta struct {
		EndSessionEndpoint string `json:"end_session_endpoint"`
	}
	if err := provider.Claims(&meta); err != nil {
		return nil, fmt.Errorf("error parsing OIDC provider metadata: %w", err)
	}
	baseURL := strings.TrimSuffix(cfg.BaseURL, "/")
	return &Auth{
		db:       db,
		sessions: sessions,
		oauth: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  baseURL + "/auth/callback",
			Scopes:       []string{oidc.ScopeOpenID, "profile", "groups"},
		},
		verifier:    provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		editorGroup: cfg.EditorGroup,
		baseURL:     baseURL,
		endSession:  meta.EndSessionEndpoint,
	}, nil
}

// Login redirects to the OIDC provider. Flow secrets and the return path are
// kept in a short-lived cookie until the callback.
func (a *Auth) Login(w http.ResponseWriter, r *http.Request) {
	state := rand.Text()
	nonce := rand.Text()
	verifier := oauth2.GenerateVerifier()
	flow := url.Values{
		"state":    {state},
		"nonce":    {nonce},
		"verifier": {verifier},
		"next":     {safeRedirect(r.URL.Query().Get("next"))},
	}
	a.sessions.setCookie(w, loginCookie, flow.Encode(), loginTTL)
	authURL := a.oauth.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce))
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (a *Auth) Callback(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(loginCookie)
	if err != nil {
		http.Error(w, "Сессия входа истекла, попробуйте ещё раз", http.StatusBadRequest)
		return
	}
	a.sessions.setCookie(w, loginCookie, "", -1)
	flow, err := url.ParseQuery(cookie.Value)
	if err != nil || flow.Get("state") == "" || flow.Get("state") != r.URL.Query().Get("state") {
		http.Error(w, "Неверный параметр state", http.StatusBadRequest)
		return
	}
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		http.Error(w, "Ошибка входа: "+errParam, http.StatusForbidden)
		return
	}

	user, rawIDToken, err := a.exchange(r.Context(), r.URL.Query().Get("code"), flow)
	if err != nil {
		slog.Error("OIDC callback failed", "err", err)
		http.Error(w, "Не удалось войти", http.StatusBadGateway)
		return
	}
	a.sessions.Start(w, r, user, rawIDToken, flow.Get("next"))
}

// Logout ends the app session, then the provider's one (RP-initiated logout),
// which redirects back to the app's home page. Without an ID token the
// provider would ask the user to confirm, so the logout stays local then.
func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	idToken := a.sessions.End(w, r)
	if a.endSession == "" || idToken == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	params := url.Values{
		"client_id":                {a.oauth.ClientID},
		"id_token_hint":            {idToken},
		"post_logout_redirect_uri": {a.baseURL + "/"},
	}
	http.Redirect(w, r, a.endSession+"?"+params.Encode(), http.StatusSeeOther)
}

// exchange trades the authorization code for an ID token and upserts the user
// from its claims. Returns the raw ID token too.
func (a *Auth) exchange(ctx context.Context, code string, flow url.Values) (store.User, string, error) {
	oauthToken, err := a.oauth.Exchange(ctx, code, oauth2.VerifierOption(flow.Get("verifier")))
	if err != nil {
		return store.User{}, "", fmt.Errorf("code exchange: %w", err)
	}
	rawIDToken, ok := oauthToken.Extra("id_token").(string)
	if !ok {
		return store.User{}, "", errors.New("no id_token in token response")
	}
	idToken, err := a.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return store.User{}, "", fmt.Errorf("id token verification: %w", err)
	}
	if idToken.Nonce != flow.Get("nonce") {
		return store.User{}, "", errors.New("nonce mismatch")
	}

	var claims struct {
		Name              string   `json:"name"`
		PreferredUsername string   `json:"preferred_username"`
		Groups            []string `json:"groups"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return store.User{}, "", fmt.Errorf("parsing claims: %w", err)
	}
	name := cmp.Or(claims.Name, claims.PreferredUsername, idToken.Subject)
	role := store.RoleReader
	if slices.Contains(claims.Groups, a.editorGroup) {
		role = store.RoleEditor
	}
	user, err := a.db.UpsertUser(idToken.Subject, name, role)
	return user, rawIDToken, err
}
