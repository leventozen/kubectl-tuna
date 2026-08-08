package diag

import (
	"fmt"
	"sort"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"

	"github.com/leventozen/kubectl-tuna/internal/graph"
)

// rolloutStuckRule fires when a Deployment has exceeded its progress
// deadline while the current-template ReplicaSet still cannot become available.
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

		targetRS, olderRS := rolloutReplicaSets(g, n.Ref, dep)
		f := &Finding{
			Type:       RolloutStuck,
			Severity:   SeverityCritical,
			Confidence: ConfidenceHigh,
			Resource:   n.Ref,
			Summary:    "Deployment rollout exceeded its progress deadline",
			Detail:     "The Deployment controller stopped progressing: the ReplicaSet matching the current Pod template never became sufficiently available in time.",
			Evidence: []Evidence{
				{Source: "condition Progressing", Value: fmt.Sprintf("ProgressDeadlineExceeded: %s", progressingMsg)},
				{Source: "Deployment.status", Value: fmt.Sprintf("desired=%d updated=%d ready=%d available=%d",
					desired, dep.Status.UpdatedReplicas, dep.Status.ReadyReplicas, dep.Status.AvailableReplicas)},
			},
			Recommendations: []string{
				"Inspect the current-template ReplicaSet's Pods for CrashLoopBackOff, ImagePullBackOff, probe failures, or scheduling issues.",
				"Inspect rollout strategy, quota, admission failures, and controller events for constraints not represented by Pod state.",
			},
		}
		if targetRS != nil {
			f.Evidence = append(f.Evidence, Evidence{
				Source: fmt.Sprintf("ReplicaSet/%s (target)", targetRS.Name),
				Value:  fmt.Sprintf("replicas=%d ready=%d available=%d", replicasOrZero(targetRS.Spec.Replicas), targetRS.Status.ReadyReplicas, targetRS.Status.AvailableReplicas),
			})
		}
		if olderRS != nil {
			f.Evidence = append(f.Evidence, Evidence{
				Source: fmt.Sprintf("ReplicaSet/%s (older)", olderRS.Name),
				Value:  fmt.Sprintf("replicas=%d ready=%d available=%d", replicasOrZero(olderRS.Spec.Replicas), olderRS.Status.ReadyReplicas, olderRS.Status.AvailableReplicas),
			})
		}
		findings = append(findings, f)
	}
	return findings
}

// rolloutReplicaSets returns the ReplicaSet matching the Deployment's current
// Pod template and one deterministic active older ReplicaSet for context.
func rolloutReplicaSets(g *graph.Graph, depRef graph.ResourceRef, dep *appsv1.Deployment) (target, older *appsv1.ReplicaSet) {
	target = currentTemplateReplicaSet(g, depRef, dep)
	var activeOlder []*appsv1.ReplicaSet
	for _, rs := range ownedReplicaSets(g, depRef) {
		if target != nil && rs.UID == target.UID {
			continue
		}
		if replicasOrZero(rs.Spec.Replicas) > 0 {
			activeOlder = append(activeOlder, rs)
		}
	}
	sortReplicaSetsByCreationTimestamp(activeOlder)
	if len(activeOlder) > 0 {
		// Prefer the most recently created active older RS as context; this is
		// one older RS, not a claim about the full old-RS set.
		older = activeOlder[len(activeOlder)-1]
	}
	return target, older
}

// currentTemplateReplicaSet returns the owned ReplicaSet whose Pod template
// equals Deployment.spec.template under equalIgnoreHash. Candidates are
// ordered oldest-first by creation timestamp, then name; the oldest match
// wins, matching upstream FindNewReplicaSet. Returns nil when none match.
func currentTemplateReplicaSet(g *graph.Graph, depRef graph.ResourceRef, dep *appsv1.Deployment) *appsv1.ReplicaSet {
	if dep == nil {
		return nil
	}
	rss := ownedReplicaSets(g, depRef)
	sortReplicaSetsByCreationTimestamp(rss)
	for _, rs := range rss {
		if equalIgnoreHash(&rs.Spec.Template, &dep.Spec.Template) {
			return rs
		}
	}
	return nil
}

func ownedReplicaSets(g *graph.Graph, depRef graph.ResourceRef) []*appsv1.ReplicaSet {
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
	return rss
}

// equalIgnoreHash deep-copies both templates, deletes only the controller-
// managed pod-template-hash label from each copy, and compares with Kubernetes
// semantic equality. Graph objects are never mutated.
func equalIgnoreHash(template1, template2 *corev1.PodTemplateSpec) bool {
	if template1 == nil || template2 == nil {
		return template1 == template2
	}
	t1Copy := template1.DeepCopy()
	t2Copy := template2.DeepCopy()
	delete(t1Copy.Labels, appsv1.DefaultDeploymentUniqueLabelKey)
	delete(t2Copy.Labels, appsv1.DefaultDeploymentUniqueLabelKey)
	return apiequality.Semantic.DeepEqual(t1Copy, t2Copy)
}

func sortReplicaSetsByCreationTimestamp(rss []*appsv1.ReplicaSet) {
	sort.SliceStable(rss, func(i, j int) bool {
		if rss[i].CreationTimestamp.Equal(&rss[j].CreationTimestamp) {
			return rss[i].Name < rss[j].Name
		}
		return rss[i].CreationTimestamp.Before(&rss[j].CreationTimestamp)
	})
}

func replicasOrZero(r *int32) int32 {
	if r == nil {
		return 0
	}
	return *r
}
