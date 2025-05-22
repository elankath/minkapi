package main

import (
	"context"
	"fmt"
	"github.com/elankath/minkapi/api"
	"github.com/elankath/minkapi/core"
	"k8s.io/klog/v2"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"
)

func main() {
	server, err := core.NewInMemoryMinKAPI()
	if err != nil {
		klog.Fatalf("failed to initialize InMemoryKAPI: %v", err)
	}
	info, ok := debug.ReadBuildInfo()
	if ok {
		if info.Main.Version != "" {
			fmt.Printf("%s version: %s\n", api.ProgramName, info.Main.Version)
		}
	} else {
		fmt.Printf("%s: binary build info not embedded", api.ProgramName)
	}

	// Set up signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start the server in a goroutine
	go func() {
		if err := server.Start(); err != nil {
			klog.Errorf("%s server failed: %v", api.ProgramName, err)
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
		klog.Errorf("Kubernetes API InMemoryKAPI Shutdown failed: %v", err)
		os.Exit(1)
	}
	klog.Info("Kubernetes API InMemoryKAPI Shutdown gracefully")
}
