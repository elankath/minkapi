package api

import (
	"k8s.io/utils/set"
	"time"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	ProgramName = "minkapi"
)

const (
	DefaultHost           = "localhost"
	DefaultPort           = 8008
	DefaultWatchQueueSize = 100
	DefaultWatchTimeout   = 5 * time.Minute

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

	// ProfilingEnable indicates whether the minkapi service should register the standard pprof HTTP handlers: /debug/pprof/*
	ProfilingEnabled bool
}
type MinKAPIAccess interface {
	CreateObject(gvk schema.GroupVersionKind, obj metav1.Object) error
	DeleteObjects(gvk schema.GroupVersionKind, namespace string, names set.Set[string]) error
	ListPods(namespace string, matchingPodNames ...string) ([]*corev1.Pod, error)
	DeleteObjectsMatchingLabels(gvk schema.GroupVersionKind, namespace string, labels map[string]string) error
	ListEvents(namespace string) ([]*eventsv1.Event, error)
}
