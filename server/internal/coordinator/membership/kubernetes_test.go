package membership

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clientfeatures "k8s.io/client-go/features"
	clientfeaturestesting "k8s.io/client-go/features/testing"
	"k8s.io/client-go/kubernetes/fake"
)

type recordingSink struct {
	upserts chan EndpointObservation
	deletes chan string
}

func (sink *recordingSink) UpsertEndpoint(observation EndpointObservation) {
	sink.upserts <- observation
}
func (sink *recordingSink) DeletePod(namespace, name, uid, resourceVersion string) {
	sink.deletes <- namespace + "/" + name + "/" + uid + "/" + resourceVersion
}

func TestKubernetesSourceCorrelatesEndpointSliceWithStatefulSetPod(t *testing.T) {
	clientfeaturestesting.SetFeatureDuringTest(t, clientfeatures.WatchListClient, false)
	ready := true
	portName, protocol, port := "http", corev1.ProtocolTCP, int32(8082)
	controller := true
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "classic-farm", Name: "zone-pool-0", UID: types.UID("pod-uid"), ResourceVersion: "10", OwnerReferences: []metav1.OwnerReference{{Kind: "StatefulSet", Name: "zone-pool", Controller: &controller}}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	slice := &discoveryv1.EndpointSlice{ObjectMeta: metav1.ObjectMeta{Namespace: "classic-farm", Name: "zone-discovery-abc", ResourceVersion: "11", Labels: map[string]string{discoveryv1.LabelServiceName: "zone-discovery"}}, AddressType: discoveryv1.AddressTypeIPv4, Ports: []discoveryv1.EndpointPort{{Name: &portName, Protocol: &protocol, Port: &port}}, Endpoints: []discoveryv1.Endpoint{{Addresses: []string{"10.0.0.8"}, Conditions: discoveryv1.EndpointConditions{Ready: &ready}, TargetRef: &corev1.ObjectReference{Kind: "Pod", Namespace: "classic-farm", Name: "zone-pool-0", UID: types.UID("pod-uid")}}}}
	client := fake.NewClientset(pod, slice)
	sink := &recordingSink{upserts: make(chan EndpointObservation, 8), deletes: make(chan string, 8)}
	source, err := NewKubernetesSource(client, "classic-farm", "zone-discovery", "zone-headless", "classic-farm-local", sink)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runResult := make(chan error, 1)
	go func() { runResult <- source.Run(ctx) }()

	select {
	case got := <-sink.upserts:
		if got.PodName != "zone-pool-0" || got.StatefulSetName != "zone-pool" || got.Ordinal != 0 || got.Endpoint != "http://zone-pool-0.zone-headless.classic-farm.svc.cluster.local:8082" || !got.EndpointReady || got.PodPhase != "Running" {
			t.Fatalf("observation=%+v", got)
		}
	case err := <-runResult:
		t.Fatalf("source exited before observation: %v", err)
	case <-time.After(3 * time.Second):
		select {
		case <-source.Ready():
			t.Fatal("source cache synced but emitted no endpoint observation")
		default:
			t.Fatal("source informer cache did not sync")
		}
	}

	if err := client.CoreV1().Pods("classic-farm").Delete(ctx, "zone-pool-0", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-sink.deletes:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Pod deletion")
	}
}

func TestKubernetesSourceIgnoresEndpointWithMismatchedPodUID(t *testing.T) {
	clientfeaturestesting.SetFeatureDuringTest(t, clientfeatures.WatchListClient, false)
	ready := true
	portName, port := "http", int32(8082)
	controller := true
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "classic-farm", Name: "zone-pool-0", UID: types.UID("actual"), OwnerReferences: []metav1.OwnerReference{{Kind: "StatefulSet", Name: "zone-pool", Controller: &controller}}}}
	slice := &discoveryv1.EndpointSlice{ObjectMeta: metav1.ObjectMeta{Namespace: "classic-farm", Name: "slice", Labels: map[string]string{discoveryv1.LabelServiceName: "zone-discovery"}}, Ports: []discoveryv1.EndpointPort{{Name: &portName, Port: &port}}, Endpoints: []discoveryv1.Endpoint{{Addresses: []string{"10.0.0.8"}, Conditions: discoveryv1.EndpointConditions{Ready: &ready}, TargetRef: &corev1.ObjectReference{Kind: "Pod", Namespace: "classic-farm", Name: "zone-pool-0", UID: types.UID("wrong")}}}}
	sink := &recordingSink{upserts: make(chan EndpointObservation, 1), deletes: make(chan string, 1)}
	source, err := NewKubernetesSource(fake.NewClientset(pod, slice), "classic-farm", "zone-discovery", "zone-headless", "cluster", sink)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = source.Run(ctx) }()
	select {
	case got := <-sink.upserts:
		t.Fatalf("unexpected observation: %+v", got)
	case <-time.After(300 * time.Millisecond):
	}
}
