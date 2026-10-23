package diag

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/leventozen/kubectl-tuna/internal/graph"
)

func TestPendingClaimWithoutStorageSchedulingEvidenceRemainsRisk(t *testing.T) {
	pod := storageBlockedPod("api", "0/1 nodes are available: Insufficient cpu.")
	pod.Spec.Volumes = []corev1.Volume{{
		Name: "data",
		VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
			ClaimName: "data",
		}},
	}}
	podRef := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api"}
	claimRef := graph.ResourceRef{Kind: "PersistentVolumeClaim", Namespace: "ns", Name: "data"}
	g := graph.New(podRef)
	g.AddNode(podRef, pod)
	g.AddNode(claimRef, &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "data", Namespace: "ns"},
		Status:     corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending},
	})
	g.AddEdge(podRef, claimRef, graph.EdgeClaimsStorage)

	findings := persistentVolumeClaimRule{}.Evaluate(g)
	require.Len(t, findings, 1)
	require.Equal(t, PersistentVolumeClaimPending, findings[0].Type)
	require.Equal(t, ImpactRisk, findings[0].Impact)
	require.Equal(t, ConfidenceMedium, findings[0].Confidence)
	require.Contains(t, findings[0].Detail, "WaitForFirstConsumer")
}

func TestPendingClaimWithExactStorageSchedulingEvidenceIsCurrent(t *testing.T) {
	g := storageClaimContractGraph(&corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "data", Namespace: "ns"},
		Status:     corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending},
	})

	findings := persistentVolumeClaimRule{}.Evaluate(g)
	require.Len(t, findings, 1)
	require.Equal(t, ImpactCurrent, findings[0].Impact)
	require.Equal(t, ConfidenceHigh, findings[0].Confidence)
	require.Equal(t, SeverityCritical, findings[0].Severity)
}
