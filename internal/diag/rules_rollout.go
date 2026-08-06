package diag

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"

	"github.com/leventozen/kubectl-tuna/internal/graph"
)

// rolloutStuckRule fires when a Deployment has exceeded its progress
// deadline while a newer ReplicaSet still cannot become available.
type rolloutStuckRule struct{}

func (rolloutStuckRule) ID() string { return "rollout-stuck" }

func (rolloutStuckRule) Evaluate(g *graph.Graph) []*Finding {
	var findings []*Finding
	for _, n := range g.NodesOfKind("Deployment") {
		dep, ok := n.Object.(*appsv1.Deployment)
		if !ok {
			continue
		}
		deadlineExceeded := false
		var progressingMsg string
		for _, cond := range dep.Status.Conditions {
			if cond.Type == appsv1.DeploymentProgressing &&
				cond.Reason == "ProgressDeadlineExceeded" &&
				cond.Status == "False" {
				deadlineExceeded = true
				progressingMsg = cond.Message
				break
			}
		}
		if !deadlineExceeded {
			continue
		}

		desired := int32(1)
		if dep.Spec.Replicas != nil {
			desired = *dep.Spec.Replicas
		}

		newRS, oldRS := rolloutReplicaSets(g, n.Ref)
		f := &Finding{
			Type:       RolloutStuck,
			Severity:   SeverityCritical,
			Confidence: ConfidenceHigh,
			Resource:   n.Ref,
			Summary:    "Deployment rollout exceeded its progress deadline",
			Detail:     "The Deployment controller stopped progressing: the new ReplicaSet never became sufficiently available in time.",
			Evidence: []Evidence{
				{Source: "condition Progressing", Value: fmt.Sprintf("ProgressDeadlineExceeded: %s", progressingMsg)},
				{Source: "Deployment.status", Value: fmt.Sprintf("desired=%d updated=%d ready=%d available=%d",
					desired, dep.Status.UpdatedReplicas, dep.Status.ReadyReplicas, dep.Status.AvailableReplicas)},
			},
			Recommendations: []string{
				"Inspect the new ReplicaSet's Pods for CrashLoopBackOff, ImagePullBackOff, probe failures, or scheduling issues.",
				"Inspect rollout strategy, quota, admission failures, and controller events for constraints not represented by Pod state.",
			},
		}
		if newRS != nil {
			f.Evidence = append(f.Evidence, Evidence{
				Source: fmt.Sprintf("ReplicaSet/%s (newer)", newRS.Name),
				Value:  fmt.Sprintf("replicas=%d ready=%d available=%d", replicasOrZero(newRS.Spec.Replicas), newRS.Status.ReadyReplicas, newRS.Status.AvailableReplicas),
			})
		}
		if oldRS != nil {
			f.Evidence = append(f.Evidence, Evidence{
				Source: fmt.Sprintf("ReplicaSet/%s (older)", oldRS.Name),
				Value:  fmt.Sprintf("replicas=%d ready=%d available=%d", replicasOrZero(oldRS.Spec.Replicas), oldRS.Status.ReadyReplicas, oldRS.Status.AvailableReplicas),
			})
		}
		findings = append(findings, f)
	}
	return findings
}

// rolloutReplicaSets returns the newest and an older ReplicaSet owned by the
// Deployment, based on creation timestamp / revision annotation.
func rolloutReplicaSets(g *graph.Graph, depRef graph.ResourceRef) (newer, older *appsv1.ReplicaSet) {
	var rss []*appsv1.ReplicaSet
	for _, e := range g.EdgesFrom(depRef, graph.EdgeOwns) {
		if e.To.Kind != "ReplicaSet" {
			continue
		}
		n, ok := g.Node(e.To)
		if !ok {
			continue
		}
		if rs, ok := n.Object.(*appsv1.ReplicaSet); ok {
			rss = append(rss, rs)
		}
	}
	if len(rss) == 0 {
		return nil, nil
	}
	// Prefer the RS with the highest deployment.kubernetes.io/revision.
	bestIdx := 0
	for i := 1; i < len(rss); i++ {
		if revisionOf(rss[i]) >= revisionOf(rss[bestIdx]) {
			bestIdx = i
		}
	}
	newer = rss[bestIdx]
	for i, rs := range rss {
		if i == bestIdx {
			continue
		}
		if replicasOrZero(rs.Spec.Replicas) > 0 {
			older = rs
			break
		}
	}
	return newer, older
}

func revisionOf(rs *appsv1.ReplicaSet) int {
	if rs.Annotations == nil {
		return 0
	}
	var rev int
	_, _ = fmt.Sscanf(rs.Annotations["deployment.kubernetes.io/revision"], "%d", &rev)
	return rev
}

func replicasOrZero(r *int32) int32 {
	if r == nil {
		return 0
	}
	return *r
}
