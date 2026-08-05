package kube

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	versioninfo "k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/leventozen/kdiag/internal/graph"
)

func TestClusterVersionDiscoveryHonorsContextDeadline(t *testing.T) {
	client := fake.NewClientset()
	client.Discovery().(*fakediscovery.FakeDiscovery).FakedServerVersion = &versioninfo.Info{GitVersion: "v1.36.2"}
	started := make(chan struct{})
	release := make(chan struct{})
	client.PrependReactor("get", "version", func(clienttesting.Action) (bool, runtime.Object, error) {
		close(started)
		<-release
		return true, nil, nil
	})

	g := graph.New(graph.ResourceRef{Kind: "Service", Namespace: "ns", Name: "api"})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	begin := time.Now()
	NewCollector(client).collectClusterInfo(ctx, g)
	require.Less(t, time.Since(begin), time.Second)
	close(release)
	<-started

	require.False(t, g.HasKubernetesVersion())
	require.Len(t, g.CollectionIssues(), 1)
	require.Equal(t, graph.SourceServerVersion, g.CollectionIssues()[0].Source)
	require.Contains(t, g.CollectionIssues()[0].Message, context.DeadlineExceeded.Error())
}
