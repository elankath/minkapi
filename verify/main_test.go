// Package verify module is used for quick developer testing against the minkapi service
package verify

import (
	"bytes"
	"compress/gzip"
	"context"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/elankath/minkapi/cli"
	"github.com/elankath/minkapi/core"
	"github.com/elankath/minkapi/core/objutil"
	"github.com/elankath/minkapi/core/typeinfo"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	"sigs.k8s.io/yaml"
)

// TestMain should handle server setup/teardown for the test suite.
func TestMain(m *testing.M) {
	//TODO: start minkapi server
	code := m.Run() // Run tests
	//TODO: shutdown minkapi server
	os.Exit(code)
}

func TestListStorageClasses(t *testing.T) {
	client := createKubeClient(t)
	t.Logf("Created kubernetes client")
	ctx := context.Background()

	scList, err := client.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("error listing StorageClasses: %v", err)
	}
	t.Logf("Found %d StorageClasses", len(scList.Items))
}

func TestWatchStorageClasses(t *testing.T) {
	client := createKubeClient(t)
	t.Logf("Created kubernetes client")
	ctx := context.Background()
	watcher, err := client.StorageV1().StorageClasses().Watch(ctx, metav1.ListOptions{Watch: true})
	if err != nil {
		t.Fatalf("failed to create StorageClasses watcher: %v", err)
		return
	}
	listObjects(t, watcher)
}
func TestSharedInformerNode(t *testing.T) {
	client := createKubeClient(t)
	t.Logf("Created kubernetes client")
	factory := informers.NewSharedInformerFactory(client, 30*time.Second)
	nodeInformer := factory.Core().V1().Nodes().Informer()
	_, err := nodeInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			n := obj.(*corev1.Node)
			t.Logf("[ADD] Node: %s\n", n.Name)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			newN := newObj.(*corev1.Node)
			t.Logf("[UPDATE] Node: %s\n", newN.Name)
		},
		DeleteFunc: func(obj interface{}) {
			n := obj.(*corev1.Node)
			t.Logf("[DELETE] Node: %s\n", n.Name)
		},
	})
	if err != nil {
		t.Fatalf("failed to add handler for StorageClasses: %v", err)
	}

	stopCh := make(chan struct{})
	defer close(stopCh)
	// Start informers
	factory.Start(stopCh)

	// Wait for initial cache sync
	if !cache.WaitForCacheSync(stopCh, nodeInformer.HasSynced) {
		t.Error(fmt.Errorf("timed out waiting for caches to sync"))
		return
	}

	t.Logf("NodeInformer informer running...")
	<-stopCh

}
func TestSharedInformerStorageClass(t *testing.T) {
	client := createKubeClient(t)
	t.Logf("Created kubernetes client")
	factory := informers.NewSharedInformerFactory(client, 30*time.Second)
	storageInformer := factory.Storage().V1().StorageClasses().Informer()
	_, err := storageInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			sc := obj.(*storagev1.StorageClass)
			t.Logf("[ADD] StorageClass: %s\n", sc.Name)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			newSc := newObj.(*storagev1.StorageClass)
			t.Logf("[UPDATE] StorageClass: %s\n", newSc.Name)
		},
		DeleteFunc: func(obj interface{}) {
			sc := obj.(*storagev1.StorageClass)
			t.Logf("[DELETE] StorageClass: %s\n", sc.Name)
		},
	})
	if err != nil {
		t.Fatalf("failed to add handler for StorageClasses: %v", err)
	}

	stopCh := make(chan struct{})
	defer close(stopCh)
	// Start informers
	factory.Start(stopCh)

	// Wait for initial cache sync
	if !cache.WaitForCacheSync(stopCh, storageInformer.HasSynced) {
		t.Error(fmt.Errorf("timed out waiting for caches to sync"))
		return
	}

	fmt.Println("StorageClass informer running...")
	<-stopCh

}

func TestWatchNodes(t *testing.T) {
	client := createKubeClient(t)
	t.Logf("Created kubernetes client")
	ctx := context.Background()
	watcher, err := client.CoreV1().Nodes().Watch(ctx, metav1.ListOptions{Watch: true})
	if err != nil {
		t.Fatalf("failed to create node watcher: %v", err)
		return
	}
	listObjects(t, watcher)
}

func TestWatchPods(t *testing.T) {
	client := createKubeClient(t)
	t.Logf("Created kubernetes client")
	ctx := context.Background()
	watcher, err := client.CoreV1().Pods("").Watch(ctx, metav1.ListOptions{Watch: true})
	if err != nil {
		t.Fatalf("failed to create pods watcher: %v", err)
		return
	}
	listObjects(t, watcher)
}

func TestWatchEvents(t *testing.T) {
	client := createKubeClient(t)
	t.Logf("Created kubernetes client")
	ctx := context.Background()
	watcher, err := client.EventsV1().Events("").Watch(ctx, metav1.ListOptions{Watch: true})
	if err != nil {
		t.Fatalf("failed to create event watcher: %v", err)
		return
	}
	listObjects(t, watcher)
}

func TestWatchStorageClass(t *testing.T) {
	client := createKubeClient(t)
	t.Logf("Created kubernetes client")
	ctx := context.Background()
	watcher, err := client.StorageV1().StorageClasses().Watch(ctx, metav1.ListOptions{Watch: true})
	if err != nil {
		t.Fatalf("failed to create sc watcher: %v", err)
		return
	}
	listObjects(t, watcher)
}

func TestWatchCSIDrivers(t *testing.T) {
	client := createKubeClient(t)
	t.Logf("Created kubernetes client")
	ctx := context.Background()
	watcher, err := client.StorageV1().CSIDrivers().Watch(ctx, metav1.ListOptions{Watch: true})
	if err != nil {
		t.Fatalf("failed to create csidrver watcher: %v", err)
		return
	}
	listObjects(t, watcher)
}

func TestWatchPersistentVolumes(t *testing.T) {
	client := createKubeClient(t)
	t.Logf("Created kubernetes client")
	ctx := context.Background()
	watcher, err := client.CoreV1().PersistentVolumes().Watch(ctx, metav1.ListOptions{Watch: true})
	if err != nil {
		t.Fatalf("failed to create PV watcher: %v", err)
		return
	}
	listObjects(t, watcher)
}

func TestObjCreationViaScheme(t *testing.T) {
	//scheme, err := typeinfo.RegisterSchemes()
	//if err != nil {
	//	t.Fatalf("failed to register schemes: %v", err)
	//}
	//gvk := schema.GroupVersionKind{
	//	Group:   "",
	//	Version: "v1",
	//	Kind:    "Node",
	//}
	descriptors := []typeinfo.Descriptor{
		typeinfo.PodsDescriptor,
		typeinfo.NodesDescriptor,
	}
	for _, d := range descriptors {
		obj, err := typeinfo.SupportedScheme.New(d.GVK)
		if err != nil {
			t.Fatalf("failed to create object using %q due to %v", d.GVK, err)
		}
		t.Logf("Created object using %q: %v", d.GVK, obj)
		listObj, err := typeinfo.SupportedScheme.New(d.ListGVK)
		if err != nil {
			t.Fatalf("failed to create list object using %q due to %v", d.ListGVK, err)
		}
		t.Logf("Created list object using %q: %v", d.ListGVK, listObj)
	}
}

func listObjects(t *testing.T, watcher watch.Interface) {
	t.Helper()
	watchCh := watcher.ResultChan()
	t.Logf("Waiting on watchCh: %v", watchCh)
	for ev := range watchCh {
		t.Logf("%v: %v", ev.Type, ev.Object)
	}
}

func createKubeClient(t *testing.T) kubernetes.Interface {
	t.Helper() // Marks this function as a helper
	kubeconfigPath := getKubeConfigPath()
	clientConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		t.Fatal(err)
	}
	clientConfig.ContentType = "application/json"
	clientset, err := kubernetes.NewForConfig(clientConfig)
	if err != nil {
		t.Fatal(err)
	}
	return clientset
}

func getKubeConfigPath() string {
	kubeConfigPath := os.Getenv("KUBECONFIG")
	if kubeConfigPath == "" {
		kubeConfigPath = "/tmp/minkapi.yaml"
	}
	return kubeConfigPath
}
func TestSharedInformerPod(t *testing.T) {
	flagSet := flag.NewFlagSet("klog", flag.ContinueOnError)
	klog.InitFlags(flagSet)
	err := flagSet.Parse([]string{"-v=4"})
	if err != nil {
		t.Fatal(err)
	}
	client := createKubeClient(t)
	t.Logf("Created kubernetes client")
	factory := informers.NewSharedInformerFactory(client, 30*time.Second)
	podInformer := factory.Core().V1().Pods().Informer()
	_, err = podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			p := obj.(*corev1.Pod)
			t.Logf("[ADD] Pod: %s\n", p.Name)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			newP := newObj.(*corev1.Pod)
			t.Logf("[UPDATE] Pod: %s\n", newP.Name)
		},
		DeleteFunc: func(obj interface{}) {
			p := obj.(*corev1.Pod)
			t.Logf("[DELETE] Pod: %s\n", p.Name)
		},
	})
	if err != nil {
		t.Fatalf("failed to add handler for Pods: %v", err)
	}

	stopCh := make(chan struct{})
	defer close(stopCh)
	// Start informers
	factory.Start(stopCh)

	// Wait for initial cache sync
	if !cache.WaitForCacheSync(stopCh, podInformer.HasSynced) {
		t.Error(fmt.Errorf("timed out waiting for caches to sync"))
		return
	}

	t.Logf("PodInformer informer running...")
	<-stopCh

}

func TestLoadClusterObjects(t *testing.T) {
	objects, err := loadObjects(t, "/tmp/prod-hna0")
	if err != nil {
		t.Errorf("failed to load objects: %v", err)
		return
	}
	t.Logf("Loaded %d objects", len(objects))
	runtime.GC()
	rss, err := getRSS()
	if err != nil {
		t.Errorf("failed to get RSS: %v", err)
		return
	}
	t.Logf("RSS in Bytes: %d", rss)
	t.Logf("RSS in Human: %s", bytesToHuman(rss))
}

func TestLoadClusterJsons(t *testing.T) {
	objJsons, err := loadObjectJSONs(t, "/tmp/prod-hna0")
	if err != nil {
		t.Errorf("failed to load objects: %v", err)
		return
	}
	t.Logf("Loaded %d objects", len(objJsons))
	runtime.GC()
	// Convert HeapAlloc to a Quantity
	// Print human-readable format
	rss, err := getRSS()
	if err != nil {
		t.Errorf("failed to get RSS: %v", err)
		return
	}
	t.Logf("RSS in Bytes: %d", rss)
	t.Logf("RSS in Human: %s", bytesToHuman(rss))
}

func TestInMemObjCreation(t *testing.T) {
	mainOpts, err := cli.ParseProgramFlags([]string{"-k", "/tmp/minkapi-embed.yaml", "-P", "9876"})
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Err: %v\n", err)
		// os.Exit(minkapicli.ExitErrParseOpts)
		return
	}
	log := klog.NewKlogr()
	svc, err := core.NewInMemoryMinKAPI(context.TODO(), mainOpts.MinKAPIConfig, log)
	if err != nil {
		log.Error(err, "failed to start InMemoryKAPI")
		return
	}

	go func() {
		if err := svc.Start(); err != nil {
			log.Error(err, fmt.Sprintf("minkapi start failed"), err)
			// os.Exit(cli.ExitErrStart)
			return
		}
	}()

	waitSecs := 2
	slog.Info("Waiting for minkapi to start", "waitSecs", waitSecs)
	<-time.After(time.Duration(waitSecs) * time.Second)

	var n1 corev1.Node
	err = objutil.LoadYamlIntoObj("../specs/node-a.yaml", &n1)
	if err != nil {
		t.Error(err)
		return
	}

	err = svc.CreateObject(typeinfo.NodesDescriptor.GVK, &n1)
	if err != nil {
		t.Error(err)
		return
	}

	var p1 corev1.Pod
	err = objutil.LoadYamlIntoObj("../specs/pod-a.yaml", &p1)
	if err != nil {
		t.Error(err)
		return
	}

	err = svc.CreateObject(typeinfo.PodsDescriptor.GVK, &p1)
	if err != nil {
		t.Error(err)
		return
	}

	// List objects
	nodes, err := svc.ListNodes()
	if err != nil {
		t.Error(err)
		return
	}
	t.Logf("Nodes are %v", nodes)

	pods, err := svc.ListPods("default")
	if err != nil {
		t.Error(err)
		return
	}
	t.Logf("Pods are %v", pods)

	<-time.After(1 * time.Minute)
}

func loadObjects(t *testing.T, baseObjDir string) (objs []*unstructured.Unstructured, err error) {
	t.Helper()
	start := time.Now()
	t.Logf("Loading objects from baseObjDir %q", baseObjDir)
	objCount := 0
	objs = make([]*unstructured.Unstructured, 0, 3000)
	err = filepath.WalkDir(baseObjDir, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf(" path error for %q: %w", path, err)
		}
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			return nil
		}
		// Infer GVR from parent directory
		resourcesDirName := filepath.Base(filepath.Dir(path))
		parts := strings.SplitN(resourcesDirName, "-", 3)
		if len(parts) != 3 {
			err = fmt.Errorf("invalid object resourcesDirName: %s", resourcesDirName)
			return err
		}
		var obj *unstructured.Unstructured
		obj, err = loadAndCleanObj(path)
		if err != nil {
			return err
		}
		objs = append(objs, obj)
		objCount++
		if objCount%2000 == 0 {
			slog.Info("Loaded object", "objCount", objCount, "path", path)
		} else {
			slog.Debug("Loaded object", "objCount", objCount, "path", path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	end := time.Now()
	t.Logf("Loaded total objects: %d from baseObjDir %q in %s duration", objCount, baseObjDir, end.Sub(start))
	return objs, nil
}

func loadObjectJSONs(t *testing.T, baseObjDir string) (objJsons [][]byte, err error) {
	t.Helper()
	t.Logf("Loading objects from baseObjDir %q", baseObjDir)
	start := time.Now()
	objCount := 0
	objJsons = make([][]byte, 0, 10000)
	var totalSize uint64 = 0
	err = filepath.WalkDir(baseObjDir, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf(" path error for %q: %w", path, err)
		}
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			return nil
		}
		// Infer GVR from parent directory
		resourcesDirName := filepath.Base(filepath.Dir(path))
		parts := strings.SplitN(resourcesDirName, "-", 3)
		if len(parts) != 3 {
			err = fmt.Errorf("invalid object resourcesDirName: %s", resourcesDirName)
			return err
		}
		data, err := loadObjBytes(path)
		if err != nil {
			return err
		}
		objJsons = append(objJsons, data)
		objCount++
		if objCount%2000 == 0 {
			t.Logf("#%d Loaded object of size %d from path %q", objCount, len(data), path)
		}
		totalSize += uint64(len(data))
		return nil
	})
	if err != nil {
		return nil, err
	}
	end := time.Now()
	t.Logf("Loaded total objects: %d, totalSize: %d, humanSize: %s from baseObjDir %q in %s duration", objCount, totalSize, bytesToHuman(totalSize), baseObjDir, end.Sub(start))
	return objJsons, nil
}

func loadAndCleanObj(objPath string) (obj *unstructured.Unstructured, err error) {
	data, err := os.ReadFile(objPath)
	if err != nil {
		err = fmt.Errorf("failed to read %q: %w", objPath, err)
		return
	}
	obj = &unstructured.Unstructured{}
	jsonData, err := yaml.YAMLToJSON(data)
	if err != nil {
		err = fmt.Errorf("failed to convert YAML to JSON for %q: %w", objPath, err)
		return
	}

	err = obj.UnmarshalJSON(jsonData)
	if err != nil {
		err = fmt.Errorf("failed to unmarshal object in %q: %w", objPath, err)
		return
	}
	unstructured.RemoveNestedField(obj.Object, "metadata", "resourceVersion")
	//unstructured.RemoveNestedField(obj.Object, "metadata", "uid")
	//unstructured.RemoveNestedField(obj.Object, "metadata", "generation")
	obj.SetManagedFields(nil)
	//unstructured.RemoveNestedField(obj.Object, "status")
	obj.SetGeneration(0)

	if obj.GetKind() != "Pod" {
		return
	}
	//TODO: Make this configurable via flag

	err = unstructured.SetNestedField(obj.Object, "", "spec", "nodeName")
	if err != nil {
		err = fmt.Errorf("cannot clear spec.nodeName for pod %q: %w", obj.GetName(), err)
		return
	}
	return
}

func loadObjBytes(objPath string) (compressData []byte, err error) {
	data, err := os.ReadFile(objPath)
	if err != nil {
		err = fmt.Errorf("failed to read %q: %w", objPath, err)
		return
	}
	jsonData, err := yaml.YAMLToJSON(data)
	if err != nil {
		err = fmt.Errorf("failed to convert YAML to JSON for %q: %w", objPath, err)
		return
	}
	compressData, err = compressJSON(jsonData)
	return
}

// bytesToHuman converts a uint64 byte value to a human-readable string with BinarySI units (B, Ki, Mi, Gi, etc.).
func bytesToHuman(bytes uint64) string {
	const (
		KiB = 1024
		MiB = 1024 * KiB
		GiB = 1024 * MiB
		TiB = 1024 * GiB
		PiB = 1024 * TiB
		EiB = 1024 * PiB
	)
	switch {
	case bytes >= EiB:
		return fmt.Sprintf("%.2fEi", float64(bytes)/float64(EiB))
	case bytes >= PiB:
		return fmt.Sprintf("%.2fPi", float64(bytes)/float64(PiB))
	case bytes >= TiB:
		return fmt.Sprintf("%.2fTi", float64(bytes)/float64(TiB))
	case bytes >= GiB:
		return fmt.Sprintf("%.2fGi", float64(bytes)/float64(GiB))
	case bytes >= MiB:
		return fmt.Sprintf("%.2fMi", float64(bytes)/float64(MiB))
	case bytes >= KiB:
		return fmt.Sprintf("%.2fKi", float64(bytes)/float64(KiB))
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

// getRSS attempts to get the maximum RSS (Resident Set Size) in bytes for the current process using syscall.Getrusage.
func getRSS() (uint64, error) {
	var rusage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &rusage); err != nil {
		return 0, fmt.Errorf("failed to get rusage: %w", err)
	}
	// ru_maxrss is the maximum resident set size in bytes
	return uint64(rusage.Maxrss), nil
}

var bufPool = sync.Pool{
	New: func() interface{} { return new(bytes.Buffer) },
}

func compressJSON(data []byte) ([]byte, error) {
	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)
	buf.Reset()
	gz := gzip.NewWriter(buf)
	_, err := io.Copy(gz, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to compress data: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	// Return a copy of the buffer's contents to avoid reuse issues
	result := make([]byte, buf.Len())
	copy(result, buf.Bytes())
	return result, nil
}
