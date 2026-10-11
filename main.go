package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/religiosa1/dictionary/internal/auth"
	"github.com/religiosa1/dictionary/internal/store"
	"github.com/religiosa1/dictionary/internal/version"
	"github.com/religiosa1/dictionary/internal/web"
)

type config struct {
	ListenAddr string
	DBPath     string
	PublicRead bool
	Auth       auth.Config
}

func loadConfig() (config, error) {
	cfg := config{
		ListenAddr: cmp.Or(os.Getenv("DICT_LISTEN_ADDR"), "127.0.0.1:8080"),
		DBPath:     cmp.Or(os.Getenv("DICT_DB_PATH"), "dictionary.db"),
		Auth: auth.Config{
			Issuer:       os.Getenv("DICT_OIDC_ISSUER"),
			ClientID:     os.Getenv("DICT_OIDC_CLIENT_ID"),
			ClientSecret: os.Getenv("DICT_OIDC_CLIENT_SECRET"),
			BaseURL:      os.Getenv("DICT_BASE_URL"),
			EditorGroup:  cmp.Or(os.Getenv("DICT_EDITOR_GROUP"), "dictionary-editors"),
		},
	}
	if value := os.Getenv("DICT_PUBLIC_READ"); value != "" {
		publicRead, err := strconv.ParseBool(value)
		if err != nil {
			return cfg, fmt.Errorf("invalid DICT_PUBLIC_READ: %w", err)
		}
		cfg.PublicRead = publicRead
	}

	var missing []error
	for name, value := range map[string]string{
		"DICT_OIDC_ISSUER":        cfg.Auth.Issuer,
		"DICT_OIDC_CLIENT_ID":     cfg.Auth.ClientID,
		"DICT_OIDC_CLIENT_SECRET": cfg.Auth.ClientSecret,
		"DICT_BASE_URL":           cfg.Auth.BaseURL,
	} {
		if value == "" {
			missing = append(missing, fmt.Errorf("%s is required", name))
		}
	}
	return cfg, errors.Join(missing...)
}

func main() {
	var showVersion bool
	flag.BoolVar(&showVersion, "version", false, "show version information and exit")
	flag.BoolVar(&showVersion, "v", false, "shorthand for -version")
	flag.Parse()
	if showVersion {
		fmt.Println(version.String())
		return
	}

	if err := run(); err != nil {
		slog.Error("fatal error", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	sessions := auth.NewSessions(db, strings.HasPrefix(cfg.Auth.BaseURL, "https://"))
	authn, err := auth.New(ctx, cfg.Auth, db, sessions)
	if err != nil {
		return err
	}
	app, err := web.New(db, cfg.PublicRead)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/login", authn.Login)
	mux.HandleFunc("GET /auth/callback", authn.Callback)
	mux.HandleFunc("POST /auth/logout", authn.Logout)
	app.Register(mux)

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           web.Handler(sessions, mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	serveErr := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.ListenAddr, "publicRead", cfg.PublicRead, "version", version.String())
		serveErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return server.Shutdown(shutdownCtx)
}
