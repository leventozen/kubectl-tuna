package kube_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	versioninfo "k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/leventozen/kubectl-tuna/internal/diag"
	"github.com/leventozen/kubectl-tuna/internal/graph"
	"github.com/leventozen/kubectl-tuna/internal/kube"
)

func TestEndpointSliceFailureProducesUnknownHealth(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "api"},
			Ports:    []corev1.ServicePort{{Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080)}},
		},
	}
	pod := readyPod("api-1")
	client := fakeClient(svc, pod)
	client.PrependReactor("list", "endpointslices", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})

	g, err := kube.NewCollector(client).CollectService(context.Background(), "ns", "api")
	require.NoError(t, err)
	res := diag.NewEngine().Evaluate(g)
	require.Equal(t, diag.HealthUnknown, res.Health)
	require.True(t, res.Partial)
	require.Len(t, res.Warnings, 1)
	for _, finding := range res.Findings {
		require.NotEqual(t, diag.ServiceNoReadyEndpoints, finding.Type, "unavailable EndpointSlices are not evidence of zero endpoints")
	}
}

func TestEndpointSliceThrottlingProducesUnknownHealth(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "api"},
			Ports:    []corev1.ServicePort{{Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080)}},
		},
	}
	pod := readyPod("api-1")
	client := fakeClient(svc, pod)
	client.PrependReactor("list", "endpointslices", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewTooManyRequests("API priority and fairness throttled the request", 1)
	})

	g, err := kube.NewCollector(client).CollectService(context.Background(), "ns", "api")
	require.NoError(t, err)
	res := diag.NewEngine().Evaluate(g)
	require.Equal(t, diag.HealthUnknown, res.Health)
	require.True(t, hasWarningSource(res, graph.SourceEndpointSlices))
	require.False(t, hasFindingType(res, diag.ServiceNoReadyEndpoints))
}

func TestCanceledFocusRequestRemainsACommandError(t *testing.T) {
	client := fakeClient()
	client.PrependReactor("get", "pods", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, context.Canceled
	})

	_, err := kube.NewCollector(client).CollectPod(context.Background(), "ns", "api")
	require.ErrorIs(t, err, context.Canceled)
}

func TestFocusRevisionChangeSuspendsDiagnosticRules(t *testing.T) {
	pod := readyPod("api-1")
	pod.UID = "pod-uid"
	pod.ResourceVersion = "10"
	pod.Status.Conditions[0].Status = corev1.ConditionFalse
	client := fakeClient()
	gets := 0
	client.PrependReactor("get", "pods", func(action clienttesting.Action) (bool, runtime.Object, error) {
		if action.(clienttesting.GetAction).GetName() != pod.Name {
			return false, nil, nil
		}
		gets++
		observed := pod.DeepCopy()
		if gets == 2 {
			observed.ResourceVersion = "11"
		}
		return true, observed, nil
	})

	g, err := kube.NewCollector(client).CollectPod(context.Background(), "ns", pod.Name)
	require.NoError(t, err)
	require.Equal(t, graph.FocusStabilityChanged, g.Collection.FocusStability)

	res := diag.NewEngine().Evaluate(g)
	require.Equal(t, diag.HealthUnknown, res.Health)
	require.Empty(t, res.Findings, "mixed-time evidence must not be evaluated")
	require.Zero(t, res.Rules.Evaluated)
	require.NotEmpty(t, res.Rules.SuspendedReason)
	require.True(t, hasWarningSource(res, graph.SourceTemporalIntegrity))
}

func TestFocusRevisionRecheckFailureSuspendsDiagnosticRules(t *testing.T) {
	pod := readyPod("api-1")
	pod.ResourceVersion = "10"
	client := fakeClient()
	gets := 0
	client.PrependReactor("get", "pods", func(clienttesting.Action) (bool, runtime.Object, error) {
		gets++
		if gets == 1 {
			return true, pod.DeepCopy(), nil
		}
		return true, nil, errors.New("recheck forbidden")
	})

	g, err := kube.NewCollector(client).CollectPod(context.Background(), "ns", pod.Name)
	require.NoError(t, err)
	require.Equal(t, graph.FocusStabilityUnknown, g.Collection.FocusStability)
	res := diag.NewEngine().Evaluate(g)
	require.Equal(t, diag.HealthUnknown, res.Health)
	require.Empty(t, res.Findings)
	require.True(t, hasWarningSource(res, graph.SourceTemporalIntegrity))
}

func TestStaleDeploymentObservedGenerationSuspendsDiagnosticRules(t *testing.T) {
	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "api", Namespace: "ns", UID: "dep-uid", ResourceVersion: "10", Generation: 4,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
		},
		Status: appsv1.DeploymentStatus{ObservedGeneration: 3},
	}
	client := fakeClient(dep)

	g, err := kube.NewCollector(client).CollectDeployment(context.Background(), "ns", "api")
	require.NoError(t, err)
	require.Equal(t, graph.FocusStabilityStable, g.Collection.FocusStability)
	res := diag.NewEngine().Evaluate(g)
	require.Equal(t, diag.HealthUnknown, res.Health)
	require.Empty(t, res.Findings)
	require.Contains(t, res.Rules.SuspendedReason, "controller freshness")
	require.True(t, hasWarningSource(res, graph.SourceTemporalIntegrity))
}

func TestStaleStatefulSetObservedGenerationSuspendsDiagnosticRules(t *testing.T) {
	replicas := int32(1)
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "database", Namespace: "ns", UID: "sts-uid", ResourceVersion: "10", Generation: 4,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "database"}},
		},
		Status: appsv1.StatefulSetStatus{ObservedGeneration: 3},
	}
	client := fakeClient(sts)

	g, err := kube.NewCollector(client).CollectStatefulSet(context.Background(), "ns", "database")
	require.NoError(t, err)
	require.Equal(t, graph.FocusStabilityStable, g.Collection.FocusStability)
	res := diag.NewEngine().Evaluate(g)
	require.Equal(t, diag.HealthUnknown, res.Health)
	require.Empty(t, res.Findings)
	require.Contains(t, res.Rules.SuspendedReason, "controller freshness")
	require.True(t, hasWarningSource(res, graph.SourceTemporalIntegrity))
}

func TestStatefulSetCollectorRetainsOnlyExactOwnedPods(t *testing.T) {
	replicas := int32(1)
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "database", Namespace: "ns", UID: "sts-uid", ResourceVersion: "10", Generation: 1,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "database"}},
		},
		Status: appsv1.StatefulSetStatus{ObservedGeneration: 1, AvailableReplicas: 1, ReadyReplicas: 1},
	}
	owned := readyPod("database-0")
	owned.Labels = map[string]string{"app": "database"}
	owned.OwnerReferences = []metav1.OwnerReference{statefulSetOwnerRef("database", "sts-uid")}
	unrelated := readyPod("lookalike-0")
	unrelated.Labels = map[string]string{"app": "database"}
	unrelated.OwnerReferences = []metav1.OwnerReference{statefulSetOwnerRef("lookalike", "other-uid")}
	nonController := readyPod("database-helper")
	nonController.Labels = map[string]string{"app": "database"}
	nonController.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: appsv1.SchemeGroupVersion.String(), Kind: "StatefulSet",
		Name: "database", UID: "sts-uid",
	}}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "database", Namespace: "ns"},
		Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "database"}},
	}
	client := fakeClient(sts, owned, unrelated, nonController, svc)

	g, err := kube.NewCollector(client).CollectStatefulSet(context.Background(), "ns", "database")
	require.NoError(t, err)
	stsRef := graph.ResourceRef{Kind: "StatefulSet", Namespace: "ns", Name: "database"}
	ownedRef := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "database-0"}
	unrelatedRef := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "lookalike-0"}
	nonControllerRef := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "database-helper"}
	svcRef := graph.ResourceRef{Kind: "Service", Namespace: "ns", Name: "database"}
	require.True(t, g.HasEdge(stsRef, ownedRef, graph.EdgeOwns))
	require.True(t, g.HasEdge(svcRef, ownedRef, graph.EdgeSelects))
	_, exists := g.Node(unrelatedRef)
	require.False(t, exists, "matching labels do not prove StatefulSet ownership")
	_, exists = g.Node(nonControllerRef)
	require.False(t, exists, "a non-controller ownerReference does not prove StatefulSet lifecycle ownership")
}

func TestPodCollectorResolvesDirectStatefulSetOwner(t *testing.T) {
	pod := readyPod("database-0")
	pod.UID = "pod-uid"
	pod.OwnerReferences = []metav1.OwnerReference{statefulSetOwnerRef("database", "sts-uid")}
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "database", Namespace: "ns", UID: "sts-uid", Generation: 1},
		Status:     appsv1.StatefulSetStatus{ObservedGeneration: 1},
	}
	client := fakeClient(pod, sts)

	g, err := kube.NewCollector(client).CollectPod(context.Background(), "ns", "database-0")
	require.NoError(t, err)
	require.True(t, g.HasEdge(
		graph.ResourceRef{Kind: "StatefulSet", Namespace: "ns", Name: "database"},
		graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "database-0"},
		graph.EdgeOwns,
	))
}

func TestPodCollectorRejectsRecreatedSameNameStatefulSetOwner(t *testing.T) {
	pod := readyPod("database-0")
	pod.UID = "pod-uid"
	pod.OwnerReferences = []metav1.OwnerReference{statefulSetOwnerRef("database", "old-sts-uid")}
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "database", Namespace: "ns", UID: "new-sts-uid", Generation: 1},
		Status:     appsv1.StatefulSetStatus{ObservedGeneration: 1},
	}
	client := fakeClient(pod, sts)

	g, err := kube.NewCollector(client).CollectPod(context.Background(), "ns", "database-0")
	require.NoError(t, err)
	stsRef := graph.ResourceRef{Kind: "StatefulSet", Namespace: "ns", Name: "database"}
	podRef := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "database-0"}
	require.False(t, g.HasEdge(stsRef, podRef, graph.EdgeOwns),
		"same name with a different UID is not the Pod owner")
	res := diag.NewEngine().Evaluate(g)
	require.True(t, hasWarningSource(res, graph.SourceStatefulSet))
}

func TestStatefulSetPodListFailureKeepsStatusDiagnosisAvailable(t *testing.T) {
	replicas := int32(1)
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "database", Namespace: "ns", UID: "sts-uid", ResourceVersion: "10", Generation: 1,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "database"}},
		},
		Status: appsv1.StatefulSetStatus{ObservedGeneration: 1, AvailableReplicas: 0},
	}
	client := fakeClient(sts)
	client.PrependReactor("list", "pods", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})

	g, err := kube.NewCollector(client).CollectStatefulSet(context.Background(), "ns", "database")
	require.NoError(t, err)
	res := diag.NewEngine().Evaluate(g)
	require.Equal(t, diag.HealthDegraded, res.Health)
	require.True(t, res.Partial)
	require.True(t, hasWarningSource(res, graph.SourcePod))
	require.True(t, hasFindingType(res, diag.StatefulSetUnavailable))
}

func TestStaleRelatedDeploymentSuspendsServiceDiagnosis(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "api", Namespace: "ns", UID: "svc-uid", ResourceVersion: "20",
		},
		Spec: corev1.ServiceSpec{Selector: map[string]string{"app": "api"}},
	}
	pod := readyPod("api-1")
	pod.UID = "pod-uid"
	pod.OwnerReferences = []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "api-rs", UID: "rs-uid"}}
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "api-rs", Namespace: "ns", UID: "rs-uid", Generation: 1,
			OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: "api", UID: "dep-uid"}},
		},
		Status: appsv1.ReplicaSetStatus{ObservedGeneration: 1},
	}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns", UID: "dep-uid", Generation: 2},
		Status:     appsv1.DeploymentStatus{ObservedGeneration: 1},
	}
	client := fakeClient(svc, pod, rs, dep)

	g, err := kube.NewCollector(client).CollectService(context.Background(), "ns", "api")
	require.NoError(t, err)
	require.Equal(t, graph.FocusStabilityStable, g.Collection.FocusStability)
	res := diag.NewEngine().Evaluate(g)

	require.Equal(t, diag.HealthUnknown, res.Health)
	require.Empty(t, res.Findings)
	require.True(t, hasWarningForResource(res, graph.SourceTemporalIntegrity, graph.ResourceRef{
		Kind: "Deployment", Namespace: "ns", Name: "api",
	}))
}

func TestStaleRelatedReplicaSetSuspendsDeploymentDiagnosis(t *testing.T) {
	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "api", Namespace: "ns", UID: "dep-uid", ResourceVersion: "20", Generation: 2,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
		},
		Status: appsv1.DeploymentStatus{ObservedGeneration: 2},
	}
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "api-rs", Namespace: "ns", UID: "rs-uid", Generation: 3,
			Labels:          map[string]string{"app": "api"},
			OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: "api", UID: "dep-uid"}},
		},
		Status: appsv1.ReplicaSetStatus{ObservedGeneration: 2},
	}
	client := fakeClient(dep, rs)

	g, err := kube.NewCollector(client).CollectDeployment(context.Background(), "ns", "api")
	require.NoError(t, err)
	res := diag.NewEngine().Evaluate(g)

	require.Equal(t, diag.HealthUnknown, res.Health)
	require.Empty(t, res.Findings)
	require.True(t, hasWarningForResource(res, graph.SourceTemporalIntegrity, graph.ResourceRef{
		Kind: "ReplicaSet", Namespace: "ns", Name: "api-rs",
	}))
}

func TestFreshRelatedControllersAllowRuleEvaluation(t *testing.T) {
	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "api", Namespace: "ns", UID: "dep-uid", ResourceVersion: "20", Generation: 2,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
		},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 2, AvailableReplicas: 1, ReadyReplicas: 1, UpdatedReplicas: 1,
		},
	}
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "api-rs", Namespace: "ns", UID: "rs-uid", Generation: 3,
			Labels:          map[string]string{"app": "api"},
			OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: "api", UID: "dep-uid"}},
		},
		Status: appsv1.ReplicaSetStatus{ObservedGeneration: 3},
	}
	client := fakeClient(dep, rs)

	g, err := kube.NewCollector(client).CollectDeployment(context.Background(), "ns", "api")
	require.NoError(t, err)
	res := diag.NewEngine().Evaluate(g)

	require.Equal(t, diag.HealthOK, res.Health)
	require.Equal(t, len(diag.DefaultRuleRegistrations()), res.Rules.Evaluated)
	require.Empty(t, res.Rules.SuspendedReason)
	require.False(t, hasWarningSource(res, graph.SourceTemporalIntegrity))
}

func TestEventsAreListedOnlyForRelatedNonReadyPodsWithFieldSelector(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "api"},
			Ports:    []corev1.ServicePort{{Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080)}},
		},
	}
	ready := readyPod("api-ready")
	ready.UID = "ready-uid"
	notReady := readyPod("api-not-ready")
	notReady.UID = "not-ready-uid"
	notReady.Status.Conditions[0].Status = corev1.ConditionFalse
	client := fakeClient(svc, ready, notReady)

	_, err := kube.NewCollector(client).CollectService(context.Background(), "ns", "api")
	require.NoError(t, err)

	var eventLists []clienttesting.ListAction
	for _, action := range client.Actions() {
		if action.GetVerb() == "list" && action.GetResource().Resource == "events" {
			eventLists = append(eventLists, action.(clienttesting.ListAction))
		}
	}
	require.Len(t, eventLists, 1)
	fieldSelector := eventLists[0].GetListRestrictions().Fields.String()
	require.Contains(t, fieldSelector, "involvedObject.kind=Pod")
	require.Contains(t, fieldSelector, "involvedObject.name=api-not-ready")
	require.Contains(t, fieldSelector, "involvedObject.uid=not-ready-uid")
	require.NotContains(t, fieldSelector, "api-ready")
}

func TestCollectorRejectsWrongUIDEventsDespiteFieldSelector(t *testing.T) {
	pod := readyPod("stale-event")
	pod.UID = "current-uid"
	pod.Status.Conditions[0].Status = corev1.ConditionFalse
	client := fakeClient(pod)
	client.PrependReactor("list", "events", func(action clienttesting.Action) (bool, runtime.Object, error) {
		listAction := action.(clienttesting.ListAction)
		fieldSelector := listAction.GetListRestrictions().Fields.String()
		require.Contains(t, fieldSelector, "involvedObject.kind=Pod")
		require.Contains(t, fieldSelector, "involvedObject.name=stale-event")
		require.Contains(t, fieldSelector, "involvedObject.uid=current-uid")
		return true, &corev1.EventList{Items: []corev1.Event{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "current", Namespace: "ns"},
				InvolvedObject: corev1.ObjectReference{
					Kind: "Pod", Namespace: "ns", Name: "stale-event", UID: "current-uid",
				},
				Reason: "Failed", Message: "InvalidImageName",
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "stale", Namespace: "ns"},
				InvolvedObject: corev1.ObjectReference{
					Kind: "Pod", Namespace: "ns", Name: "stale-event", UID: "old-uid",
				},
				Reason: "Unhealthy", Message: "Readiness probe failed",
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "anonymous", Namespace: "ns"},
				InvolvedObject: corev1.ObjectReference{
					Kind: "Pod", Namespace: "ns", Name: "stale-event",
				},
				Reason: "Unhealthy", Message: "Readiness probe failed",
			},
		}}, nil
	})

	g, err := kube.NewCollector(client).CollectPod(context.Background(), "ns", "stale-event")
	require.NoError(t, err)
	events := g.EventsFor(graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "stale-event"})
	require.Len(t, events, 1)
	require.Equal(t, "current", events[0].Name)
	require.Equal(t, "current-uid", string(events[0].InvolvedObject.UID))
}

func TestServerVersionFailureSkipsVersionScopedRules(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "api"}},
	}
	client := fakeClient(svc)
	client.PrependReactor("get", "version", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("version endpoint unavailable")
	})

	g, err := kube.NewCollector(client).CollectService(context.Background(), "ns", "api")
	require.NoError(t, err)
	res := diag.NewEngine().Evaluate(g)

	require.Equal(t, diag.HealthUnknown, res.Health)
	require.Zero(t, res.Rules.Evaluated)
	require.Len(t, res.Rules.Skipped, len(diag.DefaultRuleRegistrations()))
	require.True(t, hasWarningSource(res, graph.SourceServerVersion))
	for _, finding := range res.Findings {
		require.NotEqual(t, diag.ServiceNoReadyEndpoints, finding.Type)
	}
}

func TestServicePodListFailureIsPartialNotFalseSelectorNoPods(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "api"},
			Ports:    []corev1.ServicePort{{Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080)}},
		},
	}
	ready := true
	slice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name: "api-1", Namespace: "ns",
			Labels: map[string]string{discoveryv1.LabelServiceName: "api"},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
		Ports:       collectorTestEndpointPorts(),
		Endpoints: []discoveryv1.Endpoint{{
			Addresses: []string{"10.0.0.1"}, Conditions: discoveryv1.EndpointConditions{Ready: &ready},
		}},
	}
	client := fakeClient(svc, slice)
	client.PrependReactor("list", "pods", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})

	g, err := kube.NewCollector(client).CollectService(context.Background(), "ns", "api")
	require.NoError(t, err)
	res := diag.NewEngine().Evaluate(g)
	require.Equal(t, diag.HealthOK, res.Health)
	require.True(t, res.Partial)
	require.True(t, hasWarningSource(res, graph.SourcePod))
	for _, finding := range res.Findings {
		require.NotEqual(t, diag.ServiceSelectorNoPods, finding.Type)
	}
}

func TestServiceWithNoSelectedPodsDoesNotListCandidateDeployments(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "api"},
			Ports:    []corev1.ServicePort{{Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080)}},
		},
	}
	client := fakeClient(svc)

	g, err := kube.NewCollector(client).CollectService(context.Background(), "ns", "api")
	require.NoError(t, err)
	res := diag.NewEngine().Evaluate(g)
	require.True(t, hasFindingType(res, diag.ServiceSelectorNoPods))

	for _, action := range client.Actions() {
		require.False(t,
			action.GetVerb() == "list" && action.GetResource().Resource == "deployments",
			"Service collection must not scan Deployments to guess operator intent")
	}
}

func TestPodServiceListFailureKeepsPodDiagnosisAvailable(t *testing.T) {
	pod := readyPod("api-1")
	client := fakeClient(pod)
	client.PrependReactor("list", "services", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})

	g, err := kube.NewCollector(client).CollectPod(context.Background(), "ns", "api-1")
	require.NoError(t, err)
	res := diag.NewEngine().Evaluate(g)
	require.Equal(t, diag.HealthOK, res.Health)
	require.True(t, res.Partial)
	require.True(t, hasWarningSource(res, graph.SourceService))
}

func TestPodCollectorBroadServiceReadHasConstantRequestCount(t *testing.T) {
	objects, _ := podServiceNamespaceObjects(250)
	client := fakeClient(objects...)

	_, err := kube.NewCollector(client).CollectPod(context.Background(), "ns", "api-1")
	require.NoError(t, err)

	require.Equal(t, 2, actionCount(client.Actions(), "get", "pods"), "focus Pod is read at collection start and end")
	require.Equal(t, 1, actionCount(client.Actions(), "list", "services"), "reverse Service discovery is one broad namespace LIST")
	require.Equal(t, 1, actionCount(client.Actions(), "list", "endpointslices"), "only the one matching Service needs EndpointSlices")
}

func TestDeploymentReplicaSetListFailureKeepsStatusDiagnosisAvailable(t *testing.T) {
	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
		},
		Status: appsv1.DeploymentStatus{AvailableReplicas: 1, ReadyReplicas: 1, UpdatedReplicas: 1},
	}
	client := fakeClient(dep)
	client.PrependReactor("list", "replicasets", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})

	g, err := kube.NewCollector(client).CollectDeployment(context.Background(), "ns", "api")
	require.NoError(t, err)
	res := diag.NewEngine().Evaluate(g)
	require.Equal(t, diag.HealthOK, res.Health)
	require.True(t, res.Partial)
	require.True(t, hasWarningSource(res, graph.SourceReplicaSet))
}

func TestCollectorDoesNotReadSecretPayload(t *testing.T) {
	pod := readyPod("api-1")
	pod.Spec.Containers[0].EnvFrom = []corev1.EnvFromSource{{
		SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "credentials"}},
	}}
	client := fakeClient(pod)
	secretRead := false
	client.PrependReactor("get", "secrets", func(clienttesting.Action) (bool, runtime.Object, error) {
		secretRead = true
		return true, nil, errors.New("secret payload must not be read")
	})

	g, err := kube.NewCollector(client).CollectPod(context.Background(), "ns", "api-1")
	require.NoError(t, err)
	require.False(t, secretRead)
	node, ok := g.Node(graph.ResourceRef{Kind: "Secret", Namespace: "ns", Name: "credentials"})
	require.True(t, ok)
	require.True(t, node.ExistenceUnknown())

	res := diag.NewEngine().Evaluate(g)
	require.Equal(t, diag.HealthOK, res.Health)
	require.True(t, res.Partial)
	for _, finding := range res.Findings {
		require.NotEqual(t, diag.MissingConfigRef, finding.Type)
	}
}

func TestOptionalSecretReferenceNeedsNoLookupOrWarning(t *testing.T) {
	pod := readyPod("api-1")
	optional := true
	pod.Spec.Containers[0].EnvFrom = []corev1.EnvFromSource{{
		SecretRef: &corev1.SecretEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: "optional-credentials"},
			Optional:             &optional,
		},
	}}
	client := fakeClient(pod)
	secretRead := false
	client.PrependReactor("get", "secrets", func(clienttesting.Action) (bool, runtime.Object, error) {
		secretRead = true
		return true, nil, errors.New("secret payload must not be read")
	})

	g, err := kube.NewCollector(client).CollectPod(context.Background(), "ns", "api-1")
	require.NoError(t, err)
	require.False(t, secretRead)
	require.Empty(t, g.CollectionIssues())
	_, exists := g.Node(graph.ResourceRef{Kind: "Secret", Namespace: "ns", Name: "optional-credentials"})
	require.False(t, exists)
}

func TestPodCollectorGetsEachReferencedPersistentVolumeClaimOnce(t *testing.T) {
	pod := readyPod("api-1")
	pod.Spec.Volumes = []corev1.Volume{
		{
			Name: "data-a",
			VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: "shared-data",
			}},
		},
		{
			Name: "data-b",
			VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: "shared-data",
			}},
		},
	}
	claim := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "shared-data", Namespace: "ns"},
		Status:     corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound},
	}
	client := fakeClient(pod, claim)

	g, err := kube.NewCollector(client).CollectPod(context.Background(), "ns", "api-1")
	require.NoError(t, err)
	claimRef := graph.ResourceRef{Kind: "PersistentVolumeClaim", Namespace: "ns", Name: "shared-data"}
	podRef := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api-1"}
	node, ok := g.Node(claimRef)
	require.True(t, ok)
	require.IsType(t, &corev1.PersistentVolumeClaim{}, node.Object)
	require.True(t, g.HasEdge(podRef, claimRef, graph.EdgeClaimsStorage))
	require.Equal(t, 1, actionCount(client.Actions(), "get", "persistentvolumeclaims"))
}

func TestPersistentVolumeClaimLookupFailureRemainsUnknown(t *testing.T) {
	pod := readyPod("api-1")
	pod.Status.Phase = corev1.PodPending
	pod.Status.Conditions[0].Status = corev1.ConditionFalse
	pod.Spec.Volumes = []corev1.Volume{{
		Name: "data",
		VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
			ClaimName: "data",
		}},
	}}
	client := fakeClient(pod)
	client.PrependReactor("get", "persistentvolumeclaims", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})

	g, err := kube.NewCollector(client).CollectPod(context.Background(), "ns", "api-1")
	require.NoError(t, err)
	claimRef := graph.ResourceRef{Kind: "PersistentVolumeClaim", Namespace: "ns", Name: "data"}
	node, ok := g.Node(claimRef)
	require.True(t, ok)
	require.True(t, node.ExistenceUnknown())

	res := diag.NewEngine().Evaluate(g)
	require.True(t, res.Partial)
	require.True(t, hasWarningSource(res, graph.SourcePersistentVolumeClaim))
	require.False(t, hasFindingType(res, diag.PersistentVolumeClaimMissing))
	require.False(t, hasFindingType(res, diag.PersistentVolumeClaimPending))
}

func TestMissingPersistentVolumeClaimBecomesExactSchedulingRoot(t *testing.T) {
	pod := readyPod("api-1")
	pod.Status.Phase = corev1.PodPending
	pod.Status.Conditions = []corev1.PodCondition{
		{
			Type: corev1.PodScheduled, Status: corev1.ConditionFalse,
			Reason:  corev1.PodReasonUnschedulable,
			Message: `persistentvolumeclaim "data" not found`,
		},
		{Type: corev1.PodReady, Status: corev1.ConditionFalse},
	}
	pod.Spec.Volumes = []corev1.Volume{{
		Name: "data",
		VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
			ClaimName: "data",
		}},
	}}
	client := fakeClient(pod)

	g, err := kube.NewCollector(client).CollectPod(context.Background(), "ns", "api-1")
	require.NoError(t, err)
	claimRef := graph.ResourceRef{Kind: "PersistentVolumeClaim", Namespace: "ns", Name: "data"}
	node, ok := g.Node(claimRef)
	require.True(t, ok)
	require.True(t, node.Missing())

	res := diag.NewEngine().Evaluate(g)
	require.Equal(t, diag.HealthDegraded, res.Health)
	require.False(t, res.Partial)
	require.Len(t, res.RootCauses, 1)
	require.Equal(t, diag.PersistentVolumeClaimMissing, res.RootCauses[0].Type)
	require.Equal(t, claimRef, res.RootCauses[0].Resource)
}

func TestDeploymentCollectorRetainsOnlyExactTargetHorizontalPodAutoscaler(t *testing.T) {
	replicas := int32(2)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns", UID: "dep-uid", Generation: 1},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
		},
		Status: appsv1.DeploymentStatus{ObservedGeneration: 1, AvailableReplicas: 2, ReadyReplicas: 2},
	}
	related := testHPA("api-autoscaler", "apps/v1", "Deployment", "api")
	unrelated := testHPA("other-autoscaler", "apps/v1", "Deployment", "other")
	wrongVersion := testHPA("legacy-autoscaler", "extensions/v1beta1", "Deployment", "api")
	client := fakeClient(dep, related, unrelated, wrongVersion)

	g, err := kube.NewCollector(client).CollectDeployment(context.Background(), "ns", "api")
	require.NoError(t, err)
	require.Equal(t, 1, actionCount(client.Actions(), "list", "horizontalpodautoscalers"))

	hpaRef := graph.ResourceRef{Kind: "HorizontalPodAutoscaler", Namespace: "ns", Name: "api-autoscaler"}
	depRef := graph.ResourceRef{Kind: "Deployment", Namespace: "ns", Name: "api"}
	node, ok := g.Node(hpaRef)
	require.True(t, ok)
	require.IsType(t, &autoscalingv2.HorizontalPodAutoscaler{}, node.Object)
	require.True(t, g.HasEdge(hpaRef, depRef, graph.EdgeScales))

	_, ok = g.Node(graph.ResourceRef{Kind: "HorizontalPodAutoscaler", Namespace: "ns", Name: "other-autoscaler"})
	require.False(t, ok)
	_, ok = g.Node(graph.ResourceRef{Kind: "HorizontalPodAutoscaler", Namespace: "ns", Name: "legacy-autoscaler"})
	require.False(t, ok)
}

func TestHorizontalPodAutoscalerListFailureIsPartialAndDoesNotInventFinding(t *testing.T) {
	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns", UID: "dep-uid", Generation: 1},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
		},
		Status: appsv1.DeploymentStatus{ObservedGeneration: 1, AvailableReplicas: 1, ReadyReplicas: 1},
	}
	client := fakeClient(dep)
	client.PrependReactor("list", "horizontalpodautoscalers", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})

	g, err := kube.NewCollector(client).CollectDeployment(context.Background(), "ns", "api")
	require.NoError(t, err)
	res := diag.NewEngine().Evaluate(g)
	require.Equal(t, diag.HealthOK, res.Health)
	require.True(t, res.Partial)
	require.True(t, hasWarningSource(res, graph.SourceHorizontalPodAutoscaler))
	require.False(t, hasFindingType(res, diag.HPAMetricsUnavailable))
	require.False(t, hasFindingType(res, diag.HPAScaleUpLimited))
}

func TestStaleHorizontalPodAutoscalerStatusIsPartialAndSuppressed(t *testing.T) {
	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns", UID: "dep-uid", Generation: 1},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
		},
		Status: appsv1.DeploymentStatus{ObservedGeneration: 1, AvailableReplicas: 1, ReadyReplicas: 1},
	}
	observed := int64(1)
	hpa := testHPA("api", "apps/v1", "Deployment", "api")
	hpa.Generation = 2
	hpa.Status.ObservedGeneration = &observed
	hpa.Status.Conditions = []autoscalingv2.HorizontalPodAutoscalerCondition{{
		Type: autoscalingv2.ScalingActive, Status: corev1.ConditionFalse, Reason: "FailedGetResourceMetric",
	}}
	client := fakeClient(dep, hpa)

	g, err := kube.NewCollector(client).CollectDeployment(context.Background(), "ns", "api")
	require.NoError(t, err)
	res := diag.NewEngine().Evaluate(g)
	require.Equal(t, diag.HealthOK, res.Health)
	require.True(t, res.Partial)
	require.True(t, hasWarningForResource(res, graph.SourceHorizontalPodAutoscaler, graph.ResourceRef{
		Kind: "HorizontalPodAutoscaler", Namespace: "ns", Name: "api",
	}))
	require.False(t, hasFindingType(res, diag.HPAMetricsUnavailable))
}

func TestPodWithoutDeploymentOwnerDoesNotListHorizontalPodAutoscalers(t *testing.T) {
	client := fakeClient(readyPod("standalone"))

	_, err := kube.NewCollector(client).CollectPod(context.Background(), "ns", "standalone")
	require.NoError(t, err)
	require.Zero(t, actionCount(client.Actions(), "list", "horizontalpodautoscalers"))
}

func TestMissingProjectedConfigMapKeepsContainerSubject(t *testing.T) {
	pod := readyPod("api-1")
	pod.Status.Conditions[0].Status = corev1.ConditionFalse
	pod.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{{Name: "config", MountPath: "/etc/api"}}
	pod.Spec.Volumes = []corev1.Volume{{
		Name: "config",
		VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{Sources: []corev1.VolumeProjection{{
			ConfigMap: &corev1.ConfigMapProjection{LocalObjectReference: corev1.LocalObjectReference{Name: "api-config"}},
		}}}},
	}}
	client := fakeClient(pod)

	g, err := kube.NewCollector(client).CollectPod(context.Background(), "ns", "api-1")
	require.NoError(t, err)
	res := diag.NewEngine().Evaluate(g)
	var missing *diag.Finding
	for _, finding := range res.Findings {
		if finding.Type == diag.MissingConfigRef {
			missing = finding
			break
		}
	}
	require.NotNil(t, missing)
	require.Equal(t, "app", missing.Subject.Container)
	require.Contains(t, missing.Evidence[0].Source, "projected")
}

func readyPod(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", Labels: map[string]string{"app": "api"}},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "example/api"}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{
			{Type: corev1.PodReady, Status: corev1.ConditionTrue},
		}},
	}
}

func testHPA(name, apiVersion, kind, targetName string) *autoscalingv2.HorizontalPodAutoscaler {
	return &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns"},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: apiVersion, Kind: kind, Name: targetName,
			},
			MaxReplicas: 5,
		},
	}
}

func fakeClient(objects ...runtime.Object) *fake.Clientset {
	client := fake.NewClientset(objects...)
	client.Discovery().(*fakediscovery.FakeDiscovery).FakedServerVersion = &versioninfo.Info{GitVersion: "v1.36.2"}
	return client
}

func collectorTestEndpointPorts() []discoveryv1.EndpointPort {
	name, port, protocol := "http", int32(8080), corev1.ProtocolTCP
	return []discoveryv1.EndpointPort{{Name: &name, Port: &port, Protocol: &protocol}}
}

func hasWarningSource(res *diag.Result, source string) bool {
	for _, warning := range res.Warnings {
		if warning.Source == source {
			return true
		}
	}
	return false
}

func hasWarningForResource(res *diag.Result, source string, ref graph.ResourceRef) bool {
	for _, warning := range res.Warnings {
		if warning.Source == source && warning.Resource == ref {
			return true
		}
	}
	return false
}

func hasFindingType(res *diag.Result, findingType diag.FindingType) bool {
	for _, finding := range res.Findings {
		if finding.Type == findingType {
			return true
		}
	}
	return false
}

func actionCount(actions []clienttesting.Action, verb, resource string) int {
	count := 0
	for _, action := range actions {
		if action.GetVerb() == verb && action.GetResource().Resource == resource {
			count++
		}
	}
	return count
}

func statefulSetOwnerRef(name string, uid types.UID) metav1.OwnerReference {
	controller := true
	return metav1.OwnerReference{
		APIVersion: appsv1.SchemeGroupVersion.String(),
		Kind:       "StatefulSet",
		Name:       name,
		UID:        uid,
		Controller: &controller,
	}
}
