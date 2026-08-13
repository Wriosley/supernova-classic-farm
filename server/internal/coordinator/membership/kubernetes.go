package membership

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/Wriosley/supernova-classic-farm/server/internal/zoneidentity"
)

type EndpointObservation struct {
	Namespace       string
	PodName         string
	PodUID          string
	ResourceVersion string
	ClusterID       string
	StatefulSetName string
	Ordinal         int
	Endpoint        string
	EndpointReady   bool
	PodPhase        string
	Deleting        bool
}

type ObservationSink interface {
	UpsertEndpoint(EndpointObservation)
	DeletePod(namespace, name, uid, resourceVersion string)
}

type KubernetesSource struct {
	client              kubernetes.Interface
	namespace           string
	serviceName         string
	headlessServiceName string
	clusterID           string
	sink                ObservationSink
	ready               chan struct{}
}

func NewKubernetesSource(client kubernetes.Interface, namespace, serviceName, headlessServiceName, clusterID string, sink ObservationSink) (*KubernetesSource, error) {
	if client == nil || sink == nil || strings.TrimSpace(namespace) == "" || strings.TrimSpace(serviceName) == "" || strings.TrimSpace(headlessServiceName) == "" || strings.TrimSpace(clusterID) == "" {
		return nil, errors.New("complete Kubernetes Zone discovery configuration is required")
	}
	return &KubernetesSource{client: client, namespace: namespace, serviceName: serviceName, headlessServiceName: headlessServiceName, clusterID: clusterID, sink: sink, ready: make(chan struct{})}, nil
}

func (source *KubernetesSource) Ready() <-chan struct{} { return source.ready }

func (source *KubernetesSource) Run(ctx context.Context) error {
	factory := informers.NewSharedInformerFactoryWithOptions(source.client, 0, informers.WithNamespace(source.namespace))
	pods := factory.Core().V1().Pods().Informer()
	slices := cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				options.LabelSelector = discoveryv1.LabelServiceName + "=" + source.serviceName
				return source.client.DiscoveryV1().EndpointSlices(source.namespace).List(ctx, options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				options.LabelSelector = discoveryv1.LabelServiceName + "=" + source.serviceName
				return source.client.DiscoveryV1().EndpointSlices(source.namespace).Watch(ctx, options)
			},
		},
		&discoveryv1.EndpointSlice{}, 0, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
	)
	reconcileAll := func() {
		for _, object := range slices.GetStore().List() {
			if slice, ok := object.(*discoveryv1.EndpointSlice); ok {
				source.reconcileSlice(slice, pods.GetStore())
			}
		}
	}
	_, _ = pods.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { reconcileAll() },
		UpdateFunc: func(_, _ any) { reconcileAll() },
		DeleteFunc: func(object any) {
			pod, ok := deletedPod(object)
			if ok {
				source.sink.DeletePod(pod.Namespace, pod.Name, string(pod.UID), pod.ResourceVersion)
			}
		},
	})
	_, _ = slices.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(object any) {
			if slice, ok := object.(*discoveryv1.EndpointSlice); ok {
				source.reconcileSlice(slice, pods.GetStore())
			}
		},
		UpdateFunc: func(_, object any) {
			if slice, ok := object.(*discoveryv1.EndpointSlice); ok {
				source.reconcileSlice(slice, pods.GetStore())
			}
		},
	})
	factory.Start(ctx.Done())
	go slices.Run(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), pods.HasSynced, slices.HasSynced) {
		return ctx.Err()
	}
	reconcileAll()
	close(source.ready)
	<-ctx.Done()
	return nil
}

func (source *KubernetesSource) reconcileSlice(slice *discoveryv1.EndpointSlice, podStore cache.Store) {
	port, ok := httpPort(slice.Ports)
	if !ok {
		return
	}
	for _, endpoint := range slice.Endpoints {
		if endpoint.TargetRef == nil || endpoint.TargetRef.Kind != "Pod" || endpoint.TargetRef.Name == "" || len(endpoint.Addresses) != 1 {
			continue
		}
		namespace := endpoint.TargetRef.Namespace
		if namespace == "" {
			namespace = slice.Namespace
		}
		object, exists, err := podStore.GetByKey(namespace + "/" + endpoint.TargetRef.Name)
		if err != nil || !exists {
			continue
		}
		pod, ok := object.(*corev1.Pod)
		if !ok || endpoint.TargetRef.UID != pod.UID {
			continue
		}
		statefulSet, ok := owningStatefulSet(pod)
		if !ok {
			continue
		}
		ordinal, err := zoneidentity.ParseOrdinal(pod.Name, statefulSet)
		if err != nil {
			continue
		}
		ready := endpoint.Conditions.Ready != nil && *endpoint.Conditions.Ready
		source.sink.UpsertEndpoint(EndpointObservation{
			Namespace: pod.Namespace, PodName: pod.Name, PodUID: string(pod.UID), ResourceVersion: slice.ResourceVersion,
			ClusterID: source.clusterID, StatefulSetName: statefulSet, Ordinal: ordinal,
			Endpoint:      (&url.URL{Scheme: "http", Host: net.JoinHostPort(pod.Name+"."+source.headlessServiceName+"."+pod.Namespace+".svc.cluster.local", strconv.Itoa(int(port)))}).String(),
			EndpointReady: ready, PodPhase: string(pod.Status.Phase), Deleting: pod.DeletionTimestamp != nil,
		})
	}
}

func httpPort(ports []discoveryv1.EndpointPort) (int32, bool) {
	for _, port := range ports {
		if port.Name != nil && *port.Name == "http" && port.Port != nil && *port.Port > 0 &&
			(port.Protocol == nil || *port.Protocol == corev1.ProtocolTCP) {
			return *port.Port, true
		}
	}
	return 0, false
}

func owningStatefulSet(pod *corev1.Pod) (string, bool) {
	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "StatefulSet" && owner.Name != "" && owner.Controller != nil && *owner.Controller {
			return owner.Name, true
		}
	}
	return "", false
}

func deletedPod(object any) (*corev1.Pod, bool) {
	if pod, ok := object.(*corev1.Pod); ok {
		return pod, true
	}
	tombstone, ok := object.(cache.DeletedFinalStateUnknown)
	if !ok {
		return nil, false
	}
	pod, ok := tombstone.Obj.(*corev1.Pod)
	return pod, ok
}
