package main

import (
	"bytes"
	"crypto/hmac"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPAPIRequiresBearerTokenOutsideExplicitDevelopmentMode(t *testing.T) {
	controller := NewController("127.0.0.1:0", "127.0.0.1:0")
	controller.authMode = "secure"
	controller.apiToken = "api-token-with-at-least-thirty-two-characters"

	request := httptest.NewRequest(http.MethodGet, "/v1/cluster/status", nil)
	response := httptest.NewRecorder()
	controller.routes().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated API request returned %d, want 401", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/cluster/status", nil)
	request.Header.Set("Authorization", "Bearer "+controller.apiToken)
	response = httptest.NewRecorder()
	controller.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated API request returned %d, want 200: %s", response.Code, response.Body)
	}

	controller.authMode = "development"
	request = httptest.NewRequest(http.MethodGet, "/v1/cluster/status", nil)
	request.RemoteAddr = "203.0.113.20:54321"
	response = httptest.NewRecorder()
	controller.routes().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("remote development API request returned %d, want 403", response.Code)
	}
}

func TestHTTPBodyLimitsAreScopedToNonArtifactRequests(t *testing.T) {
	controller := NewController("127.0.0.1:0", "127.0.0.1:0")
	controller.authMode = "secure"
	controller.apiToken = "api-token-with-at-least-thirty-two-characters"
	controller.artifactDir = t.TempDir()

	request := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(nil))
	request.ContentLength = (16 << 20) + 1
	request.Header.Set("Authorization", "Bearer "+controller.apiToken)
	response := httptest.NewRecorder()
	controller.routes().ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized JSON request returned %d, want 413", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/artifacts", bytes.NewReader(nil))
	request.ContentLength = (16 << 20) + 1
	request.Header.Set("Authorization", "Bearer "+controller.apiToken)
	response = httptest.NewRecorder()
	controller.routes().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("artifact upload was incorrectly limited to 16 MiB: %d %s", response.Code, response.Body)
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/artifacts", bytes.NewReader(nil))
	request.ContentLength = int64(maxArtifactSize + 1)
	request.Header.Set("Authorization", "Bearer "+controller.apiToken)
	response = httptest.NewRecorder()
	controller.routes().ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized artifact request returned %d, want 413", response.Code)
	}
}

func TestWorkerAuthenticationBindsIdentityAndRejectsWrongCredential(t *testing.T) {
	server, worker := net.Pipe()
	defer server.Close()
	defer worker.Close()
	const id = "worker-a"
	const secret = "worker-a-authentication-secret-at-least-32"
	done := make(chan struct {
		id  string
		key []byte
		err error
	}, 1)
	go func() {
		id, key, err := authenticateWorker(server, map[string]string{id: secret})
		done <- struct {
			id  string
			key []byte
			err error
		}{id, key, err}
	}()

	challenge, err := readFrame(worker)
	if err != nil {
		t.Fatal(err)
	}
	proof := hmacSHA256([]byte(secret), []byte("NODREN-WORKER-LOGIN-v1\x00"), challenge.Payload, []byte(id))
	var response bytes.Buffer
	if err := writeString(&response, id); err != nil {
		t.Fatal(err)
	}
	response.Write(proof)
	if err := writeFrame(worker, MsgAuthResponse, 0, response.Bytes()); err != nil {
		t.Fatal(err)
	}
	result, err := readFrame(worker)
	if err != nil {
		t.Fatal(err)
	}
	key := deriveWorkerSessionKey([]byte(secret), challenge.Payload, id)
	expected := hmacSHA256(key, []byte("NODREN-CONTROLLER-LOGIN-v1\x00"), challenge.Payload, []byte(id))
	if result.Type != MsgAuthResult || !hmac.Equal(result.Payload, expected) {
		t.Fatal("Controller proof did not match")
	}
	auth := <-done
	if auth.err != nil || auth.id != id || !hmac.Equal(auth.key, key) {
		t.Fatalf("Worker authentication failed: %#v", auth)
	}

	payload := []byte("secured result")
	seq := uint64(1)
	protected := protectFramePayload(key, 'W', MsgTaskResult, 7, seq, payload)
	decoded, err := verifyFramePayload(key, 'W', MsgTaskResult, 7, seq, protected)
	if err != nil || !bytes.Equal(decoded, payload) {
		t.Fatalf("authenticated frame verification failed: %v", err)
	}
	if _, err := verifyFramePayload(key, 'W', MsgTaskResult, 7, seq+1, protected); err == nil {
		t.Fatal("replayed frame was accepted")
	}
}

func TestWorkerAuthenticationRejectsWrongCredential(t *testing.T) {
	server, worker := net.Pipe()
	defer server.Close()
	defer worker.Close()
	const id = "worker-a"
	const secret = "worker-a-authentication-secret-at-least-32"
	done := make(chan error, 1)
	go func() {
		_, _, err := authenticateWorker(server, map[string]string{id: secret})
		done <- err
	}()

	challenge, err := readFrame(worker)
	if err != nil {
		t.Fatal(err)
	}
	wrongProof := hmacSHA256([]byte("different-worker-secret-at-least-32-chars"), []byte("NODREN-WORKER-LOGIN-v1\x00"), challenge.Payload, []byte(id))
	var response bytes.Buffer
	if err := writeString(&response, id); err != nil {
		t.Fatal(err)
	}
	response.Write(wrongProof)
	if err := writeFrame(worker, MsgAuthResponse, 0, response.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("Worker with the wrong credential was authenticated")
	}
}

func TestSecureModeRequiresPerWorkerCredentialsAndHTTPS(t *testing.T) {
	tokens := map[string]string{"worker-a": "worker-a-authentication-secret-at-least-32"}
	if err := validateWorkerTokens(tokens); err != nil {
		t.Fatal(err)
	}
	if err := validateWorkerTokens(map[string]string{
		"worker-a": "same-authentication-secret-at-least-32-chars",
		"worker-b": "same-authentication-secret-at-least-32-chars",
	}); err == nil {
		t.Fatal("duplicate Worker credentials were accepted")
	}
	if err := validateControllerSecurity("development", "", nil, "0.0.0.0:8080", "127.0.0.1:9000", "", ""); err == nil {
		t.Fatal("development mode accepted a non-loopback HTTP bind")
	}
	if err := validateControllerSecurity("secure", "api-token-with-at-least-thirty-two-characters", tokens, "127.0.0.1:8080", "127.0.0.1:9000", "", ""); err == nil {
		t.Fatal("secure mode accepted missing HTTPS credentials")
	}
}
