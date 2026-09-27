package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"net/http"
)

const (
	httpAddr    = ":3000"
	runtimeAddr = "127.0.0.1:9100"
	webRoot     = "../../../Frontend/Web/dist"
)

func main() {
	http.HandleFunc("/api/status", handleStatus)
	http.HandleFunc("/api/nodes", handleNodes)
	http.HandleFunc("/api/jobs", handleJobs)

	http.Handle("/", http.FileServer(http.Dir(webRoot)))

	log.Printf("[Nodren Gateway] listening on http://localhost%s", httpAddr)

	if err := http.ListenAndServe(httpAddr, nil); err != nil {
		log.Fatal(err)
	}
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	response, err := sendToRuntime("STATUS")
	if err != nil {
		http.Error(w, "runtime unavailable", http.StatusServiceUnavailable)
		log.Printf("[Nodren Gateway] runtime error: %v", err)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(response))
}

func handleNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	response, err := sendToRuntime("NODES")
	if err != nil {
		http.Error(w, "runtime unavailable", http.StatusServiceUnavailable)
		log.Printf("[Nodren Gateway] runtime error: %v", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(response))
}

func handleJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	response, err := sendToRuntime("JOBS")
	if err != nil {
		http.Error(w, "runtime unavailable", http.StatusServiceUnavailable)
		log.Printf("[Nodren Gateway] runtime error: %v", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(response))
}

func sendToRuntime(request string) (string, error) {
	conn, err := net.Dial("tcp", runtimeAddr)
	if err != nil {
		return "", fmt.Errorf("connect to runtime: %w", err)
	}
	defer conn.Close()

	if _, err := fmt.Fprintf(conn, "%s\n", request); err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}

	reader := bufio.NewReader(conn)

	response, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	return response[:len(response)-1], nil
}