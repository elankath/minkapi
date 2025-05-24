package api

import "time"

var (
	ProgramName = "minkapi"
)

const (
	DefaultHost           = "localhost"
	DefaultPort           = 8008
	DefaultWatchQueueSize = 100
	DefaultWatchTimeout   = 30 * time.Second

	DefaultKubeConfigPath = "/tmp/minkapi.yaml"
)

type MinKAPIConfig struct {
	// Host name/IP address for the MinKAPI service.By default this is localhost as MinKAPI is meant to be used as a local helper serivice.
	// Use "0.0.0.0"  to bind to all interfaces.
	Host string

	// Port is the HTTP port on which the MinKAPI service listens for KAPI requests.
	Port int

	// KubeConfigPath is the path at which MinKAPI generates a kubeconfig that can be used by kubectl and other k8s clients to connect to MinKAPI
	KubeConfigPath string

	// WatchTimeout represents the timeout for watches following which MinKAPI service will close the connection and ends the watch.
	WatchTimeout time.Duration

	// WatchQueueSize is the maximum number of events to queue per watcher
	WatchQueueSize int
}
type MinKAPIAccess interface {
	// LoadObjecs([]metav1.Object|runtime.Object)
}
