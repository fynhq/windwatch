package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
	_ "time/tzdata" // keep the binary self-contained in shell-less container images
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/fynhq/windwatch/internal/app"
	"github.com/fynhq/windwatch/internal/config"
	"github.com/fynhq/windwatch/internal/logfile"
	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/secret"
	"github.com/fynhq/windwatch/internal/store"
	"github.com/fynhq/windwatch/internal/version"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "windwatch: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	command := "serve"
	remaining := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command = args[0]
		remaining = args[1:]
	}

	switch command {
	case "serve":
		return runServe(remaining)
	case "healthcheck":
		return runHealthcheck(remaining)
	case "reset-password":
		return runResetPassword(remaining)
	case "version":
		fmt.Printf("windwatch %s\n", version.Version)
		return nil
	case "help":
		printUsage()
		return nil
	default:
		printUsage()
		return fmt.Errorf("unknown command %q", command)
	}
}

func runServe(args []string) error {
	cfg, err := config.ParseServe(args, os.Getenv)
	if errors.Is(err, flag.ErrHelp) {
		printUsage()
		return nil
	}
	if err != nil {
		return err
	}

	fileLog, err := logfile.New(cfg.LogDir)
	if err != nil {
		return err
	}
	defer fileLog.Close()
	logger := slog.New(slog.NewTextHandler(io.MultiWriter(os.Stderr, fileLog), &slog.HandlerOptions{Level: cfg.LogLevel}))
	application, err := app.New(cfg, logger)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()
	return application.Run(ctx)
}

func runResetPassword(args []string) error {
	fs := flag.NewFlagSet("reset-password", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	defaultDataDir := "./data"
	if value := os.Getenv("WINDWATCH_DATA_DIR"); value != "" {
		defaultDataDir = value
	}
	dataDir := fs.String("data-dir", defaultDataDir, "persistent data directory")
	username := fs.String("username", "", "account to reset when multiple users exist")
	passwordStdin := fs.Bool("password-stdin", false, "read the new password from stdin")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage()
			return nil
		}
		return err
	}

	dbPath := filepath.Join(strings.TrimSpace(*dataDir), "windwatch.db")
	if _, err := os.Stat(dbPath); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("database not found: %s", dbPath)
	} else if err != nil {
		return fmt.Errorf("inspect database: %w", err)
	}

	ctx := context.Background()
	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open data store: %w", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}

	users, err := st.ListUsers(ctx)
	if err != nil {
		return fmt.Errorf("list users: %w", err)
	}
	if len(users) == 0 {
		return errors.New("no user accounts exist; start the service and complete setup first")
	}

	target, err := selectResetUser(users, strings.TrimSpace(*username))
	if err != nil {
		return err
	}

	password, err := readResetPassword(*passwordStdin)
	if err != nil {
		return err
	}
	switch count := utf8.RuneCountInString(password); {
	case count < 10:
		return errors.New("password must be at least 10 characters")
	case count > 256:
		return errors.New("password must be at most 256 characters")
	}

	hash, err := secret.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if err := st.ResetUserPassword(ctx, target.ID, hash, time.Now()); err != nil {
		return fmt.Errorf("reset password: %w", err)
	}
	fmt.Fprintf(os.Stdout, "Password reset for %q; all sessions for this account were invalidated.\n", target.Username)
	return nil
}

func selectResetUser(users []model.Admin, username string) (model.Admin, error) {
	if username == "" {
		if len(users) == 1 {
			return users[0], nil
		}
		names := make([]string, 0, len(users))
		for _, user := range users {
			names = append(names, user.Username)
		}
		return model.Admin{}, fmt.Errorf("multiple accounts exist; specify --username (%s)", strings.Join(names, ", "))
	}
	for _, user := range users {
		if user.Username == username {
			return user, nil
		}
	}
	return model.Admin{}, fmt.Errorf("account %q not found", username)
}

func readResetPassword(fromStdin bool) (string, error) {
	if fromStdin {
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 4096))
		if err != nil {
			return "", fmt.Errorf("read password from stdin: %w", err)
		}
		password := strings.TrimRight(string(data), "\r\n")
		if strings.ContainsAny(password, "\r\n") {
			return "", errors.New("password from stdin must be a single line")
		}
		return password, nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("stdin is not a terminal; use --password-stdin")
	}
	fmt.Fprint(os.Stderr, "New password: ")
	first, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	fmt.Fprint(os.Stderr, "Confirm password: ")
	second, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read password confirmation: %w", err)
	}
	if string(first) != string(second) {
		return "", errors.New("passwords do not match")
	}
	return string(first), nil
}

func runHealthcheck(args []string) error {
	fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	defaultURL := "http://127.0.0.1:8080/healthz"
	if value := os.Getenv("WINDWATCH_HEALTH_URL"); value != "" {
		defaultURL = value
	}
	url := fs.String("url", defaultURL, "health endpoint URL")
	if err := fs.Parse(args); err != nil {
		return err
	}

	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(*url)
	if err != nil {
		return fmt.Errorf("healthcheck request: %w", err)
	}
	defer response.Body.Close()
	io.Copy(io.Discard, response.Body)

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("healthcheck returned HTTP %d", response.StatusCode)
	}
	return nil
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `Usage:
  windwatch [serve] [flags]             Start the web service
  windwatch healthcheck [flags]         Check a running instance
  windwatch reset-password [flags]      Reset an account password
  windwatch version                     Print the version

Serve flags:
  --addr string              HTTP listen address (default ":8080")
  --data-dir string          persistent data directory (default "./data")
  --log-level string         debug, info, warn, error (default "info")
  --log-dir string           daily log file directory, kept 30 days (default "<data-dir>/logs")
  --trusted-proxies string   comma-separated trusted proxy IPs or CIDRs

Healthcheck flags:
  --url string               health endpoint URL (default "http://127.0.0.1:8080/healthz")

Reset-password flags:
  --data-dir string    persistent data directory (default "./data")
  --username string    account to reset when multiple users exist
  --password-stdin     read the new password from stdin`)
}
