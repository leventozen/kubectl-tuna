package diag

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/leventozen/kubectl-tuna/internal/graph"
)

// nodePressureRule fires when a Node reports memory, disk, or PID pressure
// through its structured conditions.
type nodePressureRule struct{}

func (nodePressureRule) ID() string { return "node-pressure" }

var pressureConditions = []corev1.NodeConditionType{
	corev1.NodeMemoryPressure,
	corev1.NodeDiskPressure,
	corev1.NodePIDPressure,
}

func (nodePressureRule) Evaluate(g *graph.Graph) []*Finding {
	var findings []*Finding
	for _, n := range g.NodesOfKind("Node") {
		node, ok := n.Object.(*corev1.Node)
		if !ok {
			continue
		}
		for _, want := range pressureConditions {
			for _, cond := range node.Status.Conditions {
				if cond.Type != want || cond.Status != corev1.ConditionTrue {
					continue
				}
				findings = append(findings, &Finding{
					Type:       NodePressure,
					Severity:   SeverityCritical,
					Confidence: ConfidenceHigh,
					Resource:   n.Ref,
					Summary:    fmt.Sprintf("Node is under %s", cond.Type),
					Detail:     "The kubelet may evict pods from this node to reclaim resources; workloads scheduled here are at risk regardless of their own limits.",
					Evidence: []Evidence{
						{Source: fmt.Sprintf("Node condition %s", cond.Type), Value: fmt.Sprintf("%s: %s", cond.Reason, cond.Message)},
					},
					Recommendations: []string{
						fmt.Sprintf("Inspect node resource usage: kubectl describe node %s", node.Name),
						"Check for pods without resource limits consuming node capacity.",
					},
				})
			}
		}
	}
	return findings
}

// podEvictedRule fires when a Pod was terminated by kubelet eviction. This
// is causally distinct from container-oomkilled: eviction is a node-level
// decision driven by node pressure, not the pod's own cgroup limit.
type podEvictedRule struct{}

func (podEvictedRule) ID() string { return "pod-evicted" }

func (podEvictedRule) Evaluate(g *graph.Graph) []*Finding {
	var findings []*Finding
	for _, pn := range podNodes(g) {
		pod := pn.pod
		evictedByStatus := pod.Status.Phase == corev1.PodFailed && pod.Status.Reason == "Evicted"
		evictionEvents := eventsMatching(g, pn.ref, "Evicted")
		if !evictedByStatus && len(evictionEvents) == 0 {
			continue
		}

		f := &Finding{
			Type:       PodEvicted,
			Severity:   SeverityCritical,
			Confidence: ConfidenceHigh,
			Resource:   pn.ref,
			Summary:    "Pod was evicted by the kubelet",
			Detail:     "Eviction is a node-level decision (node pressure), distinct from a container exceeding its own memory limit.",
			Recommendations: []string{
				"Check the node's pressure conditions and overall resource usage.",
				"Set realistic resource requests so the scheduler places pods on nodes with enough headroom; Guaranteed-QoS pods are evicted last.",
			},
		}
		if evictedByStatus {
			f.Evidence = append(f.Evidence, Evidence{
				Source: "Pod.status", Value: fmt.Sprintf("phase=%s reason=%s: %s", pod.Status.Phase, pod.Status.Reason, pod.Status.Message)})
		}
		if len(evictionEvents) > 0 {
			last := evictionEvents[len(evictionEvents)-1]
			f.Evidence = append(f.Evidence, Evidence{Source: "events (reason: Evicted)", Value: last.Message})
		}
		if pod.Status.QOSClass != "" {
			f.Evidence = append(f.Evidence, Evidence{Source: "Pod.status.qosClass", Value: string(pod.Status.QOSClass)})
		}
		findings = append(findings, f)
	}
	return findings
}
