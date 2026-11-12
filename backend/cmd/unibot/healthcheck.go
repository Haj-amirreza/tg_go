package main

import (
	"errors"
	"net"
	"net/http"
	"time"
)

// Probe the local API without a shell, curl, database credentials or migrations.
func checkHealth(address string) error {
	if address == "" {
		address = "127.0.0.1:8080"
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || port == "" {
		return errors.New("invalid healthcheck address")
	}
	loopback := "127.0.0.1"
	if host == "::" || host == "::1" {
		loopback = "::1"
	}
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get("http://" + net.JoinHostPort(loopback, port) + "/readyz")
	if err != nil {
		return errors.New("HTTP healthcheck unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("HTTP service is not ready")
	}
	return nil
}
