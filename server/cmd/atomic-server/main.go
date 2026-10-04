// atomic-server: fleet management server for atomic-updater.
// Configuration is environment-driven (docs/api.md §4).
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"

	"atomic-updater/server/internal/api"
	"atomic-updater/server/internal/auth"
	"atomic-updater/server/internal/bundles"
	"atomic-updater/server/internal/ca"
	"atomic-updater/server/internal/config"
	"atomic-updater/server/internal/rollout"
	"atomic-updater/server/internal/storage"
	"atomic-updater/server/internal/web"
)

func main() {
	hashPassword := flag.String("hash-password", "",
		"print the bcrypt hash for ATOMIC_ADMIN_PASSWORD_HASH and exit")
	flag.Parse()
	if *hashPassword != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(*hashPassword), bcrypt.DefaultCost)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(string(hash))
		return
	}

	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("applying migrations")
	if err := storage.Migrate(cfg.DBURL); err != nil {
		return err
	}
	db, err := storage.Open(ctx, cfg.DBURL)
	if err != nil {
		return err
	}
	defer db.Close()

	s3, err := storage.OpenS3(ctx, storage.S3Config{
		Endpoint:       cfg.S3Endpoint,
		PublicEndpoint: cfg.S3PublicEndpoint,
		AccessKey:      cfg.S3AccessKey,
		SecretKey:      cfg.S3SecretKey,
		Bucket:         cfg.S3Bucket,
		UseSSL:         cfg.S3UseSSL,
	})
	if err != nil {
		return err
	}

	deviceCA, err := auth.LoadCertificate(cfg.DeviceCACert)
	if err != nil {
		return fmt.Errorf("device CA: %w", err)
	}
	mfgCA, err := auth.LoadCertificate(cfg.MfgCACert)
	if err != nil {
		return fmt.Errorf("manufacturing CA: %w", err)
	}
	issuer, err := ca.LoadIssuer(cfg.DeviceCACert, cfg.DeviceCAKey)
	if err != nil {
		return fmt.Errorf("device CA issuer: %w", err)
	}
	chainPEM, err := os.ReadFile(cfg.CAChain)
	if err != nil {
		return fmt.Errorf("CA chain: %w", err)
	}

	poller := &bundles.Poller{Objects: s3, Registry: db, Log: log}
	srv := &api.Server{
		Store:   db,
		Objects: s3,
		Controller: &rollout.Controller{
			Store:     db,
			Presigner: s3,
			TTL:       cfg.PresignTTL,
		},
		Issuer: issuer,
		Sessions: &auth.Sessions{
			Store:        db,
			User:         cfg.AdminUser,
			PasswordHash: []byte(cfg.AdminPasswordHash),
			TTL:          24 * time.Hour,
		},
		Scanner:        poller,
		DeviceCA:       deviceCA,
		MfgCA:          mfgCA,
		CAChainPEM:     chainPEM,
		PauseMinCohort: cfg.PauseMinCohort,
		PauseFailPct:   cfg.PauseFailPct,
		Log:            log,
	}

	serverCert, err := tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
	if err != nil {
		return fmt.Errorf("server TLS keypair: %w", err)
	}
	clientCAs := x509.NewCertPool()
	clientCAs.AddCert(deviceCA)
	clientCAs.AddCert(mfgCA)

	deviceSrv := &http.Server{
		Addr:    cfg.DeviceAddr,
		Handler: srv.DeviceHandler(),
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{serverCert},
			ClientAuth:   tls.RequireAndVerifyClientCert,
			ClientCAs:    clientCAs,
			MinVersion:   tls.VersionTLS12,
		},
		ReadHeaderTimeout: 10 * time.Second,
	}
	adminSrv := &http.Server{
		Addr:              cfg.AdminAddr,
		Handler:           srv.AdminHandler(web.Handler()),
		ReadHeaderTimeout: 10 * time.Second,
	}

	if cfg.BundlePollInterval > 0 {
		if n, err := poller.RunOnce(ctx); err != nil {
			log.Warn("initial bundle scan failed", "err", err)
		} else if n > 0 {
			log.Info("initial bundle scan", "registered", n)
		}
		go poller.Run(ctx, cfg.BundlePollInterval)
	}

	errCh := make(chan error, 2)
	go func() {
		log.Info("device API listening (mTLS)", "addr", cfg.DeviceAddr)
		if err := deviceSrv.ListenAndServeTLS("", ""); !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("device listener: %w", err)
		}
	}()
	go func() {
		log.Info("admin API + dashboard listening", "addr", cfg.AdminAddr)
		if err := adminSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("admin listener: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("shutting down")
	case err := <-errCh:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = deviceSrv.Shutdown(shutdownCtx)
	_ = adminSrv.Shutdown(shutdownCtx)
	return nil
}
