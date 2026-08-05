package diag

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/leventozen/kdiag/internal/graph"
)

// podUnschedulableRule fires when the scheduler reports it cannot place a
// Pod. The trigger is the structured PodScheduled condition; the scheduler
// message is used only as secondary evidence to classify the cause.
type podUnschedulableRule struct{}

func (podUnschedulableRule) ID() string { return "pod-unschedulable" }

func (podUnschedulableRule) Evaluate(g *graph.Graph) []*Finding {
	var findings []*Finding
	for _, pn := range podNodes(g) {
		pod := pn.pod
		if pod.Status.Phase != corev1.PodPending {
			continue
		}
		cond := podCondition(pod, corev1.PodScheduled)
		if cond == nil || cond.Status != corev1.ConditionFalse || cond.Reason != corev1.PodReasonUnschedulable {
			continue
		}

		f := &Finding{
			Type:       PodUnschedulable,
			Severity:   SeverityCritical,
			Confidence: ConfidenceHigh,
			Resource:   pn.ref,
			Summary:    "The scheduler cannot place this Pod on any node",
			Evidence: []Evidence{
				{Source: "condition PodScheduled", Value: fmt.Sprintf("%s: %s", cond.Reason, cond.Message)},
			},
		}

		// Classify from the scheduler message (secondary evidence only).
		msg := cond.Message
		switch {
		case strings.Contains(msg, "Insufficient cpu") || strings.Contains(msg, "Insufficient memory"):
			f.Detail = "No node has enough allocatable resources for this Pod's requests."
			f.Evidence = append(f.Evidence, Evidence{Source: "Pod resource requests", Value: requestSummary(pod)})
			f.Recommendations = []string{
				"Lower the Pod's resource requests if they are larger than the workload needs.",
				"Add capacity (more or larger nodes), or enable the cluster autoscaler.",
			}
		case strings.Contains(msg, "untolerated taint"):
			f.Detail = "All candidate nodes carry taints this Pod does not tolerate."
			f.Recommendations = []string{
				"Add a matching toleration to the Pod, or remove the taint from a suitable node.",
				"Inspect node taints: kubectl get nodes -o custom-columns=NAME:.metadata.name,TAINTS:.spec.taints",
			}
		case strings.Contains(msg, "node affinity") || strings.Contains(msg, "node selector"):
			f.Detail = "The Pod's nodeSelector/affinity rules match no available node."
			f.Recommendations = []string{
				"Compare the Pod's nodeSelector and affinity with node labels: kubectl get nodes --show-labels",
			}
		default:
			f.Detail = "See the scheduler message in the evidence for the specific constraint."
		}

		if evs := eventsMatching(g, pn.ref, "FailedScheduling"); len(evs) > 0 {
			var count int32
			for _, ev := range evs {
				count += eventCount(ev)
			}
			f.Evidence = append(f.Evidence, Evidence{
				Source: "events (reason: FailedScheduling)",
				Value:  fmt.Sprintf("%d occurrence(s); last: %s", count, evs[len(evs)-1].Message),
			})
		}
		findings = append(findings, f)
	}
	return findings
}

func requestSummary(pod *corev1.Pod) string {
	var parts []string
	for _, c := range pod.Spec.Containers {
		req := c.Resources.Requests
		if len(req) == 0 {
			parts = append(parts, fmt.Sprintf("%s: <none>", c.Name))
			continue
		}
		cpu, mem := "-", "-"
		if v, ok := req[corev1.ResourceCPU]; ok {
			cpu = v.String()
		}
		if v, ok := req[corev1.ResourceMemory]; ok {
			mem = v.String()
		}
		parts = append(parts, fmt.Sprintf("%s: cpu=%s memory=%s", c.Name, cpu, mem))
	}
	return strings.Join(parts, "; ")
}
