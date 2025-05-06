package core

import (
	"github.com/elankath/kapisim/core/typeinfo"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"testing"
)

func TestCreateList(t *testing.T) {
	descriptors := []typeinfo.Descriptor{
		typeinfo.PodsDescriptor,
	}
	objLists := [][]runtime.Object{
		{
			&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pa"}},
			&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pb"}},
		},
	}
	for i := 0; i < len(descriptors); i++ {
		d := descriptors[i]
		ol := objLists[i]
		listObj, err := createList(d, "bingo", "v1", ol)
		if err != nil {
			t.Errorf("Failed to create list: %v", err)
		}
		t.Logf("Created list object using %q: %v", d.ListGVK, listObj)
	}

}
