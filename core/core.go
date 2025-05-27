package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/elankath/minkapi/api"
	"github.com/elankath/minkapi/core/configtmpl"
	"github.com/elankath/minkapi/core/podutil"
	"github.com/elankath/minkapi/core/typeinfo"
	"github.com/go-logr/logr"
	jsonpatch "gopkg.in/evanphx/json-patch.v4"
	"io"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kjson "k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	"log/slog"
	"net"
	"net/http"
	"reflect"
	"strconv"
	"sync"
	"text/template"
	"time"
)

var _ api.MinKAPIAccess = (*InMemoryKAPI)(nil)

// InMemoryKAPI holds the in-memory stores, watch channels, and version tracking for simple implementation of api.MinKAPIAccess
type InMemoryKAPI struct {
	cfg            api.MinKAPIConfig
	log            logr.Logger
	scheme         *runtime.Scheme
	mux            *http.ServeMux
	storeLock      sync.Mutex
	stores         map[schema.GroupVersionResource]cache.Store
	versions       map[schema.GroupVersionResource]int64                       // GVR -> latest resourceVersion
	watchers       map[schema.GroupVersionResource]map[string]chan watch.Event // GVR->namespace->event channel
	watchLock      sync.Mutex
	kubeConfigTmpl *template.Template
	listenerAddr   net.Addr
	server         *http.Server
}

func NewInMemoryMinKAPI(appCtx context.Context, cfg api.MinKAPIConfig, log logr.Logger) (*InMemoryKAPI, error) {
	mux := http.NewServeMux()
	stores := map[schema.GroupVersionResource]cache.Store{}
	for _, sd := range typeinfo.SupportedDescriptors {
		stores[sd.GVR] = cache.NewStore(cache.MetaNamespaceKeyFunc)
	}
	s := &InMemoryKAPI{
		cfg:      cfg,
		log:      log,
		stores:   stores,
		watchers: make(map[schema.GroupVersionResource]map[string]chan watch.Event, 100),
		versions: make(map[schema.GroupVersionResource]int64, 100),
		scheme:   typeinfo.SupportedScheme,
		mux:      mux,
		server: &http.Server{
			Addr:    fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
			Handler: mux,
			BaseContext: func(_ net.Listener) context.Context {
				return appCtx
			},
		},
	}
	s.registerRoutes()
	return s, nil
}

func (s *InMemoryKAPI) GetMux() *http.ServeMux {
	return s.mux
}

// Start begins the HTTP server
func (s *InMemoryKAPI) Start() error {
	// We do this because we want the bind address
	listener, err := net.Listen("tcp", s.server.Addr)
	if err != nil {
		return fmt.Errorf("%w: cannot listen on TCP Address %q: %w", api.ErrStartFailed, s.server.Addr, err)
	}
	s.listenerAddr = listener.Addr()
	err = configtmpl.GenKubeConfig(configtmpl.KubeConfigParams{
		KubeConfigPath: s.cfg.KubeConfigPath,
		URL:            "http://" + s.listenerAddr.String(),
	})
	if err != nil {
		return fmt.Errorf("%w: %w", api.ErrStartFailed, err)
	}
	s.log.Info("kubeconfig generated", "path", s.cfg.KubeConfigPath)
	s.log.Info(fmt.Sprintf("%s service listening", api.ProgramName), "address", s.server.Addr)
	if err := s.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("%w: %w", api.ErrServiceFailed, err)
	}
	return nil
}

// Shutdown cleans up resources and shuts down the HTTP server
func (s *InMemoryKAPI) Shutdown(ctx context.Context) error {
	//s.closeWatches()
	return s.server.Shutdown(ctx)
}

func (s *InMemoryKAPI) closeWatches() {
	s.watchLock.Lock()
	defer s.watchLock.Unlock()
	for gvr, nsWatchers := range s.watchers {
		for ns, ch := range nsWatchers {
			delete(nsWatchers, ns)
			close(ch)
		}
		delete(s.watchers, gvr)
	}
	return
}

func (s *InMemoryKAPI) registerRoutes() {
	s.mux.HandleFunc("GET /api", s.handleAPIVersions)
	s.mux.HandleFunc("GET /apis", s.handleAPIGroups)

	// Core API Group and Other API Groups
	s.registerAPIGroups()

	for _, d := range typeinfo.SupportedDescriptors {
		s.registerResourceRoutes(d)
	}

}

func (s *InMemoryKAPI) registerAPIGroups() {

	// Core API
	s.mux.HandleFunc("GET /api/v1/", s.handleAPIResources(typeinfo.SupportedCoreAPIResourceList))

	// API groups
	for _, apiList := range typeinfo.SupportedGroupAPIResourceLists {
		route := fmt.Sprintf("GET /apis/%s/", apiList.APIResources[0].Group)
		s.mux.HandleFunc(route, s.handleAPIResources(apiList))
	}
}

func (s *InMemoryKAPI) registerResourceRoutes(d typeinfo.Descriptor) {
	g := d.GVK.Group
	r := d.GVR.Resource
	if d.GVK.Group == "" {
		s.mux.HandleFunc(fmt.Sprintf("POST /api/v1/namespaces/{namespace}/%s", r), s.handleCreate(d))
		s.mux.HandleFunc(fmt.Sprintf("GET /api/v1/namespaces/{namespace}/%s", r), s.handleListOrWatch(d))
		s.mux.HandleFunc(fmt.Sprintf("GET /api/v1/namespaces/{namespace}/%s/{name}", r), s.handleGet(d))
		s.mux.HandleFunc(fmt.Sprintf("PATCH /api/v1/namespaces/{namespace}/%s/{name}/status", r), s.handlePatchStatus(d))
		s.mux.HandleFunc(fmt.Sprintf("DELETE /api/v1/namespaces/{namespace}/%s/{name}", r), s.handleDelete(d))

		if d.Kind == typeinfo.PodsDescriptor.Kind {
			s.mux.HandleFunc("POST /api/v1/namespaces/{namespace}/pods/{name}/binding", s.handleCreatePodBinding)
		}

		s.mux.HandleFunc(fmt.Sprintf("POST /api/v1/%s", r), s.handleCreate(d))
		s.mux.HandleFunc(fmt.Sprintf("GET /api/v1/%s", r), s.handleListOrWatch(d))
		s.mux.HandleFunc(fmt.Sprintf("GET /api/v1/%s/{name}", r), s.handleGet(d))
		s.mux.HandleFunc(fmt.Sprintf("PATCH /api/v1/%s/{name}", r), s.handlePatch(d))
		s.mux.HandleFunc(fmt.Sprintf("DELETE /api/v1/%s/{name}", r), s.handleDelete(d))
	} else {
		s.mux.HandleFunc(fmt.Sprintf("POST /apis/%s/v1/namespaces/{namespace}/%s", g, r), s.handleCreate(d))
		s.mux.HandleFunc(fmt.Sprintf("GET /apis/%s/v1/namespaces/{namespace}/%s", g, r), s.handleListOrWatch(d))
		s.mux.HandleFunc(fmt.Sprintf("GET /apis/%s/v1/namespaces/{namespace}/%s/{name}", g, r), s.handleGet(d))
		s.mux.HandleFunc(fmt.Sprintf("PATCH /apis/%s/v1/namespaces/{namespace}/%s/{name}", g, r), s.handlePatch(d))
		s.mux.HandleFunc(fmt.Sprintf("DELETE /apis/%s/v1/namespaces/{namespace}/%s/{name}", g, r), s.handleDelete(d))

		s.mux.HandleFunc(fmt.Sprintf("POST /apis/%s/v1/%s", g, r), s.handleCreate(d))
		s.mux.HandleFunc(fmt.Sprintf("GET /apis/%s/v1/%s", g, r), s.handleListOrWatch(d))
		s.mux.HandleFunc(fmt.Sprintf("GET /apis/%s/v1/%s/{name}", g, r), s.handleGet(d))
		s.mux.HandleFunc(fmt.Sprintf("DELETE /apis/%s/v1/%s/{name}", g, r), s.handleDelete(d))
	}
}

// handleAPIGroups returns the list of supported API groups
func (s *InMemoryKAPI) handleAPIGroups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJsonResponse(s.log, w, r, &typeinfo.SupportedAPIGroups)
}

// handleAPIVersions returns the list of versions for the core API group
func (s *InMemoryKAPI) handleAPIVersions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJsonResponse(s.log, w, r, &typeinfo.SupportedAPIVersions)
}

func (s *InMemoryKAPI) handleAPIResources(apiResourceList metav1.APIResourceList) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJsonResponse(s.log, w, r, apiResourceList)
	}
}

func (s *InMemoryKAPI) handleCreate(d typeinfo.Descriptor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := s.getStoreOrWriteError(d.GVR, w, r)
		if store == nil {
			return
		}
		var obj metav1.Object
		var err error
		obj, err = d.CreateObject()

		if err != nil {
			err = fmt.Errorf("cannot create object from GVK %q: %v", d.GVK, err)
			s.handleInternalServerError(w, r, err)
			return
		}

		if !s.readBodyIntoObj(w, r, obj) {
			return
		}

		namespace := r.PathValue("namespace")
		if obj.GetNamespace() == "" {
			namespace = GetObjectName(r, d).Namespace
			obj.SetNamespace(namespace)
		} else {
			namespace = obj.GetNamespace()
		}
		name := obj.GetName()
		namePrefix := obj.GetGenerateName()
		if name == "" {
			if namePrefix == "" {
				err = fmt.Errorf("missing both name and generateName in request for creating object of GVK %q in %q namespace", d.GVK, namespace)
				s.handleBadRequest(w, r, err)
				return
			}
			name = typeinfo.GenerateName(namePrefix)
		}
		obj.SetName(name)

		obj.SetResourceVersion(s.nextResourceVersion(d.GVR))
		createTimestamp := obj.GetCreationTimestamp()
		if (&createTimestamp).IsZero() { // only set creationTimestamp if not already set.
			obj.SetCreationTimestamp(metav1.Time{Time: time.Now()})
		}
		if obj.GetUID() == "" {
			obj.SetUID(uuid.NewUUID())
		}

		err = store.Add(obj)
		if err != nil {
			err = fmt.Errorf("error adding object %q to store: %v", cache.NewObjectName(namespace, name), err)
			s.handleInternalServerError(w, r, err)
			return
		}
		go s.broadcastEvent(d, namespace, watch.Event{Type: watch.Added, Object: obj.(runtime.Object)})
		writeJsonResponse(s.log, w, r, obj)
	}
}

func (s *InMemoryKAPI) handleGet(d typeinfo.Descriptor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := s.getStoreOrWriteError(d.GVR, w, r)
		if store == nil {
			return
		}
		key := GetObjectKey(r, d)
		obj, exists, err := store.GetByKey(key)
		if err != nil {
			s.handleInternalServerError(w, r, fmt.Errorf("cannot get obj by key %q: %w", key, err))
			return
		}
		if !exists {
			s.handleNotFound(w, r, d.GVR, key)
			return
		}
		writeJsonResponse(s.log, w, r, obj)
	}
}

func (s *InMemoryKAPI) handleDelete(d typeinfo.Descriptor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := s.getStoreOrWriteError(d.GVR, w, r)
		if store == nil {
			return
		}

		objName := GetObjectName(r, d)
		obj, exists, err := store.GetByKey(objName.String())
		if err != nil {
			s.handleInternalServerError(w, r, fmt.Errorf("cannot get object with key %q from store: %w", objName, err))
			return
		}
		if !exists {
			s.handleNotFound(w, r, d.GVR, objName.String())
			return
		}
		runtimeObj, ok := obj.(runtime.Object)
		if !ok {
			s.handleInternalServerError(w, r, fmt.Errorf("stored object with key %q is not runtime.Object", objName))
			return
		}
		metaV1Obj, err := meta.Accessor(runtimeObj)
		if err != nil {
			s.handleInternalServerError(w, r, fmt.Errorf("stored object with key %q is not metav1.Object: %w", objName, err))
			return
		}
		metaV1Obj.SetDeletionTimestamp(&metav1.Time{Time: time.Time{}})
		err = store.Delete(obj)
		if err != nil {
			s.handleInternalServerError(w, r, fmt.Errorf("cannot delete object with key %q: %w", objName, err))
			return
		}
		go s.broadcastEvent(d, objName.Namespace, watch.Event{Type: watch.Deleted, Object: runtimeObj})
		status := metav1.Status{
			TypeMeta: metav1.TypeMeta{ //No idea why this is explicitly needed just for this payload, but kubectl complains
				Kind:       "Status",
				APIVersion: "v1",
			},
			Status: metav1.StatusSuccess,
			Details: &metav1.StatusDetails{
				Name: objName.String(),
				Kind: d.GVR.GroupResource().Resource,
				UID:  metaV1Obj.GetUID(),
			},
		}
		writeJsonResponse(s.log, w, r, &status)
	}
}

func (s *InMemoryKAPI) handleListOrWatch(d typeinfo.Descriptor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		isWatch := query.Get("watch")
		var delegate http.HandlerFunc
		if isWatch == "true" {
			delegate = s.handleWatch(d)
		} else {
			delegate = s.handleList(d)
		}
		delegate.ServeHTTP(w, r)
	}
}
func (s *InMemoryKAPI) handleList(d typeinfo.Descriptor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := s.getStoreOrWriteError(d.GVR, w, r)
		if store == nil {
			return
		}
		namespace := r.PathValue("namespace")
		items := store.List()
		currentVersionStr := fmt.Sprintf("%d", s.getCurrentVersion(d.GVR))
		objItems, err := FromAnySlice(items)
		if err != nil {
			statusErr := apierrors.NewInternalError(err)
			writeStatusError(s.log, w, r, statusErr)
			return
		}
		list, err := createList(s.log, d, namespace, currentVersionStr, objItems)
		if err != nil {
			statusErr := apierrors.NewInternalError(err)
			writeStatusError(s.log, w, r, statusErr)
			return
		}
		writeJsonResponse(s.log, w, r, list)
	}
}
func (s *InMemoryKAPI) handlePatch(d typeinfo.Descriptor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := s.getStoreOrWriteError(d.GVR, w, r)
		if store == nil {
			return
		}
		key := GetObjectKey(r, d)
		obj, exists, err := store.GetByKey(key)
		if err != nil {
			err = fmt.Errorf("cannot get obj by key %q: %w", key, err)
			s.handleInternalServerError(w, r, err)
			return
		}
		if !exists {
			s.handleNotFound(w, r, d.GVR, key)
			return
		}
		contentType := r.Header.Get("Content-Type")
		if contentType != "application/strategic-merge-patch+json" && contentType != "application/merge-patch+json" {
			err = fmt.Errorf("unsupported content type %q for obj %q", contentType, key)
			s.handleInternalServerError(w, r, err)
			return
		}
		patchData, err := io.ReadAll(r.Body)
		if err != nil {
			statusErr := apierrors.NewInternalError(err)
			writeStatusError(s.log, w, r, statusErr)
			return
		}
		err = patchObject(obj.(runtime.Object), key, contentType, patchData)
		if err != nil {
			err = fmt.Errorf("failed to atch obj %q: %w", key, err)
			s.handleInternalServerError(w, r, err)
			return
		}
		metaObj := obj.(metav1.Object)
		metaObj.SetResourceVersion(s.nextResourceVersion(d.GVR))
		err = store.Update(obj)
		if err != nil {
			err = fmt.Errorf("cannot update object %q in store: %w", key, err)
			s.handleInternalServerError(w, r, err)
			return
		}
		writeJsonResponse(s.log, w, r, obj)
	}
}
func (s *InMemoryKAPI) handlePatchStatus(d typeinfo.Descriptor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := s.getStoreOrWriteError(d.GVR, w, r)
		if store == nil {
			return
		}

		key := GetObjectKey(r, d)
		obj, exists, err := store.GetByKey(key)
		if err != nil {
			err = fmt.Errorf("cannot get obj by key %q: %w", key, err)
			s.handleInternalServerError(w, r, err)
			return
		}
		if !exists {
			s.handleNotFound(w, r, d.GVR, key)
			return
		}
		contentType := r.Header.Get("Content-Type")
		if contentType != "application/strategic-merge-patch+json" {
			err = fmt.Errorf("unsupported content type %q for obj %q", contentType, key)
			s.handleInternalServerError(w, r, err)
			return
		}

		patchData, err := io.ReadAll(r.Body)
		if err != nil {
			err = fmt.Errorf("failed to read patch body for obj %q", key)
			s.handleInternalServerError(w, r, err)
			return
		}
		runtimeObj := obj.(runtime.Object)
		err = patchStatus(runtimeObj, key, patchData)
		if err != nil {
			err = fmt.Errorf("failed to atch status for obj %q: %w", key, err)
			s.handleInternalServerError(w, r, err)
			return
		}
		metaObj := obj.(metav1.Object)
		metaObj.SetResourceVersion(s.nextResourceVersion(d.GVR))
		err = store.Update(obj)
		if err != nil {
			err = fmt.Errorf("cannot update object %q in store: %w", key, err)
			s.handleInternalServerError(w, r, err)
			return
		}
		writeJsonResponse(s.log, w, r, obj)
	}
}

func (s *InMemoryKAPI) handleWatch(d typeinfo.Descriptor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := s.getStoreOrWriteError(d.GVR, w, r)
		if store == nil {
			return
		}

		var ok bool
		var startVersion, currentVersion int64
		var namespace string

		namespace = r.PathValue("namespace")
		startVersion, ok = getParseResourceVersion(w, r)
		if !ok {
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Transfer-Encoding", "chunked")
		flusher := getFlusher(w)
		if flusher == nil {
			return
		}
		currentVersion = s.getCurrentVersion(d.GVR)
		if startVersion < currentVersion {
			ok = s.sendPendingAddEvents(w, r, namespace, startVersion, store.List())
			if !ok {
				return
			}
		}

		ch := s.createWatchChan(d, namespace)
		watchTimeout := s.cfg.WatchTimeout
		for {
			select {
			case event := <-ch:
				metaObj, err := meta.Accessor(event.Object)
				if err != nil {
					s.log.Error(err, "could not process event object", "eventObject", event.Object)
					continue
				}
				rv, _ := strconv.ParseInt(metaObj.GetResourceVersion(), 10, 64)
				if rv > startVersion {
					eventJson, err := buildWatchEventJson(s.log, &event)
					if err != nil {
						s.removeWatch(d.GVR, namespace, ch)
						err = fmt.Errorf("cannot  encode watch %q event for object name %q, namespace %q, resourceVersion %q: %w",
							event.Type, metaObj.GetName(), metaObj.GetNamespace(), rv, err)
						s.handleInternalServerError(w, r, err)
						return
					}
					_, _ = fmt.Fprintln(w, eventJson)
					flusher.Flush()
				}
			case <-r.Context().Done():
				s.watchLock.Lock()
				delete(s.watchers[d.GVR], namespace)
				close(ch)
				s.watchLock.Unlock()
				return
			case <-time.After(watchTimeout):
				s.watchLock.Lock()
				delete(s.watchers[d.GVR], namespace)
				close(ch)
				s.watchLock.Unlock()
				return
			}
		}
	}
}

// handleCreatePodBinding is meant to handle creation for a Pod binding.
// Ex: POST http://localhost:8080/api/v1/namespaces/default/pods/a-mc6zl/binding
// This endpoint is invoked by the scheduler, and it is expected that the API Server sets the `pod.Spec.NodeName`
//
// Example Payload
// {"kind":"Binding","apiVersion":"v1","metadata":{"name":"a-p4r2l","namespace":"default","uid":"b8124ee8-a0c7-4069-930d-fc5e901675d3"},"target":{"kind":"Node","name":"a-kl827"}}
func (s *InMemoryKAPI) handleCreatePodBinding(w http.ResponseWriter, r *http.Request) {
	d := typeinfo.PodsDescriptor
	store := s.getStoreOrWriteError(d.GVR, w, r)
	if store == nil {
		return
	}
	binding := corev1.Binding{}
	if !s.readBodyIntoObj(w, r, &binding) {
		return
	}
	key := GetObjectKey(r, d)
	obj, exists, err := store.GetByKey(key)
	if err != nil {
		s.handleInternalServerError(w, r, fmt.Errorf("cannot get obj by key %q: %w", key, err))
		return
	}
	if !exists {
		s.handleNotFound(w, r, d.GVR, key)
		return
	}
	pod := obj.(*corev1.Pod)
	pod.Spec.NodeName = binding.Target.Name
	podutil.UpdatePodCondition(&pod.Status, &corev1.PodCondition{
		Type:   corev1.PodScheduled,
		Status: corev1.ConditionTrue,
	})
	s.log.V(4).Info("assigned pod to node", "podName", pod.Name, "podNamespace", pod.Namespace, "nodeName", pod.Spec.NodeName)
	err = store.Update(obj)
	if err != nil {
		err = fmt.Errorf("cannot update object %q in store: %w", key, err)
		s.handleInternalServerError(w, r, err)
		return
	}
	// Return {"kind":"Status","apiVersion":"v1","metadata":{},"status":"Success","code":201}
	statusOK := &metav1.Status{
		TypeMeta: metav1.TypeMeta{Kind: "Status"},
		Status:   metav1.StatusSuccess,
		Code:     http.StatusCreated,
	}
	writeJsonResponse(s.log, w, r, statusOK)
}

func (s *InMemoryKAPI) sendPendingAddEvents(w http.ResponseWriter, r *http.Request, namespace string, startVersion int64, items []any) (ok bool) {
	flusher := getFlusher(w)
	if flusher == nil {
		return
	}
	for _, item := range items {
		var obj metav1.Object
		var rv int64
		obj, err := meta.Accessor(item)
		if err != nil {
			s.log.Error(err, "cannot access object metadata", "item", item)
			continue
		}
		if namespace != "" && obj.GetNamespace() != namespace {
			continue
		}
		rv, err = parseObjectResourceVersion(obj)
		if err != nil {
			s.handleInternalServerError(w, r, err)
			return
		}
		if rv <= startVersion {
			continue
		}
		event := watch.Event{Type: watch.Added, Object: item.(runtime.Object)}
		eventJson, err := buildWatchEventJson(s.log, &event)
		if err != nil {
			err = fmt.Errorf("failed to encode watch event for obj %q, ns %q, rv: %d: %w", obj.GetName(), obj.GetNamespace(), rv, err)
			s.handleInternalServerError(w, r, err)
			return
		}
		_, _ = fmt.Fprintln(w, eventJson)
		flusher.Flush()
	}
	ok = true
	return
}

func (s *InMemoryKAPI) removeWatch(gvr schema.GroupVersionResource, namespace string, ch chan watch.Event) {
	s.watchLock.Lock()
	defer s.watchLock.Unlock()
	delete(s.watchers[gvr], namespace)
	close(ch)
}

func (s *InMemoryKAPI) getCurrentVersion(gvr schema.GroupVersionResource) int64 {
	s.storeLock.Lock()
	currentVersion := s.versions[gvr]
	s.storeLock.Unlock()
	return currentVersion
}

func (s *InMemoryKAPI) createWatchChan(d typeinfo.Descriptor, namespace string) chan watch.Event {
	s.watchLock.Lock()
	defer s.watchLock.Unlock()
	if namespace == "" && d.APIResource.Namespaced {
		namespace = "default"
	}
	if _, ok := s.watchers[d.GVR]; !ok {
		s.watchers[d.GVR] = make(map[string]chan watch.Event)
	}
	ch := make(chan watch.Event, s.cfg.WatchQueueSize)
	s.watchers[d.GVR][namespace] = ch
	return ch
}

// broadcastEvent sends an event to all watchers for a GVR and namespace
func (s *InMemoryKAPI) broadcastEvent(d typeinfo.Descriptor, namespace string, event watch.Event) {
	s.watchLock.Lock()
	defer s.watchLock.Unlock()

	if nsWatchers, ok := s.watchers[d.GVR]; ok {
		if ch, ok := nsWatchers[namespace]; ok {
			select {
			case ch <- event:
			default:
				s.log.V(4).Info("cannot broadcast watch event", "namespace", namespace, "event", event)
			}
		}
	}
}

func (s *InMemoryKAPI) getStore(gvr schema.GroupVersionResource) cache.Store {
	return s.stores[gvr]
}

func (s *InMemoryKAPI) getStoreOrWriteError(gvr schema.GroupVersionResource, w http.ResponseWriter, r *http.Request) (store cache.Store) {
	store = s.getStore(gvr)
	if store == nil {
		slog.Info("no store initialized for GVR", "gvr", gvr)
		statusErr := apierrors.NewNotFound(gvr.GroupResource(), "STORE")
		w.WriteHeader(http.StatusNotFound)
		w.Header().Set("Content-Type", "application/json")
		writeJsonResponse(s.log, w, r, statusErr.ErrStatus)
	}
	return
}

// nextResourceVersion increments and returns the next version for a GVR
func (s *InMemoryKAPI) nextResourceVersion(gvr schema.GroupVersionResource) string {
	s.storeLock.Lock()
	defer s.storeLock.Unlock()
	s.versions[gvr]++
	return strconv.FormatInt(s.versions[gvr], 10)
}

func (s *InMemoryKAPI) currResourceVersion(gvr schema.GroupVersionResource) string {
	s.storeLock.Lock()
	defer s.storeLock.Unlock()
	return strconv.FormatInt(s.versions[gvr], 10)
}

// createList creates the Kind specific list (PodList, etc.) and returns the same as a runtime.Object.
// Consider using unstructured.Unstructured here for generic handling or reflection?
func createList(log logr.Logger, d typeinfo.Descriptor, namespace, currVersionStr string, items []runtime.Object) (runtime.Object, error) {
	typesMap := typeinfo.SupportedScheme.KnownTypes(d.GVR.GroupVersion())
	listType, ok := typesMap[string(d.ListKind)]
	if !ok {
		return nil, runtime.NewNotRegisteredErrForKind(typeinfo.SupportedScheme.Name(), d.ListGVK)
	}
	ptr := reflect.New(listType) // *PodList
	listObjVal := ptr.Elem()     // PodList
	typeMetaVal := listObjVal.FieldByName("TypeMeta")
	if !typeMetaVal.IsValid() {
		return nil, fmt.Errorf("failed to get TypeMeta field on %v", listObjVal)
	}
	listMetaVal := listObjVal.FieldByName("ListMeta")
	if !listMetaVal.IsValid() {
		return nil, fmt.Errorf("failed to get ListMeta field on %v", listObjVal)
	}
	typeMetaVal.Set(reflect.ValueOf(metav1.TypeMeta{
		Kind:       string(d.ListKind),
		APIVersion: d.GVR.GroupVersion().String(),
	}))
	listMetaVal.Set(reflect.ValueOf(metav1.ListMeta{
		ResourceVersion: currVersionStr,
	}))

	itemsField := listObjVal.FieldByName("Items")
	if !itemsField.IsValid() || !itemsField.CanSet() || itemsField.Kind() != reflect.Slice {
		return nil, fmt.Errorf("list object for %q does not have a settable slice field named Items", d.GVK.Kind)
	}
	itemType := itemsField.Type().Elem() // e.g., v1.Pod
	resultSlice := reflect.MakeSlice(itemsField.Type(), 0, len(items))

	for _, obj := range items {
		metaV1Obj, err := meta.Accessor(obj)
		if err != nil {
			log.Error(err, "cannot access meta object", "obj", obj)
			continue
		}
		if namespace != "" && metaV1Obj.GetNamespace() != namespace {
			continue
		}
		val := reflect.ValueOf(obj)
		if val.Kind() != reflect.Ptr || val.IsNil() {
			return nil, fmt.Errorf("element for kind %q is not a non-nil pointer: %T", d.Kind, obj)
		}
		if val.Elem().Type() != itemType {
			return nil, fmt.Errorf("type mismatch, list kind %q expects items of type %v, but got %v", d.ListKind, itemType, val.Elem().Type())
		}
		resultSlice = reflect.Append(resultSlice, val.Elem()) // append the dereferenced struct
	}
	itemsField.Set(resultSlice)
	listObj := ptr.Interface().(runtime.Object)
	return listObj, nil
}

func writeStatusError(log logr.Logger, w http.ResponseWriter, r *http.Request, statusError *apierrors.StatusError) {
	w.WriteHeader(int(statusError.ErrStatus.Code))
	writeJsonResponse(log, w, r, statusError.ErrStatus)
}

// writeJsonResponse sets Content-Type to application/json  and encodes the object to the response writer.
func writeJsonResponse(log logr.Logger, w http.ResponseWriter, r *http.Request, obj any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(obj); err != nil {
		log.Error(err, "cannot  encode response", "method", r.Method, "requestURI", r.RequestURI, "obj", obj)
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
	}
}

func getParseResourceVersion(w http.ResponseWriter, r *http.Request) (resourceVersion int64, ok bool) {
	paramValue := r.URL.Query().Get("resourceVersion")
	if paramValue == "" {
		ok = true
		resourceVersion = 0
		return
	}
	resourceVersion, err := parseResourceVersion(paramValue)
	if err != nil {
		http.Error(w, "Invalid resourceVersion", http.StatusBadRequest)
		return
	}
	ok = true
	return
}
func parseObjectResourceVersion(obj metav1.Object) (resourceVersion int64, err error) {
	resourceVersion, err = parseResourceVersion(obj.GetResourceVersion())
	if err != nil {
		err = fmt.Errorf("failed to parse resource version %q for object %q in ns %q: %w", obj.GetResourceVersion(), obj.GetName(), obj.GetNamespace(), err)
	}
	return
}
func parseResourceVersion(rvStr string) (resourceVersion int64, err error) {
	if rvStr != "" {
		resourceVersion, err = strconv.ParseInt(rvStr, 10, 64)
	}
	return
}
func getFlusher(w http.ResponseWriter) http.Flusher {
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Transfer-Encoding", "chunked")
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return nil
	}
	return flusher
}
func buildWatchEventJson(log logr.Logger, event *watch.Event) (string, error) {
	// NOTE: Simple Json serialization does NOT work due to bug in Watch struct
	//if err := json.NewEncoder(w).Encode(event); err != nil {
	//	http.Error(w, fmt.Sprintf("Failed to encode watch event: %v", err), http.StatusInternalServerError)
	//	s.removeWatch(gvr, namespace, ch)
	//	return
	//}
	data, err := kjson.Marshal(event.Object)
	if err != nil {
		log.Error(err, "cannot encode watch event", "event", event)
		return "", err
	}
	payload := fmt.Sprintf("{\"type\":\"%s\",\"object\":%s}", event.Type, string(data))
	return payload, nil
}

func (s *InMemoryKAPI) readBodyIntoObj(w http.ResponseWriter, r *http.Request, obj any) (ok bool) {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		s.handleBadRequest(w, r, err)
		ok = false
		return
	}
	if err := json.Unmarshal(data, obj); err != nil {
		err = fmt.Errorf("cannot unmarshal JSON for request %q: %w", r.RequestURI, err)
		s.handleBadRequest(w, r, err)
		ok = false
		return
	}
	ok = true
	return
}

func (s *InMemoryKAPI) handleInternalServerError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error(err, "internal server error", "method", r.Method, "requestURI", r.RequestURI)
	statusErr := apierrors.NewInternalError(err)
	w.WriteHeader(http.StatusInternalServerError)
	w.Header().Set("Content-Type", "application/json")
	writeJsonResponse(s.log, w, r, statusErr.ErrStatus)
}

func (s *InMemoryKAPI) handleNotFound(w http.ResponseWriter, r *http.Request, gr schema.GroupVersionResource, key string) {
	statusErr := apierrors.NewNotFound(gr.GroupResource(), key)
	s.log.Error(statusErr, "object not found", "key", key, "resource", gr.Resource)
	w.WriteHeader(http.StatusNotFound)
	w.Header().Set("Content-Type", "application/json")
	writeJsonResponse(s.log, w, r, statusErr.ErrStatus)
}

func (s *InMemoryKAPI) handleBadRequest(w http.ResponseWriter, r *http.Request, err error) {
	err = fmt.Errorf("cannot handle request %q: %w", r.Method+" "+r.RequestURI, err)
	s.log.Error(err, "bad request", "method", r.Method, "requestURI", r.RequestURI)
	statusErr := apierrors.NewBadRequest(err.Error())
	w.WriteHeader(http.StatusBadRequest)
	w.Header().Set("Content-Type", "application/json")
	writeJsonResponse(s.log, w, r, statusErr.ErrStatus)
}

func GetObjectName(r *http.Request, d typeinfo.Descriptor) cache.ObjectName {
	namespace := r.PathValue("namespace")
	if namespace == "" && d.APIResource.Namespaced {
		namespace = "default"
	}
	name := r.PathValue("name")
	return cache.NewObjectName(namespace, name)
}
func GetObjectKey(r *http.Request, d typeinfo.Descriptor) string {
	return GetObjectName(r, d).String()
}

func FromAnySlice(objs []any) ([]runtime.Object, error) {
	result := make([]runtime.Object, 0, len(objs))
	for _, item := range objs {
		obj, ok := item.(runtime.Object)
		if !ok {
			return nil, fmt.Errorf("element %T does not implement runtime.Object", item)
		}
		result = append(result, obj)
	}
	return result, nil
}

func patchObject(objPtr runtime.Object, key string, contentType string, patchBytes []byte) error {
	objValuePtr := reflect.ValueOf(objPtr)
	if objValuePtr.Kind() != reflect.Ptr || objValuePtr.IsNil() {
		return fmt.Errorf("object %q must be a non-nil pointer", key)
	}
	objInterface := objValuePtr.Interface()
	originalJSON, err := kjson.Marshal(objInterface)
	if err != nil {
		return fmt.Errorf("failed to marshal object %q: %w", key, err)
	}

	var patchedBytes []byte
	if contentType == "application/strategic-merge-patch+json" {
		patchedBytes, err = strategicpatch.StrategicMergePatch(originalJSON, patchBytes, objInterface)
		if err != nil {
			return fmt.Errorf("failed to apply strategic merge patch for object %q: %w", key, err)
		}
	} else if contentType == "application/merge-patch+json" {
		patchedBytes, err = jsonpatch.MergePatch(originalJSON, patchBytes)
		if err != nil {
			return fmt.Errorf("failed to apply merge-patch for object %q: %w", key, err)
		}
	} else {
		return fmt.Errorf("unsupported patch content type %q for object %q", contentType, key)
	}
	err = kjson.Unmarshal(patchedBytes, objInterface)
	if err != nil {
		return fmt.Errorf("failed to unmarshal patched JSON back into obj %q: %w", key, err)
	}
	return nil
}

func patchStatus(objPtr runtime.Object, key string, patch []byte) error {
	objValuePtr := reflect.ValueOf(objPtr)
	if objValuePtr.Kind() != reflect.Ptr || objValuePtr.IsNil() {
		return fmt.Errorf("object %q must be a non-nil pointer", key)
	}
	statusField := objValuePtr.Elem().FieldByName("Status")
	if !statusField.IsValid() {
		return fmt.Errorf("object %q of type %T has no Status field", key, objPtr)
	}

	var patchWrapper map[string]json.RawMessage
	err := json.Unmarshal(patch, &patchWrapper)
	if err != nil {
		return fmt.Errorf("failed to parse patch for %q as JSON object: %w", key, err)
	}
	statusPatchRaw, ok := patchWrapper["status"]
	if !ok {
		return fmt.Errorf("patch for %q does not contain a 'status' key", key)
	}

	statusInterface := statusField.Interface()
	originalStatusJSON, err := kjson.Marshal(statusInterface)
	if err != nil {
		return fmt.Errorf("failed to marshal original status for object %q: %w", key, err)
	}
	patchedStatusJSON, err := strategicpatch.StrategicMergePatch(originalStatusJSON, statusPatchRaw, statusInterface)
	if err != nil {
		return fmt.Errorf("failed to apply strategic merge patch for object %q: %w", key, err)
	}

	newStatusVal := reflect.New(statusField.Type())
	newStatusPtr := newStatusVal.Interface()
	if err := json.Unmarshal(patchedStatusJSON, newStatusPtr); err != nil {
		return fmt.Errorf("failed to unmarshal patched status for object %q: %w", key, err)
	}
	statusField.Set(newStatusVal.Elem())
	return nil
}
