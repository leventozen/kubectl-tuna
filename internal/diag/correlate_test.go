package diag

import (
	"testing"

	"github.com/stretchr/testify/require"

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
