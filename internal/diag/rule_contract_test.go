package diag

import (
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/leventozen/kubectl-tuna/internal/graph"
)

// TestBuiltInRuleContracts is the direct rule matrix. It deliberately calls
// each rule without the collector, engine, or correlator so scenario fixtures
// cannot accidentally hide an untested trigger. Registry parity makes adding
// a built-in rule or finding type without a contract test fail this suite.
func TestBuiltInRuleContracts(t *testing.T) {
	type positiveCase struct {
		findingType FindingType
		graph       func() *graph.Graph
	}
	type contract struct {
		rule      Rule
		positives []positiveCase
		negative  func() *graph.Graph
	}

	contracts := []contract{
		{
			rule: serviceSelectorNoPodsRule{},
			positives: []positiveCase{{ServiceSelectorNoPods, func() *graph.Graph {
				return selectorContractGraph(false)
			}}},
			negative: func() *graph.Graph { return selectorContractGraph(true) },
		},
		{
			rule: serviceTargetPortMismatchRule{},
			positives: []positiveCase{{ServiceTargetPortMismatch, func() *graph.Graph {
				return serviceWithTargetPort(intstr.FromString("web"), []corev1.ContainerPort{{Name: "http", ContainerPort: 8080}})
			}}},
			negative: func() *graph.Graph {
				return serviceWithTargetPort(intstr.FromString("http"), []corev1.ContainerPort{{Name: "http", ContainerPort: 8080}})
			},
		},
		{
			rule: serviceNoReadyEndpointsRule{},
			positives: []positiveCase{
				{ServiceNoReadyEndpoints, func() *graph.Graph {
					return endpointContractGraph(false, false, false)
				}},
				{ServiceTerminatingOnly, func() *graph.Graph {
					return endpointContractGraph(false, true, true)
				}},
			},
			negative: func() *graph.Graph { return endpointContractGraph(true, false, false) },
		},
		{
			rule: readinessProbePortRule{},
			positives: []positiveCase{{ReadinessProbePortInvalid, func() *graph.Graph {
				return readinessPortContractGraph("health")
			}}},
			negative: func() *graph.Graph { return readinessPortContractGraph("http") },
		},
		{
			rule: readinessProbeFailingRule{},
			positives: []positiveCase{{ReadinessProbeFailing, func() *graph.Graph {
				return readinessEventContractGraph("Readiness probe failed: connection refused")
			}}},
			negative: func() *graph.Graph {
				return readinessEventContractGraph("Liveness probe failed: connection refused")
			},
		},
		{
			rule: podNotReadyRule{},
			positives: []positiveCase{{PodNotReady, func() *graph.Graph {
				return podReadyContractGraph(false)
			}}},
			negative: func() *graph.Graph { return podReadyContractGraph(true) },
		},
		{
			rule: crashLoopRule{},
			positives: []positiveCase{{CrashLoopBackOff, func() *graph.Graph {
				return waitingStateContractGraph("CrashLoopBackOff")
			}}},
			negative: func() *graph.Graph { return waitingStateContractGraph("ContainerCreating") },
		},
		{
			rule: imagePullRule{},
			positives: []positiveCase{{ImagePullFailure, func() *graph.Graph {
				return waitingStateContractGraph("ErrImagePull")
			}}},
			negative: func() *graph.Graph { return waitingStateContractGraph("ContainerCreating") },
		},
		{
			rule: missingConfigRefRule{},
			positives: []positiveCase{{MissingConfigRef, func() *graph.Graph {
				return configReferenceContractGraph(false)
			}}},
			negative: func() *graph.Graph { return configReferenceContractGraph(true) },
		},
		{
			rule: oomKilledRule{},
			positives: []positiveCase{
				{ContainerOOMKilled, func() *graph.Graph { return terminationContractGraph("OOMKilled", 137, 1) }},
				{ContainerSIGKILL, func() *graph.Graph { return terminationContractGraph("Error", 137, 1) }},
			},
			negative: func() *graph.Graph { return terminationContractGraph("Error", 1, 1) },
		},
		{
			rule: deploymentUnavailableRule{},
			positives: []positiveCase{{DeploymentUnavailable, func() *graph.Graph {
				return deploymentAvailabilityContractGraph(0)
			}}},
			negative: func() *graph.Graph { return deploymentAvailabilityContractGraph(1) },
		},
		{
			rule: rolloutStuckRule{},
			positives: []positiveCase{{RolloutStuck, func() *graph.Graph {
				return rolloutContractGraph(corev1.ConditionFalse)
			}}},
			negative: func() *graph.Graph { return rolloutContractGraph(corev1.ConditionTrue) },
		},
		{
			rule: podUnschedulableRule{},
			positives: []positiveCase{{PodUnschedulable, func() *graph.Graph {
				return schedulingContractGraph(corev1.PodReasonUnschedulable)
			}}},
			negative: func() *graph.Graph { return schedulingContractGraph("SchedulingGated") },
		},
		{
			rule: nodePressureRule{},
			positives: []positiveCase{{NodePressure, func() *graph.Graph {
				return nodePressureContractGraph(corev1.ConditionTrue)
			}}},
			negative: func() *graph.Graph { return nodePressureContractGraph(corev1.ConditionFalse) },
		},
		{
			rule: podEvictedRule{},
			positives: []positiveCase{{PodEvicted, func() *graph.Graph {
				return evictionContractGraph("Evicted")
			}}},
			negative: func() *graph.Graph { return evictionContractGraph("Error") },
		},
	}

	registrations := DefaultRuleRegistrations()
	require.Len(t, contracts, len(registrations), "every built-in rule must have exactly one direct contract")
	byID := make(map[string]contract, len(contracts))
	for _, contract := range contracts {
		require.NotNil(t, contract.rule)
		require.NotNil(t, contract.negative)
		require.NotEmpty(t, contract.positives)
		require.NotContains(t, byID, contract.rule.ID(), "duplicate direct contract")
		byID[contract.rule.ID()] = contract
	}

	for _, registration := range registrations {
		registration := registration
		t.Run(registration.Metadata.ID, func(t *testing.T) {
			contract, ok := byID[registration.Metadata.ID]
			require.True(t, ok, "built-in rule has no direct contract")
			require.Equal(t, registration.Metadata.ID, contract.rule.ID())

			coveredTypes := make([]FindingType, 0, len(contract.positives))
			for _, positive := range contract.positives {
				positive := positive
				t.Run("positive/"+string(positive.findingType), func(t *testing.T) {
					findings := contract.rule.Evaluate(positive.graph())
					requireFindingType(t, findings, positive.findingType)
				})
				coveredTypes = append(coveredTypes, positive.findingType)
			}
			require.ElementsMatch(t, registration.Metadata.FindingTypes, coveredTypes,
				"every finding type declared by the rule must have a direct positive contract")

			t.Run("negative", func(t *testing.T) {
				require.Empty(t, contract.rule.Evaluate(contract.negative()),
					"the rule fired across its negative/boundary condition")
			})
		})
	}
}

func requireFindingType(t *testing.T, findings []*Finding, want FindingType) {
	t.Helper()
	for _, finding := range findings {
		if finding.Type == want {
			return
		}
	}
	require.Failf(t, "finding not emitted", "wanted %q, got %#v", want, findings)
}

func selectorContractGraph(selected bool) *graph.Graph {
	svcRef := graph.ResourceRef{Kind: "Service", Namespace: "ns", Name: "api"}
	podRef := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api-1"}
	g := graph.New(svcRef)
	g.AddNode(svcRef, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "api", "tier": "web"},
		},
	})
	if selected {
		g.AddNode(podRef, contractPod())
		g.AddEdge(svcRef, podRef, graph.EdgeSelects)
	}
	return g
}

func endpointContractGraph(ready, serving, terminating bool) *graph.Graph {
	svcRef := graph.ResourceRef{Kind: "Service", Namespace: "ns", Name: "api"}
	podRef := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api-1"}
	sliceRef := graph.ResourceRef{Kind: "EndpointSlice", Namespace: "ns", Name: "api-1"}
	g := graph.New(svcRef)
	g.AddNode(svcRef, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "api"},
			Ports:    testServicePorts(),
		},
	})
	g.AddNode(podRef, contractPod())
	g.AddNode(sliceRef, &discoveryv1.EndpointSlice{
		ObjectMeta:  metav1.ObjectMeta{Name: "api-1", Namespace: "ns"},
		AddressType: discoveryv1.AddressTypeIPv4,
		Ports:       testEndpointPorts(),
		Endpoints: []discoveryv1.Endpoint{{
			Addresses: []string{"10.0.0.1"},
			Conditions: discoveryv1.EndpointConditions{
				Ready:       boolPointer(ready),
				Serving:     boolPointer(serving),
				Terminating: boolPointer(terminating),
			},
		}},
	})
	g.AddEdge(svcRef, podRef, graph.EdgeSelects)
	g.AddEdge(svcRef, sliceRef, graph.EdgeRoutesTo)
	return g
}

func readinessPortContractGraph(probePortName string) *graph.Graph {
	pod := contractPod()
	pod.Spec.Containers[0].Ports = []corev1.ContainerPort{{Name: "http", ContainerPort: 8080}}
	pod.Spec.Containers[0].ReadinessProbe = &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Port: intstr.FromString(probePortName)}},
	}
	return graphWithPod(pod)
}

func readinessEventContractGraph(message string) *graph.Graph {
	pod := contractPod()
	pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}}
	pod.Spec.Containers[0].ReadinessProbe = &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Port: intstr.FromInt32(8080)}},
	}
	g := graphWithPod(pod)
	g.AddEvents(corev1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: "probe", Namespace: "ns"},
		InvolvedObject: corev1.ObjectReference{
			Kind: "Pod", Namespace: "ns", Name: pod.Name,
		},
		Reason:  "Unhealthy",
		Message: message,
	})
	return g
}

func podReadyContractGraph(ready bool) *graph.Graph {
	pod := contractPod()
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: status}}
	return graphWithPod(pod)
}

func waitingStateContractGraph(reason string) *graph.Graph {
	pod := contractPod()
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{{
		Name:  "app",
		Image: "registry.example/app:v1",
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reason}},
	}}
	return graphWithPod(pod)
}

func configReferenceContractGraph(unknown bool) *graph.Graph {
	pod := contractPod()
	pod.Spec.Containers[0].EnvFrom = []corev1.EnvFromSource{{
		ConfigMapRef: &corev1.ConfigMapEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: "settings"},
		},
	}}
	g := graphWithPod(pod)
	ref := graph.ResourceRef{Kind: "ConfigMap", Namespace: "ns", Name: "settings"}
	if unknown {
		g.AddUnknownNode(ref)
	} else {
		g.AddNode(ref, nil)
	}
	return g
}

func terminationContractGraph(reason string, exitCode, restarts int32) *graph.Graph {
	pod := contractPod()
	pod.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
		corev1.ResourceMemory: resource.MustParse("128Mi"),
	}
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{{
		Name:         "app",
		RestartCount: restarts,
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
			Reason: reason, ExitCode: exitCode,
		}},
	}}
	return graphWithPod(pod)
}

func deploymentAvailabilityContractGraph(available int32) *graph.Graph {
	ref := graph.ResourceRef{Kind: "Deployment", Namespace: "ns", Name: "api"}
	replicas := int32(1)
	g := graph.New(ref)
	g.AddNode(ref, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
		Status:     appsv1.DeploymentStatus{AvailableReplicas: available},
	})
	return g
}

func rolloutContractGraph(status corev1.ConditionStatus) *graph.Graph {
	ref := graph.ResourceRef{Kind: "Deployment", Namespace: "ns", Name: "api"}
	g := graph.New(ref)
	g.AddNode(ref, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Status: appsv1.DeploymentStatus{Conditions: []appsv1.DeploymentCondition{{
			Type: appsv1.DeploymentProgressing, Status: status, Reason: "ProgressDeadlineExceeded",
		}}},
	})
	return g
}

func schedulingContractGraph(reason string) *graph.Graph {
	pod := contractPod()
	pod.Status.Phase = corev1.PodPending
	pod.Status.Conditions = []corev1.PodCondition{{
		Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: reason,
	}}
	return graphWithPod(pod)
}

func nodePressureContractGraph(status corev1.ConditionStatus) *graph.Graph {
	ref := graph.ResourceRef{Kind: "Node", Name: "worker-1"}
	g := graph.New(ref)
	g.AddNode(ref, &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-1"},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{
			Type: corev1.NodeMemoryPressure, Status: status, Reason: "KubeletHasInsufficientMemory",
		}}},
	})
	return g
}

func evictionContractGraph(reason string) *graph.Graph {
	pod := contractPod()
	pod.Status.Phase = corev1.PodFailed
	pod.Status.Reason = reason
	return graphWithPod(pod)
}

func graphWithPod(pod *corev1.Pod) *graph.Graph {
	ref := graph.ResourceRef{Kind: "Pod", Namespace: pod.Namespace, Name: pod.Name}
	g := graph.New(ref)
	g.AddNode(ref, pod)
	return g
}

func contractPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api-1", Namespace: "ns"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "app", Image: "registry.example/app:v1",
		}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func boolPointer(value bool) *bool { return &value }
