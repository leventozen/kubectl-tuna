package diag

import (
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/leventozen/kubectl-tuna/internal/graph"
)

// persistentVolumeClaimRule reports claims used by collected Pods that are
// definitively missing or not yet bound. A Pending claim is a risk by default:
// delayed binding can be intentional. It becomes a current, high-confidence
// blocker only when one exact Pod has one unresolved claim and the scheduler's
// structured condition identifies storage as the scheduling constraint.
type persistentVolumeClaimRule struct{}

func (persistentVolumeClaimRule) ID() string { return "persistent-volume-claim" }

func (persistentVolumeClaimRule) Evaluate(g *graph.Graph) []*Finding {
	var findings []*Finding
	for _, node := range g.NodesOfKind("PersistentVolumeClaim") {
		usages := claimUsageEvidence(g, node.Ref)
		if len(usages) == 0 {
			continue
		}

		if node.Missing() {
			findings = append(findings, &Finding{
				Type:       PersistentVolumeClaimMissing,
				Severity:   SeverityCritical,
				Confidence: ConfidenceHigh,
				Resource:   node.Ref,
				Summary:    fmt.Sprintf("PersistentVolumeClaim %q is referenced by a Pod but does not exist", node.Ref.Name),
				Detail:     "Kubernetes cannot resolve the Pod volume until the claim exists in the same namespace.",
				Evidence: append(usages, Evidence{
					Source: "lookup", Value: fmt.Sprintf("PersistentVolumeClaim %q not found in namespace %q", node.Ref.Name, node.Ref.Namespace),
				}),
				Recommendations: []string{
					fmt.Sprintf("Create PersistentVolumeClaim %q in namespace %q, or correct the Pod volume claimName.", node.Ref.Name, node.Ref.Namespace),
				},
			})
			continue
		}

		claim, ok := node.Object.(*corev1.PersistentVolumeClaim)
		if !ok || claim == nil || claim.Status.Phase != corev1.ClaimPending {
			continue
		}

		blocking := claimBlocksAnyPod(g, node.Ref, PersistentVolumeClaimPending)
		finding := &Finding{
			Type:       PersistentVolumeClaimPending,
			Severity:   SeverityWarning,
			Confidence: ConfidenceMedium,
			Impact:     ImpactRisk,
			Resource:   node.Ref,
			Summary:    fmt.Sprintf("PersistentVolumeClaim %q is Pending and not yet bound", node.Ref.Name),
			Detail:     "Pending alone is not proof of a fault: a WaitForFirstConsumer StorageClass can intentionally delay binding until scheduling. Tuna only treats this claim as the scheduling root when the exact Pod has one unresolved claim and its scheduler condition names a storage constraint.",
			Evidence: append(usages,
				Evidence{Source: "PersistentVolumeClaim.status.phase", Value: string(claim.Status.Phase)},
				Evidence{Source: "PersistentVolumeClaim.spec", Value: claimRequestSummary(claim)},
			),
			Recommendations: []string{
				fmt.Sprintf("Inspect claim and provisioning events: kubectl describe pvc %s -n %s", node.Ref.Name, node.Ref.Namespace),
				"Verify the StorageClass, provisioner, requested capacity, access modes, and topology constraints.",
			},
		}
		if blocking {
			finding.Severity = SeverityCritical
			finding.Confidence = ConfidenceHigh
			finding.Impact = ImpactCurrent
			finding.Detail = "The claim is unbound and the exact Pod's scheduler condition identifies storage as the scheduling constraint. The claim is the only unresolved claim used by that Pod."
		}
		findings = append(findings, finding)
	}
	return findings
}

func claimUsageEvidence(g *graph.Graph, claimRef graph.ResourceRef) []Evidence {
	var evidence []Evidence
	for _, pn := range podNodes(g) {
		if !g.HasEdge(pn.ref, claimRef, graph.EdgeClaimsStorage) {
			continue
		}
		for _, volume := range pn.pod.Spec.Volumes {
			if volume.PersistentVolumeClaim == nil || volume.PersistentVolumeClaim.ClaimName != claimRef.Name {
				continue
			}
			evidence = append(evidence, Evidence{
				Source: fmt.Sprintf("Pod/%s.spec.volumes[%s].persistentVolumeClaim.claimName", pn.ref.Name, volume.Name),
				Value:  claimRef.Name,
			})
		}
	}
	sort.Slice(evidence, func(i, j int) bool {
		if evidence[i].Source != evidence[j].Source {
			return evidence[i].Source < evidence[j].Source
		}
		return evidence[i].Value < evidence[j].Value
	})
	return evidence
}

func claimRequestSummary(claim *corev1.PersistentVolumeClaim) string {
	storageClass := "<default>"
	if claim.Spec.StorageClassName != nil {
		storageClass = *claim.Spec.StorageClassName
		if storageClass == "" {
			storageClass = "<none>"
		}
	}
	requested := "<unspecified>"
	if quantity, ok := claim.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
		requested = quantity.String()
	}
	modes := make([]string, 0, len(claim.Spec.AccessModes))
	for _, mode := range claim.Spec.AccessModes {
		modes = append(modes, string(mode))
	}
	if len(modes) == 0 {
		modes = append(modes, "<unspecified>")
	}
	return fmt.Sprintf("storageClass=%s requested=%s accessModes=%s", storageClass, requested, strings.Join(modes, ","))
}

// storageClaimBlocksPod is the exact causal predicate for
// claim -> Pod scheduling. Generic graph proximity is insufficient: the Pod
// must claim this PVC, this must be its only unresolved claim, and the
// scheduler condition must identify the corresponding storage constraint.
func storageClaimBlocksPod(cause, effect *Finding, g *graph.Graph) bool {
	if effect.Type != PodUnschedulable || !g.HasEdge(effect.Resource, cause.Resource, graph.EdgeClaimsStorage) {
		return false
	}
	unresolved := unresolvedClaimsForPod(g, effect.Resource)
	if len(unresolved) != 1 || unresolved[0] != cause.Resource {
		return false
	}
	return podSchedulingNamesStorageConstraint(g, effect.Resource, cause.Resource, cause.Type)
}

func claimBlocksAnyPod(g *graph.Graph, claimRef graph.ResourceRef, findingType FindingType) bool {
	for _, pn := range podNodes(g) {
		candidate := &Finding{Type: findingType, Resource: claimRef}
		unschedulable := &Finding{Type: PodUnschedulable, Resource: pn.ref}
		if storageClaimBlocksPod(candidate, unschedulable, g) {
			return true
		}
	}
	return false
}

func unresolvedClaimsForPod(g *graph.Graph, podRef graph.ResourceRef) []graph.ResourceRef {
	var refs []graph.ResourceRef
	for _, edge := range g.EdgesFrom(podRef, graph.EdgeClaimsStorage) {
		node, ok := g.Node(edge.To)
		if !ok {
			continue
		}
		if node.ExistenceUnknown() {
			refs = append(refs, edge.To)
			continue
		}
		if node.Missing() {
			refs = append(refs, edge.To)
			continue
		}
		claim, ok := node.Object.(*corev1.PersistentVolumeClaim)
		if ok && claim != nil && claim.Status.Phase == corev1.ClaimPending {
			refs = append(refs, edge.To)
		}
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].String() < refs[j].String() })
	return refs
}

func podSchedulingNamesStorageConstraint(g *graph.Graph, podRef, claimRef graph.ResourceRef, findingType FindingType) bool {
	node, ok := g.Node(podRef)
	if !ok {
		return false
	}
	pod, ok := node.Object.(*corev1.Pod)
	if !ok || pod.Status.Phase != corev1.PodPending {
		return false
	}
	condition := podCondition(pod, corev1.PodScheduled)
	if condition == nil || condition.Status != corev1.ConditionFalse || condition.Reason != corev1.PodReasonUnschedulable {
		return false
	}
	message := strings.ToLower(condition.Message)
	switch findingType {
	case PersistentVolumeClaimMissing:
		return strings.Contains(message, strings.ToLower(claimRef.Name)) &&
			strings.Contains(message, "persistentvolumeclaim") &&
			(strings.Contains(message, "not found") || strings.Contains(message, "does not exist"))
	case PersistentVolumeClaimPending:
		return strings.Contains(message, "unbound immediate persistentvolumeclaim") ||
			(strings.Contains(message, strings.ToLower(claimRef.Name)) &&
				strings.Contains(message, "persistentvolumeclaim") && strings.Contains(message, "unbound"))
	default:
		return false
	}
}
