package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/app"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "check whether the local server is ready")
	flag.Parse()
	config, err := runtimeConfigFromEnv(os.Getenv)
	if err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("invalid bootstrap configuration", "error", err)
		os.Exit(1)
	}
	logLevel := new(slog.LevelVar)
	logLevel.Set(slog.LevelInfo)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	if *healthcheck {
		if err := checkHealth(context.Background(), config.healthURL()); err != nil {
			logger.Error("healthcheck failed", "error", err)
			os.Exit(1)
		}
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.Run(ctx, app.Config{
		DatabaseURL: config.databaseURL,
		HTTPAddress: config.listenerAddress(),
		Logger:      logger,
		LogLevel:    logLevel,
	}); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("application stopped with an error", "error", err)
		os.Exit(1)
	}
}

type runtimeConfig struct {
	databaseURL string
	bindAddress netip.Addr
	port        uint16
}

func runtimeConfigFromEnv(getenv func(string) string) (runtimeConfig, error) {
	bindAddress := strings.TrimSpace(getenv("HTTP_BIND_ADDRESS"))
	if bindAddress == "" {
		bindAddress = "0.0.0.0"
	}
	address, err := netip.ParseAddr(bindAddress)
	if err != nil {
		return runtimeConfig{}, fmt.Errorf("HTTP_BIND_ADDRESS must be an IP address: %w", err)
	}
	if address.Zone() != "" {
		return runtimeConfig{}, fmt.Errorf("HTTP_BIND_ADDRESS must not include an IPv6 zone")
	}
	portValue := strings.TrimSpace(getenv("HTTP_PORT"))
	if portValue == "" {
		portValue = "8080"
	}
	port, err := strconv.ParseUint(portValue, 10, 16)
	if err != nil || port == 0 {
		return runtimeConfig{}, fmt.Errorf("HTTP_PORT must be an integer between 1 and 65535")
	}
	return runtimeConfig{
		databaseURL: getenv("DATABASE_URL"),
		bindAddress: address,
		port:        uint16(port),
	}, nil
}

func (config runtimeConfig) listenerAddress() string {
	return netip.AddrPortFrom(config.bindAddress, config.port).String()
}

func (config runtimeConfig) healthURL() string {
	address := config.bindAddress
	if config.bindAddress.IsUnspecified() {
		address = netip.MustParseAddr("127.0.0.1")
		if config.bindAddress.Is6() {
			address = netip.IPv6Loopback()
		}
	}
	return "http://" + netip.AddrPortFrom(address, config.port).String() + "/health/ready"
}

func checkHealth(ctx context.Context, url string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	client := &http.Client{
		Timeout:   3 * time.Second,
		Transport: &http.Transport{Proxy: nil},
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("request readiness: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("readiness returned %s", response.Status)
	}
	return nil
}
