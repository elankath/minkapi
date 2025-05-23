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
	info, ok := debug.ReadBuildInfo()
	if ok {
		if info.Main.Version != "" {
			fmt.Printf("%s version: %s\n", api.ProgramName, info.Main.Version)
		}
	} else {
		fmt.Printf("%s: binary build info not embedded", api.ProgramName)
	}

	appCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	service, err := core.NewInMemoryMinKAPI(appCtx)
	if err != nil {
		klog.Error("failed to initialize InMemoryKAPI: %v", err)
		return
	}
	// Start the service in a goroutine
	go func() {
		if err := service.Start(); err != nil {
			klog.Errorf("%s service failed: %v", api.ProgramName, err)
			os.Exit(1)
		}
	}()

	// Wait for a signal
	<-appCtx.Done()
	stop()
	klog.Warning("Received shutdown signal, initiating graceful shutdown...")

	// Create a context with a 5-second timeout for shutdown
	shutDownCtx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	// Perform shutdown
	if err := service.Shutdown(shutDownCtx); err != nil {
		klog.Errorf("Kubernetes API InMemoryKAPI Shutdown failed: %v", err)
		os.Exit(1)
	}
	klog.Info("Kubernetes API InMemoryKAPI Shutdown gracefully")
}
