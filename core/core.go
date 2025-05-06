package core

import (
	"context"
	"fmt"
	"github.com/elankath/kapisim/core/typeinfo"
	"io"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"sync"
	"time"
)

// Simulator holds the in-memory stores, watch channels, and version tracking
type Simulator struct {
	scheme    *runtime.Scheme
	mux       *http.ServeMux
	storeLock sync.Mutex
	stores    map[schema.GroupVersionResource]cache.Store
	versions  map[schema.GroupVersionResource]int64 // GVR -> latest resourceVersion
	watchers  map[schema.GroupVersionResource]map[string]chan watch.Event
	watchLock sync.Mutex
	server    *http.Server
}

func NewKAPISimulator() (*Simulator, error) {
	mux := http.NewServeMux()
	stores := map[schema.GroupVersionResource]cache.Store{}
	for _, sd := range typeinfo.SupportedDescriptors {
		stores[sd.GVR] = cache.NewStore(cache.MetaNamespaceKeyFunc)
	}
	s := &Simulator{
		stores:   stores,
		watchers: make(map[schema.GroupVersionResource]map[string]chan watch.Event),
		versions: make(map[schema.GroupVersionResource]int64),
		scheme:   typeinfo.SupportedScheme,
		mux:      mux,
		server:   &http.Server{Addr: ":8080", Handler: mux},
	}
	s.registerRoutes()
	if err := s.generateKubeconfig(); err != nil {
		return nil, fmt.Errorf("failed to generate kubeconfig: %w", err)
	}
	return s, nil
}

func (s *Simulator) GetMux() *http.ServeMux {
	return s.mux
}

// Start begins the HTTP server
func (s *Simulator) Start() error {
	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server failed: %v", err)
	}
	return nil
}

// Shutdown cleans up resources and shuts down the HTTP server
func (s *Simulator) Shutdown(ctx context.Context) error {
	s.closeWatches()
	return s.server.Shutdown(ctx)
}

func (s *Simulator) closeWatches() {
	s.watchLock.Lock()
	defer s.watchLock.Unlock()
	for gvr, nsWatchers := range s.watchers {
		for ns, ch := range nsWatchers {
			close(ch)
			delete(nsWatchers, ns)
		}
		delete(s.watchers, gvr)
	}
	return
}

//func (s *Simulator) registerRoutesForDescriptor(d typeinfo.Descriptor) {
//	var resPath string
//	if d.GVK.Group == "" {
//		resPath = "/api/v1/" + d.GVR.Resource
//	}
//	//Example: Core Path Pattern: "POST|GET /api/v1/namespaces"
//	//Example: Core Path Pattern: "GET|DELETE /api/v1/namespaces/{name}"
//	s.mux.HandleFunc(fmt.Sprintf("POST %s", resPath), s.handleCreate(typeinfo.NamespacesDescriptor))
//	s.mux.HandleFunc(fmt.Sprintf("GET %s", resPath), s.handleListOrWatch(typeinfo.NamespacesDescriptor))
//	//// Core v1: Namespaces
//	//s.mux.HandleFunc("POST /api/v1/namespaces", s.handleCreate(typeinfo.NamespacesDescriptor))
//	//s.mux.HandleFunc("GET /api/v1/namespaces", s.handleListOrWatch(GVRNamespaces))
//	//s.mux.HandleFunc("GET /api/v1/namespaces/{name}", s.handleGet(GVRNamespaces))
//	//s.mux.HandleFunc("DELETE /api/v1/namespaces/{name}", s.handleDelete(GVRNamespaces))
//}

func (s *Simulator) registerRoutes() {
	s.mux.HandleFunc("GET /api", s.handleAPIVersions)
	s.mux.HandleFunc("GET /apis", s.handleAPIGroups)

	// Core API Group and Other API Groups
	s.registerAPIGroups()

	for _, d := range typeinfo.SupportedDescriptors {
		s.registerResourceRoutes(d)
	}

}

func (s *Simulator) registerAPIGroups() {

	// Core API
	s.mux.HandleFunc("GET /api/v1/", s.handleAPIResources(typeinfo.SupportedCoreAPIResourceList))

	// API groups
	for _, apiList := range typeinfo.SupportedGroupAPIResourceLists {
		route := fmt.Sprintf("GET /apis/%s/", apiList.APIResources[0].Group)
		s.mux.HandleFunc(route, s.handleAPIResources(apiList))
	}
}

func (s *Simulator) registerResourceRoutes(d typeinfo.Descriptor) {
	g := d.GVK.Group
	r := d.GVR.Resource
	if d.GVK.Group == "" {
		s.mux.HandleFunc(fmt.Sprintf("POST /api/v1/namespaces/{namespace}/%s", r), s.handleCreate(d))
		s.mux.HandleFunc(fmt.Sprintf("GET /api/v1/namespaces/{namespace}/%s", r), s.handleListOrWatch(d))
		s.mux.HandleFunc(fmt.Sprintf("GET /api/v1/namespaces/{namespace}/%s/{name}", r), s.handleGet(d))
		s.mux.HandleFunc(fmt.Sprintf("DELETE /api/v1/namespaces/{namespace}/%s/{name}", r), s.handleDelete(d))

		s.mux.HandleFunc(fmt.Sprintf("POST /api/v1/%s", r), s.handleCreate(d))
		s.mux.HandleFunc(fmt.Sprintf("GET /api/v1/%s", r), s.handleListOrWatch(d))
		s.mux.HandleFunc(fmt.Sprintf("DELETE /api/v1/%s/{name}", r), s.handleDelete(d))
		s.mux.HandleFunc(fmt.Sprintf("GET /api/v1/%s/{name}", r), s.handleGet(d))
	} else {
		s.mux.HandleFunc(fmt.Sprintf("POST /apis/%s/v1/namespaces/{namespace}/%s", g, r), s.handleCreate(d))
		s.mux.HandleFunc(fmt.Sprintf("GET /apis/%s/v1/namespaces/{namespace}/%s", g, r), s.handleListOrWatch(d))
		s.mux.HandleFunc(fmt.Sprintf("GET /apis/%s/v1/namespaces/{namespace}/%s/{name}", g, r), s.handleGet(d))
		s.mux.HandleFunc(fmt.Sprintf("DELETE /apis/%s/v1/namespaces/{namespace}/%s/{name}", g, r), s.handleDelete(d))
		s.mux.HandleFunc(fmt.Sprintf("POST /apis/%s/v1/%s", g, r), s.handleCreate(d))
		s.mux.HandleFunc(fmt.Sprintf("GET /apis/%s/v1/%s", g, r), s.handleListOrWatch(d))
		s.mux.HandleFunc(fmt.Sprintf("GET /apis/%s/v1/%s/{name}", g, r), s.handleGet(d))
		s.mux.HandleFunc(fmt.Sprintf("DELETE /apis/%s/v1/%s/{name}", g, r), s.handleDelete(d))
	}
}

// handleAPIGroups returns the list of supported API groups
func (s *Simulator) handleAPIGroups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJsonResponse(w, r, &typeinfo.SupportedAPIGroups)
}

// handleAPIVersions returns the list of versions for the core API group
func (s *Simulator) handleAPIVersions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJsonResponse(w, r, &typeinfo.SupportedAPIVersions)
}

func (s *Simulator) handleAPIResources(apiResourceList metav1.APIResourceList) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJsonResponse(w, r, apiResourceList)
	}
}

func (s *Simulator) handleCreate(descriptor typeinfo.Descriptor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := s.getStoreOrWriteError(descriptor.GVR, w, r)
		if store == nil {
			return
		}
		var obj metav1.Object
		var err error
		obj, err = descriptor.CreateObject()

		if err != nil {
			klog.Errorf("Error creating object from GVK %q: %v", descriptor.GVK, err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if !readBodyIntoObj(w, r, obj) {
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
			name = typeinfo.GenerateName(namePrefix)
		}

		obj.SetName(name)
		obj.SetNamespace(namespace)
		obj.SetResourceVersion(s.nextResourceVersion(descriptor.GVR))
		obj.SetCreationTimestamp(metav1.Now())

		if err := store.Add(obj); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.broadcastEvent(descriptor.GVR, namespace, watch.Event{Type: watch.Added, Object: obj.(runtime.Object)})
		writeJsonResponse(w, r, obj)
	}
}

func (s *Simulator) handleGet(d typeinfo.Descriptor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := s.getStoreOrWriteError(d.GVR, w, r)
		if store == nil {
			return
		}

		key := GetObjectKey(r)
		obj, exists, err := store.GetByKey(key)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !exists {
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		}
		writeJsonResponse(w, r, obj)
	}
}

func (s *Simulator) handleDelete(d typeinfo.Descriptor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := s.getStoreOrWriteError(d.GVR, w, r)
		if store == nil {
			return
		}

		on := GetObjectName(r)
		obj, exists, err := store.GetByKey(on.String())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !exists {
			statusErr := apierrors.NewNotFound(d.GVR.GroupResource(), on.String())
			writeStatusError(w, r, statusErr)
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
		s.broadcastEvent(d.GVR, on.Namespace, watch.Event{Type: watch.Deleted, Object: runtimeObj})
		status := metav1.Status{
			TypeMeta: metav1.TypeMeta{ //No idea why this is needed, but kubectl complains
				Kind:       "Status",
				APIVersion: "v1",
			},
			Status: metav1.StatusSuccess,
			Details: &metav1.StatusDetails{
				Name: on.String(),
				Kind: d.GVR.GroupResource().Resource,
				UID:  metav1Obj.GetUID(),
			},
		}
		writeJsonResponse(w, r, &status)
	}
}

func (s *Simulator) handleListOrWatch(d typeinfo.Descriptor) http.HandlerFunc {
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
func (s *Simulator) handleList(d typeinfo.Descriptor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := s.getStoreOrWriteError(d.GVR, w, r)
		if store == nil {
			return
		}
		namespace := getNamespace(r)
		items := store.List()
		currentVersionStr := fmt.Sprintf("%d", s.getCurrentVersion(d.GVR))
		objItems, err := FromAnySlice(items)
		if err != nil {
			statusErr := apierrors.NewInternalError(err)
			writeStatusError(w, r, statusErr)
			return
		}
		list, err := createList(d, namespace, currentVersionStr, objItems)
		if err != nil {
			statusErr := apierrors.NewInternalError(err)
			writeStatusError(w, r, statusErr)
			return
		}
		writeJsonResponse(w, r, list)
	}
}

// createList creates the Kind specific list (PodList, etc) and returns the same as a runtime.Object.
// Consider using unstructured.Unstructured here for generic handling or reflection?
func createList(d typeinfo.Descriptor, namespace, currVersionStr string, items []runtime.Object) (runtime.Object, error) {
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

func (s *Simulator) handleWatch(d typeinfo.Descriptor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := s.getStoreOrWriteError(d.GVR, w, r)
		if store == nil {
			return
		}

		var ok bool
		var startVersion, currentVersion int64
		var namespace string

		namespace = getNamespace(r)
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
			ok = sendPendingAddEvents(w, r, namespace, startVersion, store.List())
			if !ok {
				return
			}
		}

		ch := s.createWatchChan(d.GVR, namespace)
		watchTimeout := 2 * time.Minute
		for {
			select {
			case event := <-ch:
				rv, _ := strconv.ParseInt(event.Object.(metav1.Object).GetResourceVersion(), 10, 64)
				if rv > startVersion {
					eventJson, err := buildWatchEventJson(&event)
					if err != nil {
						http.Error(w, fmt.Sprintf("Failed to encode watch event: %v", err), http.StatusInternalServerError)
						s.removeWatch(d.GVR, namespace, ch)
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

func sendPendingAddEvents(w http.ResponseWriter, r *http.Request, namespace string, startVersion int64, items []any) (ok bool) {
	flusher := getFlusher(w)
	if flusher == nil {
		return
	}
	for _, item := range items {
		var obj metav1.Object
		var rv int64
		obj, ok = item.(metav1.Object)
		if !ok || obj.GetNamespace() != namespace {
			continue
		}
		rv, err := parseObjectResourceVersion(obj)
		if err != nil {
			handleInternalServerError(w, r, err)
			return
		}
		if rv <= startVersion {
			continue
		}
		event := watch.Event{Type: watch.Added, Object: item.(runtime.Object)}
		eventJson, err := buildWatchEventJson(&event)
		if err != nil {
			err = fmt.Errorf("failed to encode watch event for obj %q, ns %q, rv: %d: %w", obj.GetName(), obj.GetNamespace(), rv, err)
			handleInternalServerError(w, r, err)
			return
		}
		_, _ = fmt.Fprintln(w, eventJson)
		flusher.Flush()
	}
	ok = true
	return
}

func handleInternalServerError(w http.ResponseWriter, r *http.Request, err error) {
	klog.Error(err)
	statusErr := apierrors.NewInternalError(err)
	w.WriteHeader(http.StatusInternalServerError)
	w.Header().Set("Content-Type", "application/json")
	writeJsonResponse(w, r, statusErr.ErrStatus)
}

func (s *Simulator) removeWatch(gvr schema.GroupVersionResource, namespace string, ch chan watch.Event) {
	s.watchLock.Lock()
	defer s.watchLock.Unlock()
	delete(s.watchers[gvr], namespace)
	close(ch)
}

func (s *Simulator) getCurrentVersion(gvr schema.GroupVersionResource) int64 {
	s.storeLock.Lock()
	currentVersion := s.versions[gvr]
	s.storeLock.Unlock()
	return currentVersion
}

func (s *Simulator) createWatchChan(gvr schema.GroupVersionResource, namespace string) chan watch.Event {
	s.watchLock.Lock()
	defer s.watchLock.Unlock()
	if _, ok := s.watchers[gvr]; !ok {
		s.watchers[gvr] = make(map[string]chan watch.Event)
	}
	ch := make(chan watch.Event, 10)
	s.watchers[gvr][namespace] = ch
	return ch
}

// broadcastEvent sends an event to all watchers for a GVR and namespace
func (s *Simulator) broadcastEvent(gvr schema.GroupVersionResource, namespace string, event watch.Event) {
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
func (s *Simulator) generateKubeconfig() error {
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

func (s *Simulator) getStore(gvr schema.GroupVersionResource) cache.Store {
	return s.stores[gvr]
}

func (s *Simulator) getStoreOrWriteError(gvr schema.GroupVersionResource, w http.ResponseWriter, r *http.Request) (store cache.Store) {
	store = s.getStore(gvr)
	if store == nil {
		http.Error(w, "Resource not supported", http.StatusNotFound)
	}
	return
}

// nextResourceVersion increments and returns the next version for a GVR
func (s *Simulator) nextResourceVersion(gvr schema.GroupVersionResource) string {
	s.storeLock.Lock()
	defer s.storeLock.Unlock()
	s.versions[gvr]++
	return strconv.FormatInt(s.versions[gvr], 10)
}

func (s *Simulator) currResourceVersion(gvr schema.GroupVersionResource) string {
	s.storeLock.Lock()
	defer s.storeLock.Unlock()
	return strconv.FormatInt(s.versions[gvr], 10)
}

func writeStatusError(w http.ResponseWriter, r *http.Request, statusError *apierrors.StatusError) {
	w.WriteHeader(int(statusError.ErrStatus.Code))
	writeJsonResponse(w, r, statusError.ErrStatus)
}

// writeJsonResponse sets Content-Type to application/json  and encodes the object to the response writer.
func writeJsonResponse(w http.ResponseWriter, r *http.Request, obj any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(obj); err != nil {
		klog.Errorf("Failed to encode %s response: %v", r.URL.Path, err)
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
	}
}

func getNamespace(r *http.Request) (ns string) {
	ns = r.PathValue("namespace")
	if ns == "" {
		ns = "default"
	}
	return
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
		return flusher
	}
	return flusher
}
func buildWatchEventJson(event *watch.Event) (string, error) {
	// NOTE: Simple Json serialization does NOT work due to bug in Watch struct
	//if err := json.NewEncoder(w).Encode(event); err != nil {
	//	http.Error(w, fmt.Sprintf("Failed to encode watch event: %v", err), http.StatusInternalServerError)
	//	s.removeWatch(gvr, namespace, ch)
	//	return
	//}
	data, err := json.Marshal(event.Object)
	if err != nil {
		klog.Errorf("Failed to encode watch event: %v", err)
		return "", err
	}
	payload := fmt.Sprintf("{\"type\":\"%s\",\"object\":%s}", event.Type, string(data))
	return payload, nil
}

func readBodyIntoObj(w http.ResponseWriter, r *http.Request, obj any) (ok bool) {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
	if err := json.Unmarshal(data, obj); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
	ok = true
	return
}

func GetObjectName(r *http.Request) cache.ObjectName {
	namespace := getNamespace(r)
	name := r.PathValue("name")
	return cache.NewObjectName(namespace, name)
}
func GetObjectKey(r *http.Request) string {
	return GetObjectName(r).String()
}

func FromAnySlice(anys []any) ([]runtime.Object, error) {
	result := make([]runtime.Object, 0, len(anys))
	for _, item := range anys {
		obj, ok := item.(runtime.Object)
		if !ok {
			return nil, fmt.Errorf("element %T does not implement runtime.Object", item)
		}
		result = append(result, obj)
	}
	return result, nil
}
