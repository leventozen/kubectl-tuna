package diag

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/leventozen/kubectl-tuna/internal/graph"
)

// serviceSelectorNoPodsRule reports the directly observable state that a
// Service selector matches no Pods. It deliberately does not guess which
// nearby workload the operator intended the Service to target.
type serviceSelectorNoPodsRule struct{}

func (serviceSelectorNoPodsRule) ID() string { return "service-selector-no-pods" }

func (serviceSelectorNoPodsRule) Evaluate(g *graph.Graph) []*Finding {
	var findings []*Finding
	for _, n := range g.NodesOfKind("Service") {
		svc, ok := n.Object.(*corev1.Service)
		if !ok || len(svc.Spec.Selector) == 0 {
			continue
		}
		// A failed Pod list is unknown evidence, not proof of an empty match.
		if g.HasCollectionIssue(graph.SourcePod, n.Ref) {
			continue
		}
		if len(g.EdgesFrom(n.Ref, graph.EdgeSelects)) > 0 {
			continue
		}

		findings = append(findings, &Finding{
			Type:       ServiceSelectorNoPods,
			Severity:   SeverityCritical,
			Confidence: ConfidenceHigh,
			Resource:   n.Ref,
			Summary:    fmt.Sprintf("Service selector currently matches zero Pods in namespace %q", svc.Namespace),
			Detail:     "The empty match is confirmed, but its cause is not: the selector may be wrong, the intended workload may be absent, or an exactly matching workload may be scaled to zero.",
			Evidence: []Evidence{
				{Source: "Service.spec.selector", Value: formatLabels(svc.Spec.Selector)},
				{Source: "selected Pods", Value: "0"},
			},
			Recommendations: []string{
				"Compare Service.spec.selector with the Pod template labels of the intended workload.",
				"Verify the intended workload exists in the same namespace and is scaled above zero.",
			},
		})
	}
	return findings
}

// serviceTargetPortMismatchRule fires when a Service targetPort does not
// resolve against one or more selected Pods.
type serviceTargetPortMismatchRule struct{}

func (serviceTargetPortMismatchRule) ID() string { return "service-target-port-mismatch" }

func (serviceTargetPortMismatchRule) Evaluate(g *graph.Graph) []*Finding {
	var findings []*Finding
	for _, n := range g.NodesOfKind("Service") {
		svc, ok := n.Object.(*corev1.Service)
		if !ok {
			continue
		}
		var pods []*corev1.Pod
		for _, e := range g.EdgesFrom(n.Ref, graph.EdgeSelects) {
			if pn, ok := g.Node(e.To); ok {
				if pod, ok := pn.Object.(*corev1.Pod); ok {
					pods = append(pods, pod)
				}
			}
		}
		if len(pods) == 0 {
			continue // selector-no-pods rule covers this case
		}

		for _, sp := range svc.Spec.Ports {
			target := sp.TargetPort
			if target.Type == intstr.Int && target.IntVal == 0 && target.StrVal == "" {
				target = intstr.FromInt32(sp.Port)
			}
			matching := podsDeclaringPort(pods, target)
			if matching == len(pods) {
				continue
			}

			f := &Finding{
				Type:     ServiceTargetPortMismatch,
				Severity: SeverityCritical,
				Resource: n.Ref,
				Summary: fmt.Sprintf("Service targetPort %s is not declared by %d/%d selected Pods",
					target.String(), len(pods)-matching, len(pods)),
				Evidence: []Evidence{
					{Source: fmt.Sprintf("Service.spec.ports[%q].targetPort", sp.Name), Value: target.String()},
					{Source: "selected Pods container ports", Value: podPortSummary(pods)},
				},
				Recommendations: []string{
					"Align Service targetPort with the port the container actually listens on.",
					"If using a named port, ensure the container declares a port with that exact name.",
				},
			}
			if target.Type == intstr.String {
				f.Confidence = ConfidenceHigh
				if matching == 0 {
					f.Detail = "Named targetPorts must match a declared container port name; none of the selected Pods declares this name."
				} else {
					f.Severity = SeverityWarning
					f.Detail = "The named targetPort resolves for some selected Pods but not others. Traffic can continue through matching Pods, but the affected Pods cannot supply this Service port."
				}
			} else {
				// Numeric ports can work without a containerPort declaration,
				// so this is strong but not conclusive evidence.
				f.Confidence = ConfidenceMedium
				f.Severity = SeverityWarning
				f.Impact = ImpactRisk
				f.Detail = fmt.Sprintf("%d/%d selected Pods do not declare this numeric port. Traffic may still work if their containers listen on it without declaring it; verify the listening port inside the containers.", len(pods)-matching, len(pods))
			}
			findings = append(findings, f)
		}
	}
	return findings
}

func podsDeclaringPort(pods []*corev1.Pod, target intstr.IntOrString) int {
	matching := 0
	for _, pod := range pods {
		found := false
		for _, c := range pod.Spec.Containers {
			for _, p := range c.Ports {
				if target.Type == intstr.String && p.Name == target.StrVal {
					found = true
				}
				if target.Type == intstr.Int && p.ContainerPort == target.IntVal {
					found = true
				}
			}
		}
		if found {
			matching++
		}
	}
	return matching
}

func podPortSummary(pods []*corev1.Pod) string {
	seen := map[string]bool{}
	var parts []string
	for _, pod := range pods {
		for _, c := range pod.Spec.Containers {
			s := fmt.Sprintf("%s: %s", c.Name, containerPortList(c))
			if !seen[s] {
				seen[s] = true
				parts = append(parts, s)
			}
		}
	}
	return strings.Join(parts, "; ")
}

// serviceNoReadyEndpointsRule reports either no endpoint eligible for regular
// traffic or the narrower risk state where every serving endpoint is also
// terminating. Usually a propagated symptom.
type serviceNoReadyEndpointsRule struct{}

func (serviceNoReadyEndpointsRule) ID() string { return "service-no-ready-endpoints" }

func (serviceNoReadyEndpointsRule) Evaluate(g *graph.Graph) []*Finding {
	var findings []*Finding
	for _, n := range g.NodesOfKind("Service") {
		svc, ok := n.Object.(*corev1.Service)
		if !ok || len(svc.Spec.Selector) == 0 {
			continue
		}
		if g.HasCollectionIssue(graph.SourceEndpointSlices, n.Ref) {
			continue
		}

		selected := len(g.EdgesFrom(n.Ref, graph.EdgeSelects))
		for _, servicePort := range svc.Spec.Ports {
			stats := endpointStatsForServicePort(g, n.Ref, servicePort)
			if stats.ready > 0 {
				continue
			}
			portLabel := formatServicePort(servicePort)
			if stats.servingTerminating > 0 {
				findings = append(findings, &Finding{
					Type:       ServiceTerminatingOnly,
					Severity:   SeverityWarning,
					Confidence: ConfidenceHigh,
					Impact:     ImpactRisk,
					Resource:   n.Ref,
					Summary:    fmt.Sprintf("Service port %s has only serving-but-terminating endpoints", portLabel),
					Detail:     "Service proxies normally ignore terminating endpoints, but may route to endpoints that are both serving and terminating when every available endpoint is terminating. Traffic loss is therefore possible, not proven.",
					Evidence: []Evidence{
						{Source: "EndpointSlices", Value: fmt.Sprintf("%d matching slice(s), %d endpoint(s), %d ready, %d serving+terminating", stats.slices, stats.total, stats.ready, stats.servingTerminating)},
						{Source: "Service port", Value: portLabel},
						{Source: "Pods matching Service selector", Value: fmt.Sprintf("%d", selected)},
					},
					Recommendations: []string{
						"Confirm that replacement Pods become Ready before the terminating endpoints stop serving.",
					},
				})
				continue
			}
			findings = append(findings, &Finding{
				Type:       ServiceNoReadyEndpoints,
				Severity:   SeverityCritical,
				Confidence: ConfidenceHigh,
				Resource:   n.Ref,
				Summary:    fmt.Sprintf("Service port %s has zero endpoints eligible for regular traffic", portLabel),
				Evidence: []Evidence{
					{Source: "EndpointSlices", Value: fmt.Sprintf("%d matching slice(s), %d endpoint(s), %d ready", stats.slices, stats.total, stats.ready)},
					{Source: "Service port", Value: portLabel},
					{Source: "Pods matching Service selector", Value: fmt.Sprintf("%d", selected)},
				},
				Recommendations: []string{
					"Inspect why selected Pods are not Ready, or why the selector matches no Pods.",
				},
			})
		}
	}
	return findings
}

type endpointStats struct {
	slices             int
	total              int
	ready              int
	servingTerminating int
}

func endpointStatsForServicePort(g *graph.Graph, serviceRef graph.ResourceRef, servicePort corev1.ServicePort) endpointStats {
	var stats endpointStats
	for _, edge := range g.EdgesFrom(serviceRef, graph.EdgeRoutesTo) {
		node, ok := g.Node(edge.To)
		if !ok {
			continue
		}
		slice, ok := node.Object.(*discoveryv1.EndpointSlice)
		if !ok || !sliceRoutesServicePort(slice, servicePort) {
			continue
		}
		stats.slices++
		for _, endpoint := range slice.Endpoints {
			if !endpointHasUsableAddress(slice.AddressType, endpoint.Addresses) {
				continue
			}
			stats.total++
			if endpoint.Conditions.Ready == nil || *endpoint.Conditions.Ready {
				stats.ready++
			}
			if endpoint.Conditions.Terminating != nil && *endpoint.Conditions.Terminating &&
				(endpoint.Conditions.Serving == nil || *endpoint.Conditions.Serving) {
				stats.servingTerminating++
			}
		}
	}
	return stats
}

func sliceRoutesServicePort(slice *discoveryv1.EndpointSlice, servicePort corev1.ServicePort) bool {
	wantProtocol := servicePort.Protocol
	if wantProtocol == "" {
		wantProtocol = corev1.ProtocolTCP
	}
	for _, endpointPort := range slice.Ports {
		// EndpointSlices derived from a Service must contain a concrete target
		// port. A nil value in a manually managed slice is not evidence that this
		// Service port is routable.
		if endpointPort.Port == nil {
			continue
		}
		gotProtocol := corev1.ProtocolTCP
		if endpointPort.Protocol != nil {
			gotProtocol = *endpointPort.Protocol
		}
		if gotProtocol != wantProtocol {
			continue
		}
		gotName := ""
		if endpointPort.Name != nil {
			gotName = *endpointPort.Name
		}
		if gotName == servicePort.Name {
			return true
		}
	}
	return false
}

func endpointHasUsableAddress(addressType discoveryv1.AddressType, addresses []string) bool {
	if addressType != discoveryv1.AddressTypeIPv4 && addressType != discoveryv1.AddressTypeIPv6 {
		return false
	}
	return len(addresses) > 0 && addresses[0] != ""
}

func formatServicePort(port corev1.ServicePort) string {
	protocol := port.Protocol
	if protocol == "" {
		protocol = corev1.ProtocolTCP
	}
	if port.Name != "" {
		return fmt.Sprintf("%q (%d/%s)", port.Name, port.Port, protocol)
	}
	return fmt.Sprintf("%d/%s", port.Port, protocol)
}

// readinessProbePortRule fires when a readiness probe targets a port that
// the container does not declare.
type readinessProbePortRule struct{}

func (readinessProbePortRule) ID() string { return "readiness-probe-port-mismatch" }

func (readinessProbePortRule) Evaluate(g *graph.Graph) []*Finding {
	var findings []*Finding
	for _, pn := range podNodes(g) {
		for _, c := range pn.pod.Spec.Containers {
			probe := c.ReadinessProbe
			if probe == nil {
				continue
			}
			port, kind := probePort(probe)
			if kind == "" {
				continue
			}
			if len(c.Ports) == 0 && port.Type == intstr.Int {
				continue // numeric ports can listen without a declaration
			}
			if containerDeclaresPort(c, port) {
				continue
			}

			failures := eventsMatching(g, pn.ref, "Unhealthy")
			f := &Finding{
				Type:     ReadinessProbePortInvalid,
				Severity: SeverityCritical,
				Resource: pn.ref,
				Subject:  containerSubject(c.Name),
				Summary: fmt.Sprintf("Readiness probe of container %q targets port %s, but the container declares %s",
					c.Name, port.String(), containerPortList(c)),
				Evidence: []Evidence{
					{Source: fmt.Sprintf("container[%s].readinessProbe (%s)", c.Name, kind), Value: "port " + port.String()},
					{Source: fmt.Sprintf("container[%s].ports", c.Name), Value: containerPortList(c)},
				},
				Recommendations: []string{
					fmt.Sprintf("Point the readiness probe at a port container %q actually listens on.", c.Name),
					"Verify the listening port inside the container: kubectl exec ... -- ss -lntp",
				},
			}
			if port.Type == intstr.String {
				f.Confidence = ConfidenceHigh
				f.Detail = "The named probe port cannot resolve because this container declares no port with that name."
			} else if failure := matchingProbeFailure(failures, port.IntVal); failure != nil {
				f.Confidence = ConfidenceHigh
				f.Evidence = append(f.Evidence, Evidence{
					Source: "events (reason: Unhealthy)",
					Value:  fmt.Sprintf("matching readiness probe failure (%d occurrence(s)): %s", eventCount(*failure), failure.Message),
				})
			} else {
				f.Confidence = ConfidenceMedium
				f.Impact = ImpactRisk
				f.Detail = "The numeric probe port differs from declared container ports, but containerPort declarations do not prove which ports the process listens on. No matching connection-refused event was observed."
			}
			findings = append(findings, f)
		}
	}
	return findings
}

func matchingProbeFailure(events []corev1.Event, port int32) *corev1.Event {
	wantPort := fmt.Sprintf(":%d", port)
	for i := len(events) - 1; i >= 0; i-- {
		msg := strings.ToLower(events[i].Message)
		if strings.Contains(msg, strings.ToLower(wantPort)) && strings.Contains(msg, "connection refused") {
			return &events[i]
		}
	}
	return nil
}

func probePort(p *corev1.Probe) (intstr.IntOrString, string) {
	switch {
	case p.HTTPGet != nil:
		return p.HTTPGet.Port, "httpGet"
	case p.TCPSocket != nil:
		return p.TCPSocket.Port, "tcpSocket"
	default:
		return intstr.IntOrString{}, ""
	}
}

func containerDeclaresPort(c corev1.Container, port intstr.IntOrString) bool {
	for _, p := range c.Ports {
		if port.Type == intstr.String && p.Name == port.StrVal {
			return true
		}
		if port.Type == intstr.Int && p.ContainerPort == port.IntVal {
			return true
		}
	}
	return false
}

// readinessProbeFailingRule fires when a Pod is NotReady and readiness probe
// failures are observed in events.
type readinessProbeFailingRule struct{}

func (readinessProbeFailingRule) ID() string { return "readiness-probe-failing" }

func (readinessProbeFailingRule) Evaluate(g *graph.Graph) []*Finding {
	var findings []*Finding
	for _, pn := range podNodes(g) {
		if podIsReady(pn.pod) {
			continue
		}
		var probeEvents []corev1.Event
		for _, ev := range eventsMatching(g, pn.ref, "Unhealthy") {
			if strings.Contains(ev.Message, "Readiness probe") {
				probeEvents = append(probeEvents, ev)
			}
		}
		if len(probeEvents) == 0 {
			continue
		}
		var count int32
		for _, ev := range probeEvents {
			count += eventCount(ev)
		}
		last := probeEvents[len(probeEvents)-1]
		var subject *Subject
		if name := soleReadinessProbeContainer(pn.pod); name != "" {
			subject = containerSubject(name)
		}
		findings = append(findings, &Finding{
			Type:       ReadinessProbeFailing,
			Severity:   SeverityWarning,
			Confidence: ConfidenceHigh,
			Resource:   pn.ref,
			Subject:    subject,
			Summary:    "Readiness probe is failing, keeping the Pod NotReady",
			Evidence: []Evidence{
				{Source: "events (reason: Unhealthy)", Value: fmt.Sprintf("readiness probe failed %d time(s)", count)},
				{Source: "last probe failure", Value: last.Message},
			},
		})
	}
	return findings
}

func soleReadinessProbeContainer(pod *corev1.Pod) string {
	name := ""
	for _, c := range pod.Spec.Containers {
		if c.ReadinessProbe == nil {
			continue
		}
		if name != "" {
			return ""
		}
		name = c.Name
	}
	return name
}

// podNotReadyRule fires for every Pod that is not Ready. This is nearly
// always a propagated symptom that a more specific rule explains.
type podNotReadyRule struct{}

func (podNotReadyRule) ID() string { return "pod-not-ready" }

func (podNotReadyRule) Evaluate(g *graph.Graph) []*Finding {
	var findings []*Finding
	for _, pn := range podNodes(g) {
		pod := pn.pod
		if pod.Status.Phase == corev1.PodSucceeded || podIsReady(pod) {
			continue
		}
		readyContainers, totalContainers := 0, len(pod.Spec.Containers)
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Ready {
				readyContainers++
			}
		}
		f := &Finding{
			Type:       PodNotReady,
			Severity:   SeverityWarning,
			Confidence: ConfidenceHigh,
			Resource:   pn.ref,
			Summary:    fmt.Sprintf("Pod is %s and NotReady (%d/%d containers ready)", pod.Status.Phase, readyContainers, totalContainers),
			Detail:     "NotReady Pods are normally excluded from ready Service endpoints. The collected EndpointSlices are the source of truth for whether traffic is currently routed.",
			Evidence: []Evidence{
				{Source: "Pod.status.phase", Value: string(pod.Status.Phase)},
				{Source: "ready containers", Value: fmt.Sprintf("%d/%d", readyContainers, totalContainers)},
			},
		}
		if cond := podCondition(pod, corev1.PodReady); cond != nil && cond.Message != "" {
			f.Evidence = append(f.Evidence, Evidence{Source: "condition Ready", Value: fmt.Sprintf("%s: %s", cond.Reason, cond.Message)})
		}
		findings = append(findings, f)
	}
	return findings
}
