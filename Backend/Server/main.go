package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--benchmark" {
		for _, nodes := range []int{1, 2, 4, 8} {
			metrics := RunBenchmark(nodes, 10000, true)
			printBenchmark(fmt.Sprintf("Nodren Benchmark (%d nodes)", nodes), metrics)
		}
		return
	}

	state := newServiceState()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", state.healthHandler)
	mux.HandleFunc("/nodes", state.nodesHandler)
	mux.HandleFunc("/jobs", state.jobsHandler)
	mux.HandleFunc("/stats", state.statsHandler)

	server := &http.Server{
		Addr:              ":8080",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-shutdown
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("server shutdown failed: %v", err)
		}
		os.Exit(0)
	}()

	fmt.Println("Nodren controller listening on :8080")
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
