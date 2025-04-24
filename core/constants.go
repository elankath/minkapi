package core

import (
	"errors"
	appsv1 "k8s.io/api/apps/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"maps"
	"slices"
)

var (
	GVRNamespaces = corev1.SchemeGroupVersion.WithResource("namespaces")
	GVRPods       = corev1.SchemeGroupVersion.WithResource("pods")
	GVRNodes      = corev1.SchemeGroupVersion.WithResource("nodes")

	GVRPriorityClasses = schedulingv1.SchemeGroupVersion.WithResource("priorityclasses")

	GVRLeases = coordinationv1.SchemeGroupVersion.WithResource("leases")

	GVREvents      = eventsv1.SchemeGroupVersion.WithResource("events")
	GVRRoles       = rbacv1.SchemeGroupVersion.WithResource("roles")
	GVRDeployments = appsv1.SchemeGroupVersion.WithResource("deployments")

	GVRReplicaSets = appsv1.SchemeGroupVersion.WithResource("replicasets")

	SupportedGVRs = []schema.GroupVersionResource{GVRNamespaces, GVRPods, GVRNodes, GVRPriorityClasses, GVRLeases, GVREvents, GVRRoles, GVRDeployments, GVRReplicaSets}
)

var schemeAdders = []func(scheme *runtime.Scheme) error{
	corev1.AddToScheme,
	appsv1.AddToScheme,
	coordinationv1.AddToScheme,
	eventsv1.AddToScheme,
	rbacv1.AddToScheme,
	schedulingv1.AddToScheme,
}

func RegisterSchemes() (scheme *runtime.Scheme, err error) {
	scheme = runtime.NewScheme()
	var errs []error
	for _, fn := range schemeAdders {
		err = fn(scheme)
		if err != nil {
			errs = append(errs, err)
		}
	}
	err = errors.Join(errs...)
	return
}

var (
	apiGroupList        = buildAPIGroupList()
	coreAPIResourceList = metav1.APIResourceList{
		TypeMeta: metav1.TypeMeta{
			Kind: "APIResourceList",
		},
		GroupVersion: "v1",
		APIResources: []metav1.APIResource{
			{
				Name:               "namespaces",
				SingularName:       "namespace",
				ShortNames:         []string{"ns"},
				Kind:               "Namespace",
				Namespaced:         false,
				Verbs:              []string{"create", "delete", "get", "list", "watch"},
				StorageVersionHash: "ns111",
			},
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
				Name:               "nodes",
				SingularName:       "node",
				ShortNames:         []string{"no"},
				Kind:               "Node",
				Namespaced:         false,
				Verbs:              []string{"create", "delete", "get", "list", "watch"},
				Categories:         []string{"all"},
				StorageVersionHash: "node111",
			},
		},
	}

	apiVersions = metav1.APIVersions{
		TypeMeta: metav1.TypeMeta{
			Kind: "APIVersions",
		},
		Versions: []string{"v1"},
		ServerAddressByClientCIDRs: []metav1.ServerAddressByClientCIDR{
			{
				ClientCIDR:    "0.0.0.0/0",
				ServerAddress: "127.0.0.1:8080",
			},
		},
	}
	appsAPIResourceList = metav1.APIResourceList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "APIResourceList",
			APIVersion: "v1",
		},
		GroupVersion: appsv1.SchemeGroupVersion.String(),
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

	eventsAPIResourceList = metav1.APIResourceList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "APIResourceList",
			APIVersion: "v1",
		},
		GroupVersion: eventsv1.SchemeGroupVersion.String(),
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
)

func buildAPIGroupList() metav1.APIGroupList {
	var groups = make(map[string]metav1.APIGroup)
	for _, gvr := range SupportedGVRs {
		if gvr.Group == "" {
			//  don't add default group otherwise kubectl will  give errors like the below
			// error: /, Kind=Pod matches multiple kinds [/v1, Kind=Pod /v1, Kind=Pod]
			// OH-MY-GAWD, it took me FOREVER to find this.
			continue
		}
		groups[gvr.Group] = metav1.APIGroup{
			Name: gvr.Group,
			Versions: []metav1.GroupVersionForDiscovery{
				{
					GroupVersion: gvr.GroupVersion().String(),
					Version:      gvr.Version,
				},
			},
		}
	}
	return metav1.APIGroupList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "APIGroupList",
			APIVersion: "v1",
		},
		Groups: slices.Collect(maps.Values(groups)),
	}
}
