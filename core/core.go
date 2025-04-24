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
	schedulingv1 "k8s.io/api/scheduling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/validation"
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
	scheme, err := RegisterSchemes()
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	stores := map[schema.GroupVersionResource]cache.Store{}
	for _, gvr := range SupportedGVRs {
		stores[gvr] = cache.NewStore(cache.MetaNamespaceKeyFunc)
	}
	s := &KAPISimulator{
		stores:   stores,
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

	s.mux.HandleFunc(fmt.Sprintf("GET /apis/%s/", appsv1.GroupName), s.handleAppsAPIResources)
	s.mux.HandleFunc(fmt.Sprintf("GET /apis/%s/", coordinationv1.GroupName), s.handleCoordinationAPIResources)
	s.mux.HandleFunc(fmt.Sprintf("GET /apis/%s/", eventsv1.GroupName), s.handleEventsAPIResources)
	s.mux.HandleFunc(fmt.Sprintf("GET /apis/%s/", rbacv1.GroupName), s.handleRBACAPIResources)
	s.mux.HandleFunc(fmt.Sprintf("GET /apis/%s/", schedulingv1.GroupName), s.handleSchedulingAPIResources)

	// Core v1: Namespaces
	s.mux.HandleFunc("POST /api/v1/namespaces", s.handleCreate(GVRNamespaces, func() runtime.Object { return &corev1.Namespace{} }))
	s.mux.HandleFunc("GET /api/v1/namespaces", s.handleListOrWatch(GVRNamespaces, func() runtime.Object { return &corev1.NamespaceList{} }))
	s.mux.HandleFunc("GET /api/v1/namespaces/{name}", s.handleGet(GVRNamespaces))
	s.mux.HandleFunc("DELETE /api/v1/namespaces/{name}", s.handleDelete(GVRNamespaces))

	// Core v1: Nodes
	s.mux.HandleFunc("POST /api/v1/nodes", s.handleCreate(GVRNodes, func() runtime.Object { return &corev1.Node{} }))
	s.mux.HandleFunc("GET /api/v1/nodes", s.handleListOrWatch(GVRNodes, func() runtime.Object { return &corev1.NodeList{} }))
	s.mux.HandleFunc("DELETE /api/v1/nodes/{name}", s.handleDelete(GVRNodes))
	s.mux.HandleFunc("GET /api/v1/nodes/{name}", s.handleGet(GVRNodes))

	// Core v1: Pods
	s.mux.HandleFunc("POST /api/v1/namespaces/{namespace}/pods", s.handleCreate(GVRPods, func() runtime.Object { return &corev1.Pod{} }))
	s.mux.HandleFunc("GET /api/v1/namespaces/{namespace}/pods", s.handleListOrWatch(GVRPods, func() runtime.Object { return &corev1.PodList{} }))
	s.mux.HandleFunc("GET /api/v1/namespaces/{namespace}/pods/{name}", s.handleGet(GVRPods))
	s.mux.HandleFunc("DELETE /api/v1/namespaces/{namespace}/pods/{name}", s.handleDelete(GVRPods))

	// Scheduling v1: PriorityClasses
	s.mux.HandleFunc(fmt.Sprintf("POST /apis/%s/v1/namespaces/{namespace}/%s", schedulingv1.GroupName, GVRPriorityClasses.Resource), s.handleCreate(GVRPriorityClasses, func() runtime.Object { return &schedulingv1.PriorityClass{} }))
	s.mux.HandleFunc(fmt.Sprintf("GET /apis/%s/v1/namespaces/{namespace}/%s", schedulingv1.GroupName, GVRPriorityClasses.Resource), s.handleListOrWatch(GVRPriorityClasses, func() runtime.Object { return &schedulingv1.PriorityClassList{} }))
	s.mux.HandleFunc(fmt.Sprintf("GET /apis/%s/v1/namespaces/{namespace}/%s/{name}", schedulingv1.GroupName, GVRPriorityClasses.Resource), s.handleGet(GVRPriorityClasses))
	s.mux.HandleFunc(fmt.Sprintf("DELETE /apis/%s/v1/namespaces/{namespace}/%s/{name}", schedulingv1.GroupName, GVRPriorityClasses.Resource), s.handleDelete(GVRPriorityClasses))

	// Apps v1: Deployments
	s.mux.HandleFunc("POST /apis/apps/v1/namespaces/{namespace}/deployments", s.handleCreate(GVRDeployments, func() runtime.Object { return &appsv1.Deployment{} }))
	s.mux.HandleFunc("GET /apis/apps/v1/namespaces/{namespace}/deployments/{name}", s.handleGet(GVRDeployments))
	s.mux.HandleFunc("GET /apis/apps/v1/namespaces/{namespace}/deployments", s.handleListOrWatch(GVRDeployments, func() runtime.Object { return &appsv1.DeploymentList{} }))
	s.mux.HandleFunc("DELETE /apis/apps/v1/namespaces/{namespace}/deployments/{name}", s.handleDelete(GVRDeployments))

	// Coordination v1: Leases
	s.mux.HandleFunc("POST /apis/coordination.k8s.io/v1/namespaces/{namespace}/leases", s.handleCreate(GVRLeases, func() runtime.Object { return &coordinationv1.Lease{} }))
	s.mux.HandleFunc("GET /apis/coordination.k8s.io/v1/namespaces/{namespace}/leases/{name}", s.handleGet(GVRLeases))
	s.mux.HandleFunc("GET /apis/coordination.k8s.io/v1/namespaces/{namespace}/leases", s.handleListOrWatch(GVRLeases, func() runtime.Object { return &coordinationv1.LeaseList{} }))
	s.mux.HandleFunc("DELETE /apis/coordination.k8s.io/v1/namespaces/{namespace}/leases/{name}", s.handleDelete(GVRLeases))

	// Events v1: Events
	s.mux.HandleFunc("POST /apis/events.k8s.io/v1/namespaces/{namespace}/events", s.handleCreate(GVREvents, func() runtime.Object { return &eventsv1.Event{} }))
	s.mux.HandleFunc("GET /apis/events.k8s.io/v1/namespaces/{namespace}/events/{name}", s.handleGet(GVREvents))
	s.mux.HandleFunc("GET /apis/events.k8s.io/v1/namespaces/{namespace}/events", s.handleListOrWatch(GVREvents, func() runtime.Object { return &eventsv1.EventList{} }))
	s.mux.HandleFunc("DELETE /apis/events.k8s.io/v1/namespaces/{namespace}/events/{name}", s.handleDelete(GVREvents))

	// RBAC v1: Roles
	s.mux.HandleFunc("POST /apis/rbac.authorization.k8s.io/v1/namespaces/{namespace}/roles", s.handleCreate(GVRRoles, func() runtime.Object { return &rbacv1.Role{} }))
	s.mux.HandleFunc("GET /apis/rbac.authorization.k8s.io/v1/namespaces/{namespace}/roles/{name}", s.handleGet(GVRRoles))
	s.mux.HandleFunc("GET /apis/rbac.authorization.k8s.io/v1/namespaces/{namespace}/roles", s.handleListOrWatch(GVRRoles, func() runtime.Object { return &rbacv1.RoleList{} }))
	s.mux.HandleFunc("DELETE /apis/rbac.authorization.k8s.io/v1/namespaces/{namespace}/roles/{name}", s.handleDelete(GVRRoles))
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
		GroupVersion: coordinationv1.SchemeGroupVersion.String(),
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
		GroupVersion: rbacv1.SchemeGroupVersion.String(),
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

// handleSchedulingAPIResources returns the list of supported resources for the "scheduling.k8s.io" API group
func (s *KAPISimulator) handleSchedulingAPIResources(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	apiResourceList := &metav1.APIResourceList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "APIResourceList",
			APIVersion: "v1",
		},
		GroupVersion: schedulingv1.SchemeGroupVersion.String(),
		APIResources: []metav1.APIResource{
			{
				Name:       "priorityclasses",
				Kind:       "PriorityClass",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "get", "list"},
			},
		},
	}

	writeJsonResponse(r, w, &apiResourceList)
}
func (s *KAPISimulator) handleCreate(gvr schema.GroupVersionResource, newObjFn func() runtime.Object) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := s.getStore(gvr)
		if store == nil {
			http.Error(w, "Resource not supported", http.StatusNotFound)
			return
		}

		obj := newObjFn().(metav1.Object)
		if err := json.NewDecoder(r.Body).Decode(obj); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		namespace := getNamespace(r)
		name := obj.GetName()
		namePrefix := obj.GetGenerateName()
		if name == "" {
			if namePrefix == "" {
				http.Error(w, "Name or GenerateName is required", http.StatusBadRequest)
				return
			}
			name = generateName(namePrefix)
		}

		obj.SetName(name)
		obj.SetNamespace(namespace)
		obj.SetResourceVersion(s.nextResourceVersion(gvr))
		obj.SetCreationTimestamp(metav1.Now())

		if err := store.Add(obj); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.broadcastEvent(gvr, namespace, watch.Event{Type: watch.Added, Object: obj.(runtime.Object)})
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

		namespace := getNamespace(r)
		name := r.PathValue("name")
		key := namespace + "/" + name

		obj, exists, err := store.GetByKey(key)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !exists {
			http.Error(w, "Not Found", http.StatusNotFound)
			return

		}
		writeJsonResponse(r, w, obj)
	}
}

func (s *KAPISimulator) handleDelete(gvr schema.GroupVersionResource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := s.getStore(gvr)
		if store == nil {
			http.Error(w, "Resource not supported", http.StatusNotFound)
			return
		}

		namespace := getNamespace(r)
		name := r.PathValue("name")
		key := namespace + "/" + name

		obj, exists, err := store.GetByKey(key)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !exists {
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		}
		runtimeObj, ok := obj.(runtime.Object)
		if !ok {
			http.Error(w, "Stored object is not a runtime.Object", http.StatusInternalServerError)
			return
		}
		metav1Obj, ok := obj.(metav1.Object)
		if !ok {
			http.Error(w, "Stored object is not a metav1.Object", http.StatusInternalServerError)
			return
		}
		err = store.Delete(obj)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.broadcastEvent(gvr, namespace, watch.Event{Type: watch.Deleted, Object: runtimeObj})
		status := metav1.Status{
			Status:  metav1.StatusSuccess,
			Message: "",
			Reason:  "",
			Details: &metav1.StatusDetails{
				Name: key,
				Kind: "nodes",
				UID:  metav1Obj.GetUID(),
			},
			Code: 0,
		}
		w.WriteHeader(http.StatusOK)
		writeJsonResponse(r, w, &status)
	}
}

func (s *KAPISimulator) handleListOrWatch(gvr schema.GroupVersionResource, newListFn func() runtime.Object) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		isWatch := query.Get("watch")
		var delegate http.HandlerFunc
		if isWatch == "true" {
			delegate = s.handleWatch(gvr)
		} else {
			delegate = s.handleList(gvr, newListFn)
		}
		delegate.ServeHTTP(w, r)
	}
}
func (s *KAPISimulator) handleList(gvr schema.GroupVersionResource, newListFn func() runtime.Object) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := s.getStore(gvr)
		if store == nil {
			http.Error(w, "Resource not supported", http.StatusNotFound)
			return
		}

		namespace := getNamespace(r)
		items := store.List()
		currentVersion := s.nextResourceVersion(gvr)

		list := createList(currentVersion, items, namespace, newListFn)
		writeJsonResponse(r, w, list)
	}
}

func createList(currentVersion string, items []any, namespace string, newListFn func() runtime.Object) runtime.Object {
	list := newListFn()
	switch list := list.(type) {
	case *corev1.PodList:
		list.TypeMeta = metav1.TypeMeta{Kind: "PodList", APIVersion: GVRPods.GroupVersion().String()}
		list.ListMeta = metav1.ListMeta{ResourceVersion: currentVersion}
		list.Items = make([]corev1.Pod, 0, len(items))
		for _, item := range items {
			pod := item.(*corev1.Pod)
			if pod.Namespace == namespace {
				list.Items = append(list.Items, *pod)
			}
			pod.Status.Phase = corev1.PodPending
		}
	case *corev1.NamespaceList:
		list.TypeMeta = metav1.TypeMeta{Kind: "NamespaceList", APIVersion: GVRNamespaces.GroupVersion().String()}
		list.ListMeta = metav1.ListMeta{ResourceVersion: currentVersion}
		list.Items = make([]corev1.Namespace, 0, len(items))
		for _, item := range items {
			ns := item.(*corev1.Namespace)
			list.Items = append(list.Items, *ns)
		}
	case *corev1.NodeList:
		list.TypeMeta = metav1.TypeMeta{Kind: "NodeList", APIVersion: GVRNodes.GroupVersion().String()}
		list.ListMeta = metav1.ListMeta{ResourceVersion: currentVersion}
		list.Items = make([]corev1.Node, 0, len(items))
		for _, item := range items {
			node := item.(*corev1.Node)
			list.Items = append(list.Items, *node)
			node.Status.Phase = corev1.NodeRunning
		}
	case *appsv1.DeploymentList:
		list.TypeMeta = metav1.TypeMeta{Kind: "DeploymentList", APIVersion: GVRDeployments.GroupVersion().String()}
		list.ListMeta = metav1.ListMeta{ResourceVersion: currentVersion}
		list.Items = make([]appsv1.Deployment, 0, len(items))
		for _, item := range items {
			deploy := item.(*appsv1.Deployment)
			if deploy.Namespace == namespace {
				list.Items = append(list.Items, *deploy)
			}
		}
	case *coordinationv1.LeaseList:
		list.TypeMeta = metav1.TypeMeta{Kind: "LeaseList", APIVersion: GVRLeases.GroupVersion().String()}
		list.ListMeta = metav1.ListMeta{ResourceVersion: currentVersion}
		list.Items = make([]coordinationv1.Lease, 0, len(items))
		for _, item := range items {
			lease := item.(*coordinationv1.Lease)
			if lease.Namespace == namespace {
				list.Items = append(list.Items, *lease)
			}
		}
	case *eventsv1.EventList:
		list.TypeMeta = metav1.TypeMeta{Kind: "EventList", APIVersion: GVREvents.GroupVersion().String()}
		list.ListMeta = metav1.ListMeta{ResourceVersion: currentVersion}
		list.Items = make([]eventsv1.Event, 0, len(items))
		for _, item := range items {
			event := item.(*eventsv1.Event)
			if event.Namespace == namespace {
				list.Items = append(list.Items, *event)
			}
		}
	case *rbacv1.RoleList:
		list.TypeMeta = metav1.TypeMeta{Kind: "RoleList", APIVersion: GVRRoles.GroupVersion().String()}
		list.ListMeta = metav1.ListMeta{ResourceVersion: "rbac.authorization.k8s.io/v1"}
		list.Items = make([]rbacv1.Role, 0, len(items))
		for _, item := range items {
			role := item.(*rbacv1.Role)
			if role.Namespace == namespace {
				list.Items = append(list.Items, *role)
			}
		}
	case *schedulingv1.PriorityClassList:
		list.TypeMeta = metav1.TypeMeta{Kind: "PriorityClassList", APIVersion: GVRPriorityClasses.GroupVersion().String()}
		list.ListMeta = metav1.ListMeta{ResourceVersion: "rbac.authorization.k8s.io/v1"}
		list.Items = make([]schedulingv1.PriorityClass, 0, len(items))
		for _, item := range items {
			pc := item.(*schedulingv1.PriorityClass)
			if pc.Namespace == namespace {
				list.Items = append(list.Items, *pc)
			}
		}
	}
	return list
}

func (s *KAPISimulator) handleWatch(gvr schema.GroupVersionResource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := s.getStore(gvr)
		if store == nil {
			http.Error(w, "Resource not supported", http.StatusNotFound)
			return
		}

		namespace := getNamespace(r)
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

		ch := s.createWatchChan(gvr, namespace)
		currentVersion := s.getCurrentVersion(gvr)

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
						//_, _ = fmt.Fprintln(w)
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

func (s *KAPISimulator) getCurrentVersion(gvr schema.GroupVersionResource) int64 {
	s.storeLock.Lock()
	currentVersion := s.versions[gvr]
	s.storeLock.Unlock()
	return currentVersion
}

func (s *KAPISimulator) createWatchChan(gvr schema.GroupVersionResource, namespace string) chan watch.Event {
	s.watchLock.Lock()
	if _, ok := s.watchers[gvr]; !ok {
		s.watchers[gvr] = make(map[string]chan watch.Event)
	}
	ch := make(chan watch.Event, 10)
	s.watchers[gvr][namespace] = ch
	s.watchLock.Unlock()
	return ch
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

func (s *KAPISimulator) currResourceVersion(gvr schema.GroupVersionResource) string {
	s.storeLock.Lock()
	defer s.storeLock.Unlock()
	return strconv.FormatInt(s.versions[gvr], 10)
}

// writeJsonResponse sets Content-Type to application/json  and encodes the object to the response writer.
func writeJsonResponse(r *http.Request, w http.ResponseWriter, obj any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(obj); err != nil {
		klog.Errorf("Failed to encode %s response: %v", r.URL.Path, err)
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
	}
}

func generateName(base string) string {
	const suffixLen = 5
	suffix := utilrand.String(suffixLen)
	max := validation.DNS1123SubdomainMaxLength // 253 for subdomains; use DNS1123LabelMaxLength (63) if you need stricter
	if len(base)+len(suffix) > max {
		base = base[:max-len(suffix)]
	}
	return base + suffix
}

func getNamespace(r *http.Request) (ns string) {
	ns = r.PathValue("namespace")
	if ns == "" {
		ns = "default"
	}
	return
}

func getParseResourceVersion(r *http.Request) (resourceVersion int64, err error) {
	paramValue := r.URL.Query().Get("resourceVersion")
	return parseResourceVersion(paramValue)
}
func parseResourceVersion(rvStr string) (resourceVersion int64, err error) {
	if rvStr != "" {
		resourceVersion, err = strconv.ParseInt(rvStr, 10, 64)
	}
	return
}
