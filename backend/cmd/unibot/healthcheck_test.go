package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalHealthcheckRequiresReadiness(t *testing.T) {
	for _, status := range []int{200, 503, 302} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/readyz" {
				t.Error("incorrect probe route")
			}
			w.Header().Set("Location", "https://example.invalid/")
			w.WriteHeader(status)
		}))
		err := checkHealth(strings.TrimPrefix(server.URL, "http://"))
		server.Close()
		if (err == nil) != (status == 200) {
			t.Fatalf("status %d accepted incorrectly: %v", status, err)
		}
	}
	if checkHealth("invalid") == nil {
		t.Fatal("invalid address accepted")
	}
}
