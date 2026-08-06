package kube_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/leventozen/kubectl-tuna/internal/kube"
)

// BenchmarkCollectPodServiceNamespace makes the remaining broad read visible:
// Pod focus lists every Service in the namespace because Kubernetes has no
// reverse field/label query over Service.spec.selector. The fake client removes
// API-server and network variability, so the benchmark measures only local
// list decoding, selector matching, and focused graph construction cost.
func BenchmarkCollectPodServiceNamespace(b *testing.B) {
	for _, serviceCount := range []int{10, 1000, 5000} {
		b.Run(fmt.Sprintf("services-%d", serviceCount), func(b *testing.B) {
			objects, payloadBytes := podServiceNamespaceObjects(serviceCount)
			client := fakeClient(objects...)
			collector := kube.NewCollector(client)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := collector.CollectPod(context.Background(), "ns", "api-1"); err != nil {
					b.Fatal(err)
				}
				// Fake actions are diagnostic history, not part of collector state;
				// keep them from growing across benchmark iterations.
				client.ClearActions()
			}
			b.ReportMetric(float64(serviceCount), "services/op")
			b.ReportMetric(float64(payloadBytes)/1024, "service-list-KiB/op")
		})
	}
}

func podServiceNamespaceObjects(serviceCount int) ([]runtime.Object, int) {
	pod := readyPod("api-1")
	objects := make([]runtime.Object, 0, serviceCount+1)
	objects = append(objects, pod)
	services := make([]corev1.Service, 0, serviceCount)
	for i := 0; i < serviceCount; i++ {
		selector := map[string]string{"app": fmt.Sprintf("other-%d", i)}
		if i == 0 {
			selector = map[string]string{"app": "api"}
		}
		service := corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("service-%05d", i), Namespace: "ns"},
			Spec:       corev1.ServiceSpec{Selector: selector},
		}
		services = append(services, service)
		objects = append(objects, service.DeepCopy())
	}
	payload, err := json.Marshal(corev1.ServiceList{Items: services})
	if err != nil {
		panic(err)
	}
	return objects, len(payload)
}
