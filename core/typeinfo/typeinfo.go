package typeinfo

import (
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
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
	"maps"
	"slices"
	"strings"
)

type KindName string

const (
	NamespaceKind     KindName = "Namespace"
	NamespaceListKind KindName = "NamespaceList"

	PodKind     KindName = "Pod"
	PodListKind KindName = "PodList"

	NodeKind     KindName = "Node"
	NodeListKind KindName = "NodeList"

	PriorityClassKind     KindName = "PriorityClass"
	PriorityClassListKind KindName = "PriorityClassList"

	LeaseKind     KindName = "Lease"
	LeaseListKind KindName = "LeaseList"

	EventKind     KindName = "Event"
	EventListKind KindName = "EventList"

	RoleKind     KindName = "Role"
	RoleListKind KindName = "RoleList"

	RoleBindingKind     KindName = "RoleBinding"
	RoleBindingListKind KindName = "RoleBindingList"

	DeploymentKind     KindName = "Deployment"
	DeploymentListKind KindName = "DeploymentList"

	ReplicaSetKind     KindName = "ReplicaSet"
	ReplicaSetListKind KindName = "ReplicaSetList"
)

// Descriptor is an aggregate holder of various bits of type information on a given Kind
type Descriptor struct {
	Kind         KindName
	GVK          schema.GroupVersionKind
	ListKind     KindName
	ListGVK      schema.GroupVersionKind
	GVR          schema.GroupVersionResource
	ListTypeMeta metav1.TypeMeta
	APIResource  metav1.APIResource
	//ObjTemplate     runtime.Object
	//ObjListTemplate runtime.Object
	//ObjType         reflect.Type
	//ObjListType     reflect.Type
	//ItemsSliceType  reflect.Type
}

var (
	SupportedScheme      = RegisterSchemes()
	NamespacesDescriptor = NewDescriptor(NamespaceKind, NamespaceListKind, false, corev1.SchemeGroupVersion.WithResource("namespaces"), "ns")

	NodesDescriptor = NewDescriptor(NodeKind, NodeListKind, false, corev1.SchemeGroupVersion.WithResource("nodes"), "no")
	PodsDescriptor  = NewDescriptor(PodKind, PodListKind, true, corev1.SchemeGroupVersion.WithResource("pods"), "po")

	PriorityClassesDescriptor = NewDescriptor(PriorityClassKind, PriorityClassListKind, false, schedulingv1.SchemeGroupVersion.WithResource("priorityclasses"))

	LeaseDescriptor = NewDescriptor(LeaseKind, LeaseListKind, true, coordinationv1.SchemeGroupVersion.WithResource("leases"))

	EventsDescriptor     = NewDescriptor(EventKind, EventListKind, true, eventsv1.SchemeGroupVersion.WithResource("events"), "ev")
	RolesDescriptor      = NewDescriptor(RoleKind, RoleListKind, true, rbacv1.SchemeGroupVersion.WithResource("roles"))
	DeploymentDescriptor = NewDescriptor(DeploymentKind, DeploymentListKind, true, appsv1.SchemeGroupVersion.WithResource("deployments"), "deploy")
	ReplicaSetDescriptor = NewDescriptor(ReplicaSetKind, ReplicaSetListKind, true, appsv1.SchemeGroupVersion.WithResource("replicasets"), "rs")

	SupportedDescriptors = []Descriptor{NamespacesDescriptor, PodsDescriptor, NodesDescriptor, PriorityClassesDescriptor, LeaseDescriptor, EventsDescriptor, RolesDescriptor, DeploymentDescriptor, ReplicaSetDescriptor}

	SupportedVerbs = []string{"create", "delete", "get", "list", "watch"}

	SupportedAPIVersions = metav1.APIVersions{
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

	SupportedAPIGroups = buildAPIGroupList()

	SupportedCoreAPIResourceList = metav1.APIResourceList{
		TypeMeta: metav1.TypeMeta{
			Kind: "APIResourceList",
		},
		GroupVersion: "v1",
		APIResources: []metav1.APIResource{
			NamespacesDescriptor.APIResource,
			NodesDescriptor.APIResource,
			PodsDescriptor.APIResource,
		},
		//APIResources: []metav1.APIResource{
		//	{
		//		Name:               "namespaces",
		//		SingularName:       "namespace",
		//		ShortNames:         []string{"ns"},
		//		Kind:               "Namespace",
		//		Namespaced:         false,
		//		Verbs:              SupportedVerbs,
		//		StorageVersionHash: "ns111",
		//	},
		//	{
		//		Name:               "pods",
		//		SingularName:       "pod",
		//		ShortNames:         []string{"po"},
		//		Kind:               "Pod",
		//		Namespaced:         true,
		//		Verbs:              SupportedVerbs,
		//		Categories:         []string{"all"},
		//		StorageVersionHash: "pod111",
		//	},
		//	{
		//		Name:               "nodes",
		//		SingularName:       "node",
		//		ShortNames:         []string{"no"},
		//		Kind:               "Node",
		//		Namespaced:         false,
		//		Verbs:              SupportedVerbs,
		//		Categories:         []string{"all"},
		//		StorageVersionHash: "node111",
		//	},
	}

	SupportedAppsAPIResourceList = metav1.APIResourceList{
		TypeMeta:     metaV1APIResourceList,
		GroupVersion: appsv1.SchemeGroupVersion.String(),
		APIResources: []metav1.APIResource{
			DeploymentDescriptor.APIResource,
			ReplicaSetDescriptor.APIResource,
		},
		//APIResources: []metav1.APIResource{
		//	{
		//		Name:       "deployments",
		//		ShortNames: []string{"deploy"},
		//		Kind:       "Deployment",
		//		Namespaced: true,
		//		Verbs:      SupportedVerbs,
		//	},
		//	{
		//		Name:       "replicasets",
		//		ShortNames: []string{"rs"},
		//		Kind:       "ReplicaSet",
		//		Namespaced: true,
		//		Verbs:      SupportedVerbs,
		//	},
		//},
	}
	SupportedCoordinationAPIResourceList = metav1.APIResourceList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "APIResourceList",
			APIVersion: "v1",
		},
		GroupVersion: coordinationv1.SchemeGroupVersion.String(),
		APIResources: []metav1.APIResource{
			LeaseDescriptor.APIResource,
		},
	}

	SupportedEventsAPIResourceList = metav1.APIResourceList{
		TypeMeta:     metaV1APIResourceList,
		GroupVersion: eventsv1.SchemeGroupVersion.String(),
		APIResources: []metav1.APIResource{
			EventsDescriptor.APIResource,
		},
	}

	SupportedRBACAPIResourceList = metav1.APIResourceList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "APIResourceList",
			APIVersion: "v1",
		},
		GroupVersion: rbacv1.SchemeGroupVersion.String(),
		APIResources: []metav1.APIResource{
			RolesDescriptor.APIResource,
		},
	}

	SupportedSchedulingAPIResourceList = metav1.APIResourceList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "APIResourceList",
			APIVersion: "v1",
		},
		GroupVersion: schedulingv1.SchemeGroupVersion.String(),
		APIResources: []metav1.APIResource{
			PriorityClassesDescriptor.APIResource,
		},
	}
)

var (
	schemeAdders = []func(scheme *runtime.Scheme) error{
		metav1.AddMetaToScheme,
		corev1.AddToScheme,
		appsv1.AddToScheme,
		coordinationv1.AddToScheme,
		eventsv1.AddToScheme,
		rbacv1.AddToScheme,
		schedulingv1.AddToScheme,
	}

	metaV1APIResourceList = metav1.TypeMeta{
		Kind:       "APIResourceList",
		APIVersion: "v1",
	}
	//resourceTypeCache   = buildResourceTypeCache()

)

func RegisterSchemes() (scheme *runtime.Scheme) {
	scheme = runtime.NewScheme()
	for _, fn := range schemeAdders {
		utilruntime.Must(fn(scheme))
	}
	return
}

func buildAPIGroupList() metav1.APIGroupList {
	var groups = make(map[string]metav1.APIGroup)
	for _, d := range SupportedDescriptors {
		if d.GVK.Group == "" {
			//  don't add default group otherwise kubectl will  give errors like the below
			// error: /, Kind=Pod matches multiple kinds [/v1, Kind=Pod /v1, Kind=Pod]
			// OH-MY-GAWD, it took me FOREVER to find this.
			continue
		}
		groups[d.GVR.Group] = metav1.APIGroup{
			Name: d.GVR.Group,
			Versions: []metav1.GroupVersionForDiscovery{
				{
					GroupVersion: d.GVK.GroupVersion().String(),
					Version:      d.GVK.Version,
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

func NewDescriptor(kind KindName, listKind KindName, namespaced bool, gvr schema.GroupVersionResource, shortNames ...string) Descriptor {
	//listType := reflect.TypeOf(objListTemplate)
	//objType := reflect.TypeOf(objTemplate)
	//return Descriptor{
	//	GVR:             gvr,
	//	ObjTemplate:     objTemplate,
	//	ObjListTemplate: objListTemplate,
	//	ObjType:         objType,
	//	ObjListType:     listType,
	//	ItemsSliceType:  reflect.SliceOf(objType),
	//}
	var singularName string
	if strings.HasPrefix(gvr.Resource, "sses") {
		singularName = strings.TrimSuffix(gvr.Resource, "es")
	} else {
		singularName = strings.TrimSuffix(gvr.Resource, "s")
	}
	return Descriptor{
		Kind: kind,
		GVK: schema.GroupVersionKind{
			Group:   gvr.Group,
			Version: gvr.Version,
			Kind:    string(kind),
		},
		ListKind: listKind,
		ListGVK: schema.GroupVersionKind{
			Group:   gvr.Group,
			Version: gvr.Version,
			Kind:    string(listKind),
		},
		GVR: gvr,
		ListTypeMeta: metav1.TypeMeta{
			Kind:       string(listKind),
			APIVersion: gvr.GroupVersion().String(),
		},
		APIResource: metav1.APIResource{
			Name:               gvr.Resource,
			SingularName:       singularName,
			Namespaced:         namespaced,
			Group:              gvr.Group,
			Version:            gvr.Version,
			Kind:               string(kind),
			Verbs:              SupportedVerbs,
			ShortNames:         shortNames,
			Categories:         []string{"all"}, // TODO: Uhhh, WTH is this exactly ? Who uses this ?
			StorageVersionHash: GenerateName(singularName),
		},
	}
}

func (d Descriptor) CreateObject() (obj metav1.Object, err error) {
	runtimeObj, err := SupportedScheme.New(d.GVK)
	if err != nil {
		return
	}
	obj = runtimeObj.(metav1.Object)
	return
}

func GenerateName(base string) string {
	const suffixLen = 5
	suffix := utilrand.String(suffixLen)
	m := validation.DNS1123SubdomainMaxLength // 253 for subdomains; use DNS1123LabelMaxLength (63) if you need stricter
	if len(base)+len(suffix) > m {
		base = base[:m-len(suffix)]
	}
	return base + suffix
}
