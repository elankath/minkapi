package core

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	coreAPIResourceList = metav1.APIResourceList{
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

	apiGroupList = &metav1.APIGroupList{
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

	apiVersions = metav1.APIVersions{
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
	appsAPIResourceList = metav1.APIResourceList{
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

	eventsAPIResourceList = metav1.APIResourceList{
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
)
