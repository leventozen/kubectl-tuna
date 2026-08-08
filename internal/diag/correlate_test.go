package diag

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/leventozen/kubectl-tuna/internal/graph"
)

func TestCorrelationRequiresExactScheduledNode(t *testing.T) {
	g := graph.New(graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "pod-b"})
	nodeA := graph.ResourceRef{Kind: "Node", Name: "node-a"}
	nodeB := graph.ResourceRef{Kind: "Node", Name: "node-b"}
	podA := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "pod-a"}
	podB := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "pod-b"}
	service := graph.ResourceRef{Kind: "Service", Namespace: "ns", Name: "shared"}
	g.AddEdge(service, podA, graph.EdgeSelects)
	g.AddEdge(service, podB, graph.EdgeSelects)
	g.AddEdge(podA, nodeA, graph.EdgeScheduledOn)
	g.AddEdge(podB, nodeB, graph.EdgeScheduledOn)

	pressure := &Finding{Type: NodePressure, Resource: nodeA}
	evicted := &Finding{Type: PodEvicted, Resource: podB}
	Correlate([]*Finding{pressure, evicted}, g)

	require.Empty(t, pressure.Causes, "a shared Service must not bridge pressure on node-a to a Pod on node-b")
	require.Empty(t, evicted.CausedBy)
}

func TestCorrelationRequiresOwningDeployment(t *testing.T) {
	g := graph.New(graph.ResourceRef{Kind: "Deployment", Namespace: "ns", Name: "dep-b"})
	depA := graph.ResourceRef{Kind: "Deployment", Namespace: "ns", Name: "dep-a"}
	depB := graph.ResourceRef{Kind: "Deployment", Namespace: "ns", Name: "dep-b"}
	rsA := graph.ResourceRef{Kind: "ReplicaSet", Namespace: "ns", Name: "rs-a"}
	rsB := graph.ResourceRef{Kind: "ReplicaSet", Namespace: "ns", Name: "rs-b"}
	podA := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "pod-a"}
	podB := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "pod-b"}
	service := graph.ResourceRef{Kind: "Service", Namespace: "ns", Name: "shared"}
	g.AddEdge(depA, rsA, graph.EdgeOwns)
	g.AddEdge(rsA, podA, graph.EdgeOwns)
	g.AddEdge(depB, rsB, graph.EdgeOwns)
	g.AddEdge(rsB, podB, graph.EdgeOwns)
	g.AddEdge(service, podA, graph.EdgeSelects)
	g.AddEdge(service, podB, graph.EdgeSelects)

	notReady := &Finding{Type: PodNotReady, Resource: podA}
	unavailable := &Finding{Type: DeploymentUnavailable, Resource: depB}
	Correlate([]*Finding{notReady, unavailable}, g)

	require.Empty(t, notReady.Causes, "a shared Service must not bridge one workload's Pod to another Deployment")
	require.Empty(t, unavailable.CausedBy)
}

func TestCorrelationRequiresSameContainer(t *testing.T) {
	pod := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "multi"}
	g := graph.New(pod)
	oom := &Finding{Type: ContainerOOMKilled, Resource: pod, Subject: &Subject{Container: "sidecar"}}
	crash := &Finding{Type: CrashLoopBackOff, Resource: pod, Subject: &Subject{Container: "app"}}
	Correlate([]*Finding{oom, crash}, g)

	require.Empty(t, oom.Causes)
	require.Empty(t, crash.CausedBy)

	crash.Subject.Container = "sidecar"
	Correlate([]*Finding{oom, crash}, g)
	require.Equal(t, []*Finding{crash}, oom.Causes)
}

func TestCurrentOOMTerminationExplainsPodNotReadyWithoutCrashLoopSnapshot(t *testing.T) {
	podRef := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api"}
	g := graph.New(podRef)
	g.AddNode(podRef, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
			Name:  "app",
			Ready: false,
			State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
				Reason: "OOMKilled", ExitCode: 137,
			}},
		}}},
	})
	oom := &Finding{Type: ContainerOOMKilled, Resource: podRef, Subject: &Subject{Container: "app"}}
	notReady := &Finding{Type: PodNotReady, Resource: podRef}

	Correlate([]*Finding{oom, notReady}, g)

	require.Equal(t, []*Finding{notReady}, oom.Causes)
	require.Equal(t, []*Finding{oom}, notReady.CausedBy)
}

func TestHistoricalOOMTerminationDoesNotExplainCurrentPodNotReady(t *testing.T) {
	podRef := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api"}
	g := graph.New(podRef)
	g.AddNode(podRef, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
			Name:  "app",
			Ready: false,
			State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
				Reason: "OOMKilled", ExitCode: 137,
			}},
		}}},
	})
	oom := &Finding{Type: ContainerOOMKilled, Resource: podRef, Subject: &Subject{Container: "app"}}
	notReady := &Finding{Type: PodNotReady, Resource: podRef}

	Correlate([]*Finding{oom, notReady}, g)

	require.Empty(t, oom.Causes)
	require.Empty(t, notReady.CausedBy)
}

func TestReplicaOwnersRequireExactDeploymentReplicaSetPodPath(t *testing.T) {
	depA := graph.ResourceRef{Kind: "Deployment", Namespace: "ns", Name: "api"}
	depB := graph.ResourceRef{Kind: "Deployment", Namespace: "ns", Name: "worker"}
	rsA := graph.ResourceRef{Kind: "ReplicaSet", Namespace: "ns", Name: "api-rs"}
	rsB := graph.ResourceRef{Kind: "ReplicaSet", Namespace: "ns", Name: "worker-rs"}
	podA := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api-1"}
	podB := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "worker-1"}
	standalonePod := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "manual"}
	g := graph.New(depA)
	for _, ref := range []graph.ResourceRef{depA, depB, rsA, rsB, podA, podB, standalonePod} {
		g.AddNode(ref, struct{}{})
	}
	g.AddEdge(depA, rsA, graph.EdgeOwns)
	g.AddEdge(rsA, podA, graph.EdgeOwns)
	g.AddEdge(depB, rsB, graph.EdgeOwns)
	g.AddEdge(rsB, podB, graph.EdgeOwns)

	findings := []*Finding{
		{ID: "f-1", Resource: podA},
		{ID: "f-2", Resource: podB},
		{ID: "f-3", Resource: standalonePod},
		{ID: "f-4", Resource: depA},
	}
	owners := replicaOwners(findings, g)
	require.Equal(t, depA, owners["f-1"])
	require.Equal(t, depB, owners["f-2"])
	require.NotContains(t, owners, "f-3")
	require.NotContains(t, owners, "f-4")
}

func TestRolloutStuckRequiresTargetReplicaSetPod(t *testing.T) {
	g, depRef, targetPod, oldPod := multiReplicaSetRolloutGraph(t)

	targetNotReady := &Finding{Type: PodNotReady, Resource: targetPod}
	oldNotReady := &Finding{Type: PodNotReady, Resource: oldPod}
	rollout := &Finding{Type: RolloutStuck, Resource: depRef}
	unavailable := &Finding{Type: DeploymentUnavailable, Resource: depRef}

	Correlate([]*Finding{targetNotReady, oldNotReady, rollout, unavailable}, g)

	require.ElementsMatch(t, []*Finding{rollout, unavailable}, targetNotReady.Causes)
	require.Equal(t, []*Finding{unavailable}, oldNotReady.Causes)
	require.NotContains(t, oldNotReady.Causes, rollout)
	require.Equal(t, []*Finding{targetNotReady}, rollout.CausedBy)
	require.ElementsMatch(t, []*Finding{targetNotReady, oldNotReady, rollout}, unavailable.CausedBy)
}

func TestRolloutStuckFailsClosedWithoutMatchingTargetReplicaSet(t *testing.T) {
	depRef := graph.ResourceRef{Kind: "Deployment", Namespace: "ns", Name: "api"}
	rsRef := graph.ResourceRef{Kind: "ReplicaSet", Namespace: "ns", Name: "rs-old"}
	podRef := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "pod-old"}
	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns", UID: "dep"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "busybox:desired"}}},
			},
		},
	}
	g := graph.New(depRef)
	g.AddNode(depRef, dep)
	g.AddNode(rsRef, &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: "rs-old", Namespace: "ns", UID: "rs-old"},
		Spec: appsv1.ReplicaSetSpec{
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "busybox:old"}}},
			},
		},
	})
	g.AddNode(podRef, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-old", Namespace: "ns"}})
	g.AddEdge(depRef, rsRef, graph.EdgeOwns)
	g.AddEdge(rsRef, podRef, graph.EdgeOwns)

	notReady := &Finding{Type: PodNotReady, Resource: podRef}
	rollout := &Finding{Type: RolloutStuck, Resource: depRef}
	unavailable := &Finding{Type: DeploymentUnavailable, Resource: depRef}
	Correlate([]*Finding{notReady, rollout, unavailable}, g)

	require.Equal(t, []*Finding{unavailable}, notReady.Causes)
	require.Empty(t, rollout.CausedBy)
}

func TestRolloutStuckRejectsCrossDeploymentOwnership(t *testing.T) {
	g, depRef, targetPod, _ := multiReplicaSetRolloutGraph(t)
	otherDep := graph.ResourceRef{Kind: "Deployment", Namespace: "ns", Name: "other"}
	g.AddNode(otherDep, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "ns"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "other"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "busybox:match"}}},
			},
		},
	})

	notReady := &Finding{Type: PodNotReady, Resource: targetPod}
	rollout := &Finding{Type: RolloutStuck, Resource: otherDep}
	Correlate([]*Finding{notReady, rollout}, g)

	require.Empty(t, notReady.Causes)
	require.Empty(t, rollout.CausedBy)
	_ = depRef
}

func TestRolloutStuckRejectsMissingTargetOwnsEdge(t *testing.T) {
	g, depRef, targetPod, _ := multiReplicaSetRolloutGraph(t)
	orphan := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "orphan"}
	g.AddNode(orphan, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "orphan", Namespace: "ns"}})

	notReady := &Finding{Type: PodNotReady, Resource: orphan}
	rollout := &Finding{Type: RolloutStuck, Resource: depRef}
	Correlate([]*Finding{notReady, rollout}, g)

	require.Empty(t, notReady.Causes)
	require.Empty(t, rollout.CausedBy)
	_ = targetPod
}

func multiReplicaSetRolloutGraph(t *testing.T) (*graph.Graph, graph.ResourceRef, graph.ResourceRef, graph.ResourceRef) {
	t.Helper()
	depRef := graph.ResourceRef{Kind: "Deployment", Namespace: "ns", Name: "api"}
	targetRS := graph.ResourceRef{Kind: "ReplicaSet", Namespace: "ns", Name: "rs-target"}
	oldRS := graph.ResourceRef{Kind: "ReplicaSet", Namespace: "ns", Name: "rs-old"}
	targetPod := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "pod-target"}
	oldPod := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "pod-old"}
	replicas := int32(1)

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns", UID: "dep"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "busybox:match"}}},
			},
		},
	}
	g := graph.New(depRef)
	g.AddNode(depRef, dep)
	g.AddNode(targetRS, &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "rs-target", Namespace: "ns", UID: types.UID("uid-target"),
			CreationTimestamp: metav1.NewTime(time.Unix(200, 0)),
			Annotations:       map[string]string{"deployment.kubernetes.io/revision": "2"},
		},
		Spec: appsv1.ReplicaSetSpec{
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
					"app": "api", appsv1.DefaultDeploymentUniqueLabelKey: "target",
				}},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "busybox:match"}}},
			},
		},
	})
	g.AddNode(oldRS, &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "rs-old", Namespace: "ns", UID: types.UID("uid-old"),
			CreationTimestamp: metav1.NewTime(time.Unix(100, 0)),
			Annotations:       map[string]string{"deployment.kubernetes.io/revision": "1"},
		},
		Spec: appsv1.ReplicaSetSpec{
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
					"app": "api", appsv1.DefaultDeploymentUniqueLabelKey: "old",
				}},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "busybox:old"}}},
			},
		},
	})
	g.AddNode(targetPod, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-target", Namespace: "ns"}})
	g.AddNode(oldPod, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-old", Namespace: "ns"}})
	g.AddEdge(depRef, targetRS, graph.EdgeOwns)
	g.AddEdge(depRef, oldRS, graph.EdgeOwns)
	g.AddEdge(targetRS, targetPod, graph.EdgeOwns)
	g.AddEdge(oldRS, oldPod, graph.EdgeOwns)
	return g, depRef, targetPod, oldPod
}
