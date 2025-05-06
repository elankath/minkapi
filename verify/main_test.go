// Package verify module is used for quick developer testing against the kapisim service
package verify

import (
	"context"
	"github.com/elankath/kapisim/core/typeinfo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"os"
	"testing"
)

// TestMain should handle server setup/teardown for the test suite.
func TestMain(m *testing.M) {
	//TODO: start kapisim server
	code := m.Run() // Run tests
	//TODO: shutdown kapisim server
	os.Exit(code)
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
		kubeConfigPath = "/tmp/kapisim.yaml"
	}
	return kubeConfigPath
}
