package core

import (
	"context"
	"encoding/json"
	"fmt"
	appsv1 "k8s.io/api/apps/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

// KAPISimulator holds the in-memory stores, watch channels, and version tracking
type KAPISimulator struct {
	stores    map[schema.GroupVersionResource]cache.Store
	watchers  map[schema.GroupVersionResource]map[string]chan watch.Event
	versions  map[schema.GroupVersionResource]int64 // GVR -> latest resourceVersion
	scheme    *runtime.Scheme
	mux       *http.ServeMux
	watchLock sync.Mutex
	storeLock sync.Mutex
	server    *http.Server
}

func NewKAPISimulator() (*KAPISimulator, error) {
	scheme := runtime.NewScheme()
	//TODO: dont ignore errors. Make a list of scheme adders and make a function to invoke the list.
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	_ = coordinationv1.AddToScheme(scheme)
	_ = eventsv1.AddToScheme(scheme)
	_ = rbacv1.AddToScheme(scheme)

	mux := http.NewServeMux()
	s := &KAPISimulator{
		stores: map[schema.GroupVersionResource]cache.Store{
			{Group: "", Version: "v1", Resource: "pods"}:                           cache.NewStore(cache.MetaNamespaceKeyFunc),
			{Group: "", Version: "v1", Resource: "namespaces"}:                     cache.NewStore(cache.MetaNamespaceKeyFunc),
			{Group: "apps", Version: "v1", Resource: "deployments"}:                cache.NewStore(cache.MetaNamespaceKeyFunc),
			{Group: "apps", Version: "v1", Resource: "replicasets"}:                cache.NewStore(cache.MetaNamespaceKeyFunc),
			{Group: "coordination.k8s.io", Version: "v1", Resource: "leases"}:      cache.NewStore(cache.MetaNamespaceKeyFunc),
			{Group: "events.k8s.io", Version: "v1", Resource: "events"}:            cache.NewStore(cache.MetaNamespaceKeyFunc),
			{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"}: cache.NewStore(cache.MetaNamespaceKeyFunc),
		},
		watchers: make(map[schema.GroupVersionResource]map[string]chan watch.Event),
		versions: make(map[schema.GroupVersionResource]int64),
		scheme:   scheme,
		mux:      mux,
		server:   &http.Server{Addr: ":8080", Handler: mux},
	}

	s.registerRoutes()
	if err := s.generateKubeconfig(); err != nil {
		return nil, fmt.Errorf("failed to generate kubeconfig: %w", err)
	}
	return s, nil
}

func (s *KAPISimulator) GetMux() *http.ServeMux {
	return s.mux
}

// Start begins the HTTP server
func (s *KAPISimulator) Start() error {
	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server failed: %v", err)
	}
	return nil
}

// Shutdown cleans up resources and shuts down the HTTP server
func (s *KAPISimulator) Shutdown(ctx context.Context) error {
	// Close all watch channels
	s.watchLock.Lock()
	for gvr, nsWatchers := range s.watchers {
		for ns, ch := range nsWatchers {
			close(ch)
			delete(nsWatchers, ns)
		}
		delete(s.watchers, gvr)
	}
	s.watchLock.Unlock()

	// Shut down the HTTP server
	return s.server.Shutdown(ctx)
}
func (s *KAPISimulator) registerRoutes() {
	// API discovery
	s.mux.HandleFunc("GET /api", s.handleCoreAPIVersions)
	s.mux.HandleFunc("GET /apis", s.handleAPIGroups)
	//s.mux.HandleFunc("GET /api/v1", s.handleCoreAPIResources)
	s.mux.HandleFunc("GET /api/v1/", s.handleCoreAPIResources)
	s.mux.HandleFunc("GET /apis/apps/", s.handleAppsAPIResources)
	s.mux.HandleFunc("GET /apis/coordination.k8s.io/", s.handleCoordinationAPIResources)
	s.mux.HandleFunc("GET /apis/events.k8s.io/", s.handleEventsAPIResources)
	s.mux.HandleFunc("GET /apis/rbac.authorization.k8s.io/", s.handleRBACAPIResources)

	// Core v1: Namespaces
	gvrNamespaces := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}
	s.mux.HandleFunc("POST /api/v1/namespaces", s.handleCreate(gvrNamespaces, func() runtime.Object { return &corev1.Namespace{} }))
	s.mux.HandleFunc("GET /api/v1/namespaces/{name}", s.handleGet(gvrNamespaces))
	s.mux.HandleFunc("GET /api/v1/namespaces", s.handleList(gvrNamespaces, func() runtime.Object { return &corev1.NamespaceList{} }))
	s.mux.HandleFunc("DELETE /api/v1/namespaces/{name}", s.handleDelete(gvrNamespaces))
	s.mux.HandleFunc("GET /api/v1/namespaces/watch", s.handleWatch(gvrNamespaces))

	// Core v1: Pods
	gvrPods := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
	s.mux.HandleFunc("POST /api/v1/namespaces/{namespace}/pods", s.handleCreate(gvrPods, func() runtime.Object { return &corev1.Pod{} }))
	s.mux.HandleFunc("GET /api/v1/namespaces/{namespace}/pods/{name}", s.handleGet(gvrPods))
	s.mux.HandleFunc("GET /api/v1/namespaces/{namespace}/pods", s.handleList(gvrPods, func() runtime.Object { return &corev1.PodList{} }))
	s.mux.HandleFunc("DELETE /api/v1/namespaces/{namespace}/pods/{name}", s.handleDelete(gvrPods))
	s.mux.HandleFunc("GET /api/v1/namespaces/{namespace}/pods/watch", s.handleWatch(gvrPods))

	// Apps v1: Deployments
	gvrDeployments := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	s.mux.HandleFunc("POST /apis/apps/v1/namespaces/{namespace}/deployments", s.handleCreate(gvrDeployments, func() runtime.Object { return &appsv1.Deployment{} }))
	s.mux.HandleFunc("GET /apis/apps/v1/namespaces/{namespace}/deployments/{name}", s.handleGet(gvrDeployments))
	s.mux.HandleFunc("GET /apis/apps/v1/namespaces/{namespace}/deployments", s.handleList(gvrDeployments, func() runtime.Object { return &appsv1.DeploymentList{} }))
	s.mux.HandleFunc("DELETE /apis/apps/v1/namespaces/{namespace}/deployments/{name}", s.handleDelete(gvrDeployments))
	s.mux.HandleFunc("GET /apis/apps/v1/namespaces/{namespace}/deployments/watch", s.handleWatch(gvrDeployments))

	// Coordination v1: Leases
	gvrLeases := schema.GroupVersionResource{Group: "coordination.k8s.io", Version: "v1", Resource: "leases"}
	s.mux.HandleFunc("POST /apis/coordination.k8s.io/v1/namespaces/{namespace}/leases", s.handleCreate(gvrLeases, func() runtime.Object { return &coordinationv1.Lease{} }))
	s.mux.HandleFunc("GET /apis/coordination.k8s.io/v1/namespaces/{namespace}/leases/{name}", s.handleGet(gvrLeases))
	s.mux.HandleFunc("GET /apis/coordination.k8s.io/v1/namespaces/{namespace}/leases", s.handleList(gvrLeases, func() runtime.Object { return &coordinationv1.LeaseList{} }))
	s.mux.HandleFunc("DELETE /apis/coordination.k8s.io/v1/namespaces/{namespace}/leases/{name}", s.handleDelete(gvrLeases))
	s.mux.HandleFunc("GET /apis/coordination.k8s.io/v1/namespaces/{namespace}/leases/watch", s.handleWatch(gvrLeases))

	// Events v1: Events
	gvrEvents := schema.GroupVersionResource{Group: "events.k8s.io", Version: "v1", Resource: "events"}
	s.mux.HandleFunc("POST /apis/events.k8s.io/v1/namespaces/{namespace}/events", s.handleCreate(gvrEvents, func() runtime.Object { return &eventsv1.Event{} }))
	s.mux.HandleFunc("GET /apis/events.k8s.io/v1/namespaces/{namespace}/events/{name}", s.handleGet(gvrEvents))
	s.mux.HandleFunc("GET /apis/events.k8s.io/v1/namespaces/{namespace}/events", s.handleList(gvrEvents, func() runtime.Object { return &eventsv1.EventList{} }))
	s.mux.HandleFunc("DELETE /apis/events.k8s.io/v1/namespaces/{namespace}/events/{name}", s.handleDelete(gvrEvents))
	s.mux.HandleFunc("GET /apis/events.k8s.io/v1/namespaces/{namespace}/events/watch", s.handleWatch(gvrEvents))

	// RBAC v1: Roles
	gvrRoles := schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"}
	s.mux.HandleFunc("POST /apis/rbac.authorization.k8s.io/v1/namespaces/{namespace}/roles", s.handleCreate(gvrRoles, func() runtime.Object { return &rbacv1.Role{} }))
	s.mux.HandleFunc("GET /apis/rbac.authorization.k8s.io/v1/namespaces/{namespace}/roles/{name}", s.handleGet(gvrRoles))
	s.mux.HandleFunc("GET /apis/rbac.authorization.k8s.io/v1/namespaces/{namespace}/roles", s.handleList(gvrRoles, func() runtime.Object { return &rbacv1.RoleList{} }))
	s.mux.HandleFunc("DELETE /apis/rbac.authorization.k8s.io/v1/namespaces/{namespace}/roles/{name}", s.handleDelete(gvrRoles))
	s.mux.HandleFunc("GET /apis/rbac.authorization.k8s.io/v1/namespaces/{namespace}/roles/watch", s.handleWatch(gvrRoles))
}

// handleAPIGroups returns the list of supported API groups
func (s *KAPISimulator) handleAPIGroups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	writeJsonResponse(r, w, &apiGroupList)
}

// handleCoreAPIVersions returns the list of versions for the core API group
func (s *KAPISimulator) handleCoreAPIVersions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJsonResponse(r, w, &apiVersions)
}

// handleCoreAPIResources returns the list of supported resources for the core API group
func (s *KAPISimulator) handleCoreAPIResources(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	writeJsonResponse(r, w, &coreAPIResourceList)
}

// handleAppsAPIResources returns the list of supported resources for the apps API group
func (s *KAPISimulator) handleAppsAPIResources(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	writeJsonResponse(r, w, &appsAPIResourceList)
}

// handleCoordinationAPIResources returns the list of supported resources for the coordination.k8s.io API group
func (s *KAPISimulator) handleCoordinationAPIResources(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	apiResourceList := &metav1.APIResourceList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "APIResourceList",
			APIVersion: "v1",
		},
		GroupVersion: "coordination.k8s.io/v1",
		APIResources: []metav1.APIResource{
			{
				Name:       "leases",
				Kind:       "Lease",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "get", "list", "watch"},
			},
		},
	}

	writeJsonResponse(r, w, &apiResourceList)
}

// handleEventsAPIResources returns the list of supported resources for the events.k8s.io API group
func (s *KAPISimulator) handleEventsAPIResources(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJsonResponse(r, w, &eventsAPIResourceList)
}

// handleRBACAPIResources returns the list of supported resources for the rbac.authorization.k8s.io API group
func (s *KAPISimulator) handleRBACAPIResources(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	apiResourceList := &metav1.APIResourceList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "APIResourceList",
			APIVersion: "v1",
		},
		GroupVersion: "rbac.authorization.k8s.io/v1",
		APIResources: []metav1.APIResource{
			{
				Name:       "roles",
				Kind:       "Role",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "get", "list", "watch"},
			},
		},
	}

	writeJsonResponse(r, w, &apiResourceList)
}
func (s *KAPISimulator) handleCreate(gvr schema.GroupVersionResource, newObj func() runtime.Object) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := s.getStore(gvr)
		if store == nil {
			http.Error(w, "Resource not supported", http.StatusNotFound)
			return
		}

		obj := newObj()
		if err := json.NewDecoder(r.Body).Decode(obj); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		namespace := r.PathValue("namespace")
		name := obj.(metav1.Object).GetName()
		if name == "" {
			http.Error(w, "Name is required", http.StatusBadRequest)
			return
		}

		obj.(metav1.Object).SetNamespace(namespace)
		obj.(metav1.Object).SetResourceVersion(s.nextResourceVersion(gvr))

		if err := store.Add(obj); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.broadcastEvent(gvr, namespace, watch.Event{Type: watch.Added, Object: obj})
		writeJsonResponse(r, w, obj)
	}
}

func (s *KAPISimulator) handleGet(gvr schema.GroupVersionResource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := s.getStore(gvr)
		if store == nil {
			http.Error(w, "Resource not supported", http.StatusNotFound)
			return
		}

		namespace := r.PathValue("namespace")
		name := r.PathValue("name")
		key := namespace + "/" + name

		if obj, exists, err := store.Get(key); err == nil && exists {
			writeJsonResponse(r, w, obj)
			return
		}
		http.Error(w, "Not Found", http.StatusNotFound)
	}
}

func (s *KAPISimulator) handleDelete(gvr schema.GroupVersionResource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := s.getStore(gvr)
		if store == nil {
			http.Error(w, "Resource not supported", http.StatusNotFound)
			return
		}

		namespace := r.PathValue("namespace")
		name := r.PathValue("name")
		key := namespace + "/" + name

		if obj, exists, err := store.Get(key); err == nil && exists {
			runtimeObj, ok := obj.(runtime.Object)
			if !ok {
				http.Error(w, "Stored object is not a runtime.Object", http.StatusInternalServerError)
				return
			}
			runtimeObj.(metav1.Object).SetResourceVersion(s.nextResourceVersion(gvr))
			if err := store.Delete(key); err == nil {
				s.broadcastEvent(gvr, namespace, watch.Event{Type: watch.Deleted, Object: runtimeObj})
				w.WriteHeader(http.StatusOK)
				return
			}
		}
		http.Error(w, "Not Found", http.StatusNotFound)
	}
}

func (s *KAPISimulator) handleList(gvr schema.GroupVersionResource, newList func() runtime.Object) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := s.getStore(gvr)
		if store == nil {
			http.Error(w, "Resource not supported", http.StatusNotFound)
			return
		}

		namespace := r.PathValue("namespace")
		items := store.List()
		list := newList()

		s.storeLock.Lock()
		currentVersion := strconv.FormatInt(s.versions[gvr], 10)
		s.storeLock.Unlock()

		switch list := list.(type) {
		case *corev1.PodList:
			list.TypeMeta = metav1.TypeMeta{Kind: "PodList", APIVersion: "v1"}
			list.ListMeta = metav1.ListMeta{ResourceVersion: currentVersion}
			list.Items = make([]corev1.Pod, 0)
			for _, item := range items {
				pod := item.(*corev1.Pod)
				if pod.Namespace == namespace {
					list.Items = append(list.Items, *pod)
				}
			}
		case *appsv1.DeploymentList:
			list.TypeMeta = metav1.TypeMeta{Kind: "DeploymentList", APIVersion: "apps/v1"}
			list.ListMeta = metav1.ListMeta{ResourceVersion: currentVersion}
			list.Items = make([]appsv1.Deployment, 0)
			for _, item := range items {
				deploy := item.(*appsv1.Deployment)
				if deploy.Namespace == namespace {
					list.Items = append(list.Items, *deploy)
				}
			}
		case *coordinationv1.LeaseList:
			list.TypeMeta = metav1.TypeMeta{Kind: "LeaseList", APIVersion: "coordination.k8s.io/v1"}
			list.ListMeta = metav1.ListMeta{ResourceVersion: currentVersion}
			list.Items = make([]coordinationv1.Lease, 0)
			for _, item := range items {
				lease := item.(*coordinationv1.Lease)
				if lease.Namespace == namespace {
					list.Items = append(list.Items, *lease)
				}
			}
		case *eventsv1.EventList:
			list.TypeMeta = metav1.TypeMeta{Kind: "EventList", APIVersion: "events.k8s.io/v1"}
			list.ListMeta = metav1.ListMeta{ResourceVersion: currentVersion}
			list.Items = make([]eventsv1.Event, 0)
			for _, item := range items {
				event := item.(*eventsv1.Event)
				if event.Namespace == namespace {
					list.Items = append(list.Items, *event)
				}
			}
		case *rbacv1.RoleList:
			list.TypeMeta = metav1.TypeMeta{Kind: "RoleList", APIVersion: "rbac.authorization.k8s.io/v1"}
			list.ListMeta = metav1.ListMeta{ResourceVersion: "rbac.authorization.k8s.io/v1"}
			list.Items = make([]rbacv1.Role, 0)
			for _, item := range items {
				role := item.(*rbacv1.Role)
				if role.Namespace == namespace {
					list.Items = append(list.Items, *role)
				}
			}
		}
		writeJsonResponse(r, w, list)
	}
}

func (s *KAPISimulator) handleWatch(gvr schema.GroupVersionResource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := s.getStore(gvr)
		if store == nil {
			http.Error(w, "Resource not supported", http.StatusNotFound)
			return
		}

		namespace := r.PathValue("namespace")
		resourceVersion := r.URL.Query().Get("resourceVersion")
		var startVersion int64
		if resourceVersion != "" {
			var err error
			startVersion, err = strconv.ParseInt(resourceVersion, 10, 64)
			if err != nil {
				http.Error(w, "Invalid resourceVersion", http.StatusBadRequest)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Transfer-Encoding", "chunked")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming not supported", http.StatusInternalServerError)
			return
		}

		s.watchLock.Lock()
		if _, ok := s.watchers[gvr]; !ok {
			s.watchers[gvr] = make(map[string]chan watch.Event)
		}
		ch := make(chan watch.Event, 10)
		s.watchers[gvr][namespace] = ch
		s.watchLock.Unlock()

		s.storeLock.Lock()
		currentVersion := s.versions[gvr]
		s.storeLock.Unlock()

		if resourceVersion == "" || startVersion < currentVersion {
			items := store.List()
			for _, item := range items {
				if obj, ok := item.(metav1.Object); ok && obj.GetNamespace() == namespace {
					rv, _ := strconv.ParseInt(obj.GetResourceVersion(), 10, 64)
					if resourceVersion == "" || rv > startVersion {
						runtimeObj, ok := item.(runtime.Object)
						if !ok {
							continue // Skip invalid objects
						}
						event := watch.Event{Type: watch.Added, Object: runtimeObj}
						if err := json.NewEncoder(w).Encode(event); err != nil {
							http.Error(w, fmt.Sprintf("Failed to encode watch event: %v", err), http.StatusInternalServerError)
							s.watchLock.Lock()
							delete(s.watchers[gvr], namespace)
							close(ch)
							s.watchLock.Unlock()
							return
						}
						flusher.Flush()
					}
				}
			}
		}

		for {
			select {
			case event := <-ch:
				rv, _ := strconv.ParseInt(event.Object.(metav1.Object).GetResourceVersion(), 10, 64)
				if resourceVersion == "" || rv > startVersion {
					if err := json.NewEncoder(w).Encode(event); err != nil {
						http.Error(w, fmt.Sprintf("Failed to encode watch event: %v", err), http.StatusInternalServerError)
						s.watchLock.Lock()
						delete(s.watchers[gvr], namespace)
						close(ch)
						s.watchLock.Unlock()
						return
					}
					flusher.Flush()
				}
			case <-r.Context().Done():
				s.watchLock.Lock()
				delete(s.watchers[gvr], namespace)
				close(ch)
				s.watchLock.Unlock()
				return
			case <-time.After(30 * time.Second):
				s.watchLock.Lock()
				delete(s.watchers[gvr], namespace)
				close(ch)
				s.watchLock.Unlock()
				return
			}
		}
	}
}

// broadcastEvent sends an event to all watchers for a GVR and namespace
func (s *KAPISimulator) broadcastEvent(gvr schema.GroupVersionResource, namespace string, event watch.Event) {
	s.watchLock.Lock()
	defer s.watchLock.Unlock()

	if nsWatchers, ok := s.watchers[gvr]; ok {
		if ch, ok := nsWatchers[namespace]; ok {
			select {
			case ch <- event:
			default:
			}
		}
	}
}

// generateKubeconfig creates a simple kubeconfig file at /tmp/kapisim.yaml
func (s *KAPISimulator) generateKubeconfig() error {
	kubeconfig := `apiVersion: v1
kind: Config
clusters:
- cluster:
    insecure-skip-tls-verify: true
    server: http://localhost:8080
  name: kapisim
contexts:
- context:
    cluster: kapisim
    user: kapisim-user
    namespace: default
  name: kapisim-context
current-context: kapisim-context
users:
- name: kapisim-user
  user: {}
`
	return os.WriteFile("/tmp/kapisim.yaml", []byte(kubeconfig), 0644)
}

func (s *KAPISimulator) getStore(gvr schema.GroupVersionResource) cache.Store {
	return s.stores[gvr]
}

// nextResourceVersion increments and returns the next version for a GVR
func (s *KAPISimulator) nextResourceVersion(gvr schema.GroupVersionResource) string {
	s.storeLock.Lock()
	defer s.storeLock.Unlock()
	s.versions[gvr]++
	return strconv.FormatInt(s.versions[gvr], 10)
}

var coreAPIResourceList = metav1.APIResourceList{
	TypeMeta: metav1.TypeMeta{
		Kind: "APIResourceList",
	},
	GroupVersion: "v1",
	APIResources: []metav1.APIResource{
		{
			Name:               "pods",
			SingularName:       "pod",
			ShortNames:         []string{"po"},
			Kind:               "Pod",
			Namespaced:         true,
			Verbs:              []string{"create", "delete", "get", "list", "watch"},
			Categories:         []string{"all"},
			StorageVersionHash: "pod111",
		},
		{
			Name:               "namespaces",
			SingularName:       "namespace",
			ShortNames:         []string{"ns"},
			Kind:               "Namespace",
			Namespaced:         false,
			Verbs:              []string{"create", "delete", "get", "list", "watch"},
			StorageVersionHash: "ns111",
		},
	},
}

var apiGroupList = &metav1.APIGroupList{
	TypeMeta: metav1.TypeMeta{
		Kind:       "APIGroupList",
		APIVersion: "v1",
	},
	Groups: []metav1.APIGroup{
		{
			Name: "apps",
			Versions: []metav1.GroupVersionForDiscovery{
				{
					GroupVersion: "apps/v1",
					Version:      "v1",
				},
			},
			PreferredVersion: metav1.GroupVersionForDiscovery{
				GroupVersion: "apps/v1",
				Version:      "v1",
			},
		},
		{
			Name: "coordination.k8s.io",
			Versions: []metav1.GroupVersionForDiscovery{
				{
					GroupVersion: "coordination.k8s.io/v1",
					Version:      "v1",
				},
			},
			PreferredVersion: metav1.GroupVersionForDiscovery{
				GroupVersion: "coordination.k8s.io/v1",
				Version:      "v1",
			},
		},
		{
			Name: "events.k8s.io",
			Versions: []metav1.GroupVersionForDiscovery{
				{
					GroupVersion: "events.k8s.io/v1",
					Version:      "v1",
				},
			},
			PreferredVersion: metav1.GroupVersionForDiscovery{
				GroupVersion: "events.k8s.io/v1",
				Version:      "v1",
			},
		},
		{
			Name: "rbac.authorization.k8s.io",
			Versions: []metav1.GroupVersionForDiscovery{
				{
					GroupVersion: "rbac.authorization.k8s.io/v1",
					Version:      "v1",
				},
			},
			PreferredVersion: metav1.GroupVersionForDiscovery{
				GroupVersion: "rbac.authorization.k8s.io/v1",
				Version:      "v1",
			},
		},
	},
}

var apiVersions = metav1.APIVersions{
	TypeMeta: metav1.TypeMeta{
		Kind: "APIVersions",
	},
	Versions: []string{"v1"},
	ServerAddressByClientCIDRs: []metav1.ServerAddressByClientCIDR{
		{
			ClientCIDR:    "0.0.0.0/0",
			ServerAddress: "10.53.208.76:8080",
		},
	},
}
var appsAPIResourceList = metav1.APIResourceList{
	TypeMeta: metav1.TypeMeta{
		Kind:       "APIResourceList",
		APIVersion: "v1",
	},
	GroupVersion: "apps/v1",
	APIResources: []metav1.APIResource{
		{
			Name:       "deployments",
			ShortNames: []string{"deploy"},
			Kind:       "Deployment",
			Namespaced: true,
			Verbs:      []string{"create", "delete", "get", "list", "watch"},
		},
		{
			Name:       "replicasets",
			ShortNames: []string{"rs"},
			Kind:       "ReplicaSet",
			Namespaced: true,
			Verbs:      []string{"create", "delete", "get", "list", "watch"},
		},
	},
}

var eventsAPIResourceList = metav1.APIResourceList{
	TypeMeta: metav1.TypeMeta{
		Kind:       "APIResourceList",
		APIVersion: "v1",
	},
	GroupVersion: "events.k8s.io/v1",
	APIResources: []metav1.APIResource{
		{
			Name:       "events",
			ShortNames: []string{"ev"},
			Kind:       "Event",
			Namespaced: true,
			Verbs:      []string{"create", "delete", "get", "list", "watch"},
		},
	},
}

// writeJsonResponse sets Content-Type to application/json  and encodes the object to the response writer.
func writeJsonResponse(r *http.Request, w http.ResponseWriter, obj any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(obj); err != nil {
		klog.Errorf("Failed to encode %s response: %v", r.URL.Path, err)
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
	}
}
