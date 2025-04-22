package main

import (
	"context"
	"github.com/elankath/kapisim/core"
	"k8s.io/klog/v2"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	server, err := core.NewKAPISimulator()
	if err != nil {
		klog.Fatalf("failed to initialize KAPISimulator: %v", err)
	}
	klog.Info("Kubernetes API Simulator running on :8080")

	// Set up signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start the server in a goroutine
	go func() {
		if err := server.Start(); err != nil {
			klog.Errorf("Server failed: %v", err)
			os.Exit(1)
		}
	}()

	// Wait for a signal
	<-sigCh
	klog.Warning("Received shutdown signal, initiating graceful shutdown...")

	// Create a context with a 5-second timeout for shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	// Perform shutdown
	if err := server.Shutdown(ctx); err != nil {
		klog.Errorf("Kubernetes API Simulator Shutdown failed: %v", err)
		os.Exit(1)
	}
	klog.Info("Kubernetes API Simulator Shutdown gracefully")
}
