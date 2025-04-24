package verify

import (
	"context"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func TestWatchNodes(t *testing.T) {
	client := createKubeClient(t)
	t.Logf("Created kubernetes client")
	ctx := context.Background()
	watcher, err := client.CoreV1().Nodes().Watch(ctx, metav1.ListOptions{Watch: true})
	if err != nil {
		t.Fatalf("failed to watch nodes: %v", err)
		return
	}
	watchCh := watcher.ResultChan()
	t.Logf("Waiting on watchCh: %v", watchCh)
	for ev := range watchCh {
		t.Logf("Node watch event: %v: %v", ev.Type, ev.Object)
		node, ok := ev.Object.(*corev1.Node)
		if !ok {
			t.Logf("Unexpected type: %T", ev.Object)
			continue
		}
		t.Logf("New node %s: %s", ev.Type, node.Name)
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
