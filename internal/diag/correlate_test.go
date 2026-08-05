package diag

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/leventozen/kdiag/internal/graph"
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
