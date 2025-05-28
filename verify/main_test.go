// Package verify module is used for quick developer testing against the minkapi service
package verify

import (
	"context"
	"flag"
	"fmt"
	"github.com/elankath/minkapi/core/typeinfo"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	"os"
	"testing"
	"time"
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
		runtime.HandleError(fmt.Errorf("timed out waiting for caches to sync"))
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
		runtime.HandleError(fmt.Errorf("timed out waiting for caches to sync"))
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
		runtime.HandleError(fmt.Errorf("timed out waiting for caches to sync"))
		return
	}

	t.Logf("PodInformer informer running...")
	<-stopCh

}
