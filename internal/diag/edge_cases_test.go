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

func TestEndpointSliceNilReadyMeansReady(t *testing.T) {
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
	g.AddNode(podRef, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api-1", Namespace: "ns"},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{
			{Type: corev1.PodReady, Status: corev1.ConditionTrue},
		}},
	})
	g.AddNode(sliceRef, &discoveryv1.EndpointSlice{
		ObjectMeta:  metav1.ObjectMeta{Name: "api-1", Namespace: "ns"},
		AddressType: discoveryv1.AddressTypeIPv4,
		Ports:       testEndpointPorts(),
		Endpoints: []discoveryv1.Endpoint{{
			Addresses: []string{"10.0.0.1"}, Conditions: discoveryv1.EndpointConditions{Ready: nil},
		}},
	})
	g.AddEdge(svcRef, podRef, graph.EdgeSelects)
	g.AddEdge(svcRef, sliceRef, graph.EdgeRoutesTo)

	res := NewEngine().Evaluate(g)
	require.Equal(t, HealthOK, res.Health)
	for _, finding := range res.Findings {
		require.NotEqual(t, ServiceNoReadyEndpoints, finding.Type)
	}
}

func TestReadyEndpointInAnySlicePreventsZeroEndpointFinding(t *testing.T) {
	g := serviceWithTargetPort(intstr.FromString("http"), []corev1.ContainerPort{{Name: "http", ContainerPort: 8080}})
	falseValue := false
	secondSliceRef := graph.ResourceRef{Kind: "EndpointSlice", Namespace: "ns", Name: "api-not-ready"}
	g.AddNode(secondSliceRef, &discoveryv1.EndpointSlice{
		ObjectMeta:  metav1.ObjectMeta{Name: "api-not-ready", Namespace: "ns"},
		AddressType: discoveryv1.AddressTypeIPv4,
		Ports:       testEndpointPorts(),
		Endpoints: []discoveryv1.Endpoint{{
			Addresses: []string{"10.0.0.2"}, Conditions: discoveryv1.EndpointConditions{Ready: &falseValue},
		}},
	})
	g.AddEdge(g.Focus, secondSliceRef, graph.EdgeRoutesTo)

	res := NewEngine().Evaluate(g)
	require.Nil(t, findingByType(res, ServiceNoReadyEndpoints))
	require.Equal(t, HealthOK, res.Health)
}

func TestServingTerminatingEndpointsAreRiskNotConfirmedOutage(t *testing.T) {
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
	g.AddNode(podRef, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api-1", Namespace: "ns"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{
			{Type: corev1.PodReady, Status: corev1.ConditionFalse},
		}},
	})
	ready, serving, terminating := false, true, true
	g.AddNode(sliceRef, &discoveryv1.EndpointSlice{
		ObjectMeta:  metav1.ObjectMeta{Name: "api-1", Namespace: "ns"},
		AddressType: discoveryv1.AddressTypeIPv4,
		Ports:       testEndpointPorts(),
		Endpoints: []discoveryv1.Endpoint{{
			Addresses: []string{"10.0.0.1"},
			Conditions: discoveryv1.EndpointConditions{
				Ready: &ready, Serving: &serving, Terminating: &terminating,
			},
		}},
	})
	g.AddEdge(svcRef, podRef, graph.EdgeSelects)
	g.AddEdge(svcRef, sliceRef, graph.EdgeRoutesTo)

	res := NewEngine().Evaluate(g)
	finding := findingByType(res, ServiceTerminatingOnly)
	require.NotNil(t, finding)
	require.Equal(t, ImpactRisk, finding.Impact)
	require.Nil(t, findingByType(res, ServiceNoReadyEndpoints))
	require.Equal(t, HealthOK, res.Health)
}

func TestReadyEndpointWithoutAddressIsNotEligible(t *testing.T) {
	g := serviceWithTargetPort(intstr.FromString("http"), []corev1.ContainerPort{{Name: "http", ContainerPort: 8080}})
	for _, node := range g.NodesOfKind("EndpointSlice") {
		slice := node.Object.(*discoveryv1.EndpointSlice)
		slice.Endpoints[0].Addresses = nil
	}

	res := NewEngine().Evaluate(g)
	finding := findingByType(res, ServiceNoReadyEndpoints)
	require.NotNil(t, finding)
	require.Contains(t, finding.Evidence[0].Value, "0 endpoint(s)")
	require.Equal(t, HealthDegraded, res.Health)
}

func TestFQDNEndpointDoesNotCountAsProxyEligible(t *testing.T) {
	g := serviceWithTargetPort(intstr.FromString("http"), []corev1.ContainerPort{{Name: "http", ContainerPort: 8080}})
	for _, node := range g.NodesOfKind("EndpointSlice") {
		slice := node.Object.(*discoveryv1.EndpointSlice)
		slice.AddressType = discoveryv1.AddressTypeFQDN
		slice.Endpoints[0].Addresses = []string{"backend.example.com"}
	}

	res := NewEngine().Evaluate(g)
	require.NotNil(t, findingByType(res, ServiceNoReadyEndpoints))
}

func TestEndpointSliceForDifferentPortDoesNotMaskMissingServicePort(t *testing.T) {
	g := serviceWithTargetPort(intstr.FromString("http"), []corev1.ContainerPort{{Name: "http", ContainerPort: 8080}})
	for _, node := range g.NodesOfKind("EndpointSlice") {
		slice := node.Object.(*discoveryv1.EndpointSlice)
		name, port := "metrics", int32(9090)
		slice.Ports[0].Name = &name
		slice.Ports[0].Port = &port
	}

	res := NewEngine().Evaluate(g)
	finding := findingByType(res, ServiceNoReadyEndpoints)
	require.NotNil(t, finding)
	require.Contains(t, finding.Summary, `"http"`)
}

func TestMultiPortServiceReportsOnlyPortWithoutReadyEndpoints(t *testing.T) {
	g := serviceWithTargetPort(
		intstr.FromString("http"),
		[]corev1.ContainerPort{{Name: "http", ContainerPort: 8080}, {Name: "metrics", ContainerPort: 9090}},
	)
	serviceNode, ok := g.Node(g.Focus)
	require.True(t, ok)
	service := serviceNode.Object.(*corev1.Service)
	service.Spec.Ports = append(service.Spec.Ports, corev1.ServicePort{
		Name: "metrics", Port: 9090, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromString("metrics"),
	})

	res := NewEngine().Evaluate(g)
	var endpointFindings []*Finding
	for _, finding := range res.Findings {
		if finding.Type == ServiceNoReadyEndpoints {
			endpointFindings = append(endpointFindings, finding)
		}
	}
	require.Len(t, endpointFindings, 1)
	require.Contains(t, endpointFindings[0].Summary, `"metrics"`)
}

func TestNamedServiceTargetPortMismatchIsCurrent(t *testing.T) {
	g := serviceWithTargetPort(intstr.FromString("web"), []corev1.ContainerPort{{Name: "http", ContainerPort: 8080}})
	res := NewEngine().Evaluate(g)
	finding := findingByType(res, ServiceTargetPortMismatch)
	require.NotNil(t, finding)
	require.Equal(t, ConfidenceHigh, finding.Confidence)
	require.Equal(t, ImpactCurrent, finding.Impact)
	require.Equal(t, HealthDegraded, res.Health)
}

func TestNamedReadinessProbeWithoutDeclaredPortsIsInvalid(t *testing.T) {
	podRef := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api"}
	g := graph.New(podRef)
	g.AddNode(podRef, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "app",
			ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{Port: intstr.FromString("health")},
			}},
		}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{
			{Type: corev1.PodReady, Status: corev1.ConditionFalse},
		}},
	})
	res := NewEngine().Evaluate(g)
	finding := findingByType(res, ReadinessProbePortInvalid)
	require.NotNil(t, finding)
	require.Equal(t, ConfidenceHigh, finding.Confidence)
}

func TestNumericUndeclaredTargetPortIsRiskNotOutage(t *testing.T) {
	g := serviceWithTargetPort(intstr.FromInt32(8080), nil)
	res := NewEngine().Evaluate(g)
	finding := findingByType(res, ServiceTargetPortMismatch)
	require.NotNil(t, finding)
	require.Equal(t, ConfidenceMedium, finding.Confidence)
	require.Equal(t, ImpactRisk, finding.Impact)
	require.Equal(t, HealthOK, res.Health, "an undeclared numeric port is not proof of an outage")
}

func TestRelatedCurrentFindingDoesNotDegradeHealthyFocus(t *testing.T) {
	svcRef := graph.ResourceRef{Kind: "Service", Namespace: "ns", Name: "api"}
	podRef := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api-1"}
	sliceRef := graph.ResourceRef{Kind: "EndpointSlice", Namespace: "ns", Name: "api-1"}
	g := graph.New(svcRef)
	g.AddNode(svcRef, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Spec: corev1.ServiceSpec{
			Selector:                 map[string]string{"app": "api"},
			PublishNotReadyAddresses: true,
			Ports:                    testServicePorts(),
		},
	})
	g.AddNode(podRef, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api-1", Namespace: "ns"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{
			{Type: corev1.PodReady, Status: corev1.ConditionFalse},
		}},
	})
	ready := true // e.g. Service.publishNotReadyAddresses or transitional state
	g.AddNode(sliceRef, &discoveryv1.EndpointSlice{
		ObjectMeta:  metav1.ObjectMeta{Name: "api-1", Namespace: "ns"},
		AddressType: discoveryv1.AddressTypeIPv4,
		Ports:       testEndpointPorts(),
		Endpoints: []discoveryv1.Endpoint{{
			Addresses: []string{"10.0.0.1"}, Conditions: discoveryv1.EndpointConditions{Ready: &ready},
		}},
	})
	g.AddEdge(svcRef, podRef, graph.EdgeSelects)
	g.AddEdge(svcRef, sliceRef, graph.EdgeRoutesTo)

	res := NewEngine().Evaluate(g)
	require.NotNil(t, findingByType(res, PodNotReady))
	require.Equal(t, HealthOK, res.Health, "a related Pod finding must not override healthy evidence on the focus Service")
}

func TestImagePullFailureIncludesContainerSubject(t *testing.T) {
	podRef := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api"}
	g := graph.New(podRef)
	g.AddNode(podRef, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "invalid.example/api:nope"}}},
		Status: corev1.PodStatus{Phase: corev1.PodPending, ContainerStatuses: []corev1.ContainerStatus{{
			Name: "app", Image: "invalid.example/api:nope", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}},
		}}},
	})
	res := NewEngine().Evaluate(g)
	finding := findingByType(res, ImagePullFailure)
	require.NotNil(t, finding)
	require.Equal(t, "app", finding.Subject.Container)
	require.Equal(t, HealthDegraded, res.Health)
}

func TestInitContainerImagePullFailureIsDiagnosed(t *testing.T) {
	podRef := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api"}
	g := graph.New(podRef)
	g.AddNode(podRef, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{{Name: "migrate", Image: "invalid.example/migrate:nope"}},
			Containers:     []corev1.Container{{Name: "app", Image: "example/api"}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodPending, InitContainerStatuses: []corev1.ContainerStatus{{
			Name: "migrate", Image: "invalid.example/migrate:nope", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ErrImagePull"}},
		}}},
	})
	res := NewEngine().Evaluate(g)
	finding := findingByType(res, ImagePullFailure)
	require.NotNil(t, finding)
	require.Equal(t, "migrate", finding.Subject.Container)
}

func TestInitContainerOOMCorrelatesWithItsCrashLoop(t *testing.T) {
	podRef := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api"}
	g := graph.New(podRef)
	g.AddNode(podRef, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{{
				Name: "migrate",
				Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("64Mi"),
				}},
			}},
			Containers: []corev1.Container{{Name: "app"}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodPending, InitContainerStatuses: []corev1.ContainerStatus{{
			Name: "migrate", RestartCount: 3,
			State:                corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
			LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled", ExitCode: 137}},
		}}},
	})
	res := NewEngine().Evaluate(g)
	oom := findingByType(res, ContainerOOMKilled)
	crash := findingByType(res, CrashLoopBackOff)
	require.NotNil(t, oom)
	require.NotNil(t, crash)
	require.Equal(t, "migrate", oom.Subject.Container)
	require.Equal(t, "migrate", crash.Subject.Container)
	require.Contains(t, oom.Causes, crash)
}

func TestHistoricalOOMDoesNotDegradeReadyPod(t *testing.T) {
	podRef := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api"}
	g := graph.New(podRef)
	g.AddNode(podRef, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "app", Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("128Mi")}},
		}}},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "app", Ready: true, RestartCount: 1,
				State:                corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
				LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled", ExitCode: 137}},
			}},
		},
	})
	res := NewEngine().Evaluate(g)
	finding := findingByType(res, ContainerOOMKilled)
	require.NotNil(t, finding)
	require.Equal(t, ImpactHistorical, finding.Impact)
	require.Equal(t, HealthOK, res.Health)
}

func TestCurrentOOMEvidenceNamesCurrentTerminationState(t *testing.T) {
	podRef := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api"}
	g := graph.New(podRef)
	g.AddNode(podRef, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "app", Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			}},
		}}},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}},
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "app", Ready: false, RestartCount: 1,
				State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
					Reason: "OOMKilled", ExitCode: 137,
				}},
			}},
		},
	})

	res := NewEngine().Evaluate(g)
	finding := findingByType(res, ContainerOOMKilled)
	require.NotNil(t, finding)
	require.NotEmpty(t, finding.Evidence)
	require.Equal(t, "containerStatuses[app].state.terminated", finding.Evidence[0].Source)
	require.Equal(t, []*Finding{finding}, findingByType(res, PodNotReady).CausedBy)
}

func TestOOMWithoutMemoryLimitDoesNotClaimCgroupLimit(t *testing.T) {
	podRef := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api"}
	g := graph.New(podRef)
	g.AddNode(podRef, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{
			Name: "app", RestartCount: 1,
			State:                corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
			LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled", ExitCode: 137}},
		}}},
	})
	res := NewEngine().Evaluate(g)
	finding := findingByType(res, ContainerOOMKilled)
	require.NotNil(t, finding)
	require.Equal(t, ConfidenceMedium, finding.Confidence)
	require.Contains(t, finding.Detail, "no memory limit")
	require.NotContains(t, finding.Summary, "memory limit is the likely boundary")
}

func TestExactSelectorOnScaledToZeroDeploymentStillReportsNoSelectedPods(t *testing.T) {
	svcRef := graph.ResourceRef{Kind: "Service", Namespace: "ns", Name: "api"}
	depRef := graph.ResourceRef{Kind: "Deployment", Namespace: "ns", Name: "api"}
	g := graph.New(svcRef)
	g.AddNode(svcRef, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "api"},
			Ports:    testServicePorts(),
		},
	})
	zero := int32(0)
	g.AddNode(depRef, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &zero,
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api"}}},
		},
	})
	res := NewEngine().Evaluate(g)
	selectorFinding := findingByType(res, ServiceSelectorNoPods)
	require.NotNil(t, selectorFinding)
	require.Contains(t, selectorFinding.Detail, "scaled to zero")
	require.NotNil(t, findingByType(res, ServiceNoReadyEndpoints))
}

func TestDuplicateMissingConfigReferencesCollapsePerContainer(t *testing.T) {
	podRef := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api"}
	configRef := graph.ResourceRef{Kind: "ConfigMap", Namespace: "ns", Name: "api-config"}
	g := graph.New(podRef)
	g.AddNode(podRef, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name: "app",
				EnvFrom: []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: "api-config"},
				}}},
				VolumeMounts: []corev1.VolumeMount{{Name: "config", MountPath: "/etc/api"}},
			}},
			Volumes: []corev1.Volume{{
				Name: "config",
				VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{Sources: []corev1.VolumeProjection{{
					ConfigMap: &corev1.ConfigMapProjection{LocalObjectReference: corev1.LocalObjectReference{Name: "api-config"}},
				}}}},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodPending},
	})
	g.AddNode(configRef, nil)
	g.AddEdge(podRef, configRef, graph.EdgeReferences)

	res := NewEngine().Evaluate(g)
	var missing []*Finding
	for _, finding := range res.Findings {
		if finding.Type == MissingConfigRef {
			missing = append(missing, finding)
		}
	}
	require.Len(t, missing, 1)
	require.Equal(t, "app", missing[0].Subject.Container)
	require.Len(t, missing[0].Evidence, 3, "two usages plus one lookup should remain visible")
}

func serviceWithTargetPort(target intstr.IntOrString, ports []corev1.ContainerPort) *graph.Graph {
	svcRef := graph.ResourceRef{Kind: "Service", Namespace: "ns", Name: "api"}
	podRef := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api-1"}
	sliceRef := graph.ResourceRef{Kind: "EndpointSlice", Namespace: "ns", Name: "api-1"}
	g := graph.New(svcRef)
	g.AddNode(svcRef, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "api"},
			Ports:    []corev1.ServicePort{{Name: "http", Port: 80, TargetPort: target}},
		},
	})
	g.AddNode(podRef, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api-1", Namespace: "ns"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Ports: ports}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{
			{Type: corev1.PodReady, Status: corev1.ConditionTrue},
		}},
	})
	ready := true
	g.AddNode(sliceRef, &discoveryv1.EndpointSlice{
		ObjectMeta:  metav1.ObjectMeta{Name: "api-1", Namespace: "ns"},
		AddressType: discoveryv1.AddressTypeIPv4,
		Ports:       testEndpointPorts(),
		Endpoints: []discoveryv1.Endpoint{{
			Addresses: []string{"10.0.0.1"}, Conditions: discoveryv1.EndpointConditions{Ready: &ready},
		}},
	})
	g.AddEdge(svcRef, podRef, graph.EdgeSelects)
	g.AddEdge(svcRef, sliceRef, graph.EdgeRoutesTo)
	return g
}

func testServicePorts() []corev1.ServicePort {
	return []corev1.ServicePort{{Name: "http", Port: 80, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromInt32(8080)}}
}

func testEndpointPorts() []discoveryv1.EndpointPort {
	name, port, protocol := "http", int32(8080), corev1.ProtocolTCP
	return []discoveryv1.EndpointPort{{Name: &name, Port: &port, Protocol: &protocol}}
}

func findingByType(res *Result, findingType FindingType) *Finding {
	for _, finding := range res.Findings {
		if finding.Type == findingType {
			return finding
		}
	}
	return nil
}
