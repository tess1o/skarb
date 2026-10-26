package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata"

	"skarb/internal/backup"
	"skarb/internal/config"
	"skarb/internal/domain"
	"skarb/internal/fx"
	"skarb/internal/mcp"
	"skarb/internal/reminders"
	"skarb/internal/service"
	"skarb/internal/store"
	"skarb/internal/telegram"
	"skarb/internal/web"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "skarb:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return serve()
	}
	switch os.Args[1] {
	case "serve":
		return serve()
	case "mcp-stdio":
		return runMCPStdio(os.Args[2:])
	case "healthcheck":
		return healthcheck(os.Args[2:])
	case "admin":
		return adminCommand(os.Args[2:])
	case "backup":
		return backupCommand(os.Args[2:])
	case "version", "--version":
		fmt.Println("skarb 1.0.0")
		return nil
	default:
		return fmt.Errorf("unknown command %q (commands: serve, mcp-stdio, healthcheck, admin, backup)", os.Args[1])
	}
}

func serve() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := newLogger(cfg.LogLevel)
	if !cfg.SessionSecretFromEnv {
		logger.Warn("SKARB_SESSION_SECRET is not set; using a random ephemeral secret (sessions will not survive restart)")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, filepath.Join(cfg.DataDir, "skarb.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return err
	}
	fxSvc := fx.New(st, nil, logger, loc)
	svc, err := service.New(st, fxSvc, cfg)
	if err != nil {
		return err
	}
	if err := svc.BootstrapAdmin(ctx); err != nil {
		return err
	}
	if _, err := st.AdminUser(ctx); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			logger.Warn("no admin account exists; set SKARB_ADMIN_USERNAME and SKARB_ADMIN_PASSWORD on first run")
		} else {
			return err
		}
	}

	mcpServer := mcp.New(svc, cfg, logger)
	webServer, err := web.New(svc, cfg, logger)
	if err != nil {
		return err
	}
	webServer.MCPHandler = mcpServer.HTTPHandler()

	var tgBot *telegram.Bot
	if strings.TrimSpace(cfg.TelegramBotToken) != "" {
		tgBot, err = telegram.New(svc, cfg, logger, mcpServer)
		if err != nil {
			logger.Error("telegram init failed; continuing without bot", "error", err)
			tgBot = nil
		}
	}
	var sender reminders.Sender
	if tgBot != nil {
		sender = tgBot
	}
	scheduler := reminders.New(svc, sender, logger)
	webServer.OnRemindersChanged = func() {
		if err := scheduler.Reload(context.Background()); err != nil {
			logger.Error("reload reminders failed", "error", err)
		}
	}
	webServer.OnTestReminder = func(kind string, telegramUserID int64) error {
		return scheduler.SendTest(context.Background(), kind, telegramUserID)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		scheduler.Start(runCtx)
	}()
	if tgBot != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tgBot.Start(runCtx)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := fxSvc.FetchLatest(runCtx); err != nil {
			logger.Warn("initial NBU refresh failed", "error", err)
		}
		if cfg.FXBackfillDays > 0 {
			if err := fxSvc.Backfill(runCtx, cfg.FXBackfillDays); err != nil {
				logger.Warn("NBU backfill failed", "error", err)
			}
		}
	}()

	httpSrv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           webServer.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}
	errCh := make(chan error, 1)
	go func() {
		logger.Info("skarb listening", "addr", cfg.HTTPAddr, "public_url", cfg.PublicBaseURL)
		errCh <- httpSrv.ListenAndServe()
	}()

	select {
	case <-runCtx.Done():
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server stopped", "error", err)
		}
		cancel()
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	cancel()
	wg.Wait()
	return nil
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}

func openService(ctx context.Context, cfg config.Config) (*store.Store, *service.Service, error) {
	st, err := store.Open(ctx, filepath.Join(cfg.DataDir, "skarb.db"))
	if err != nil {
		return nil, nil, err
	}
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		st.Close()
		return nil, nil, err
	}
	fxSvc := fx.New(st, nil, newLogger(cfg.LogLevel), loc)
	svc, err := service.New(st, fxSvc, cfg)
	if err != nil {
		st.Close()
		return nil, nil, err
	}
	return st, svc, nil
}

func adminCommand(args []string) error {
	fs := flag.NewFlagSet("admin", flag.ExitOnError)
	if len(args) == 0 {
		return fmt.Errorf("usage: skarb admin reset-password")
	}
	switch args[0] {
	case "reset-password":
		_ = fs.Parse(args[1:])
	default:
		return fmt.Errorf("unknown admin command %q", args[0])
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx := context.Background()
	st, svc, err := openService(ctx, cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	username, err := svc.ResetAdminPassword(ctx)
	if err != nil {
		return err
	}
	fmt.Println("password reset for admin:", username)
	return nil
}

func backupCommand(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	_ = fs.Parse(args)
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx := context.Background()
	st, _, err := openService(ctx, cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	path, err := backup.Create(ctx, st, filepath.Join(cfg.DataDir, "backups"), 14)
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

func healthcheck(args []string) error {
	defaultURL := "http://127.0.0.1:8080/healthz"
	if addr := os.Getenv("SKARB_HTTP_ADDR"); addr != "" {
		if _, port, err := net.SplitHostPort(addr); err == nil && port != "" {
			defaultURL = "http://127.0.0.1:" + port + "/healthz"
		}
	}
	fs := flag.NewFlagSet("healthcheck", flag.ExitOnError)
	url := fs.String("url", defaultURL, "health endpoint URL")
	_ = fs.Parse(args)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(*url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned %d", resp.StatusCode)
	}
	return nil
}
