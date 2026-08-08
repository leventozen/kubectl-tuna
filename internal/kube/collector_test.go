package kube_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
