package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

func TestBearerTokenTransportRequiresHTTPSOutsideLoopback(t *testing.T) {
	tests := []struct {
		address string
		allowed bool
	}{
		{"https://controller.example:8080", true},
		{"http://localhost:8080", true},
		{"http://127.0.0.1:8080", true},
		{"http://[::1]:8080", true},
		{"http://controller.example:8080", false},
		{"ftp://localhost:8080", false},
	}

	for _, test := range tests {
		t.Run(test.address, func(t *testing.T) {
			target, err := url.Parse(test.address)
			if err != nil {
				t.Fatal(err)
			}
			if got := allowsBearerTokenTransport(target); got != test.allowed {
				t.Fatalf("allowsBearerTokenTransport(%q) = %t, want %t", test.address, got, test.allowed)
			}
		})
	}
}

func TestBearerTokenRequestsDoNotFollowRedirects(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	source := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusFound))
	defer source.Close()

	client := &cliClient{baseURL: source.URL, apiToken: "local-test-token"}
	client.http = &http.Client{CheckRedirect: client.checkRedirect}
	if err := client.request(http.MethodGet, "/", nil, nil); err == nil {
		t.Fatal("redirect response unexpectedly succeeded")
	}
	if hits := targetHits.Load(); hits != 0 {
		t.Fatalf("bearer-authenticated request followed redirect to target %d times", hits)
	}
}
