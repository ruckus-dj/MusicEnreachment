package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRuntimeConfigFromEnv(t *testing.T) {
	tests := []struct {
		name        string
		environment map[string]string
		wantAddress string
		wantHealth  string
		wantErr     bool
	}{
		{name: "defaults", wantAddress: "0.0.0.0:8080", wantHealth: "http://127.0.0.1:8080/health/ready"},
		{name: "custom IPv4", environment: map[string]string{"HTTP_BIND_ADDRESS": "127.0.0.1", "HTTP_PORT": "9090"}, wantAddress: "127.0.0.1:9090", wantHealth: "http://127.0.0.1:9090/health/ready"},
		{name: "specific IPv4", environment: map[string]string{"HTTP_BIND_ADDRESS": "192.0.2.1", "HTTP_PORT": "9090"}, wantAddress: "192.0.2.1:9090", wantHealth: "http://192.0.2.1:9090/health/ready"},
		{name: "IPv6", environment: map[string]string{"HTTP_BIND_ADDRESS": "::1", "HTTP_PORT": "8081"}, wantAddress: "[::1]:8081", wantHealth: "http://[::1]:8081/health/ready"},
		{name: "specific IPv6", environment: map[string]string{"HTTP_BIND_ADDRESS": "2001:db8::1", "HTTP_PORT": "8081"}, wantAddress: "[2001:db8::1]:8081", wantHealth: "http://[2001:db8::1]:8081/health/ready"},
		{name: "invalid bind address", environment: map[string]string{"HTTP_BIND_ADDRESS": "localhost"}, wantErr: true},
		{name: "zoned IPv6 bind address", environment: map[string]string{"HTTP_BIND_ADDRESS": "fe80::1%en0"}, wantErr: true},
		{name: "invalid port", environment: map[string]string{"HTTP_PORT": "0"}, wantErr: true},
		{name: "out of range port", environment: map[string]string{"HTTP_PORT": "65536"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := runtimeConfigFromEnv(func(name string) string { return test.environment[name] })
			if (err != nil) != test.wantErr {
				t.Fatalf("runtimeConfigFromEnv() error = %v, wantErr %t", err, test.wantErr)
			}
			if test.wantErr {
				return
			}
			if got := config.listenerAddress(); got != test.wantAddress {
				t.Errorf("listenerAddress() = %q, want %q", got, test.wantAddress)
			}
			if got := config.healthURL(); got != test.wantHealth {
				t.Errorf("healthURL() = %q, want %q", got, test.wantHealth)
			}
		})
	}
}

func TestCheckHealth(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "ready", status: http.StatusOK},
		{name: "not ready", status: http.StatusServiceUnavailable, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(test.status)
			}))
			defer server.Close()
			err := checkHealth(context.Background(), server.URL)
			if (err != nil) != test.wantErr {
				t.Errorf("checkHealth() error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
}
