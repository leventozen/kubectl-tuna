package graph

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSetKubernetesVersionAcceptsVendorSuffix(t *testing.T) {
	g := New(ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api"})
	require.NoError(t, g.SetKubernetesVersion("v1.36.2+k3s1"))
	require.True(t, g.HasKubernetesVersion())
	require.Equal(t, uint(1), g.Cluster.Major)
	require.Equal(t, uint(36), g.Cluster.Minor)
	require.Equal(t, "v1.36.2+k3s1", g.Cluster.KubernetesVersion)
}

func TestSetKubernetesVersionRejectsPlaceholderVersion(t *testing.T) {
	g := New(ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api"})
	require.Error(t, g.SetKubernetesVersion("v0.0.0-master+$Format:%H$"))
	require.False(t, g.HasKubernetesVersion())
}

func TestEventsForSortsChronologically(t *testing.T) {
	ref := ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api"}
	g := New(ref)
	later := metav1.NewTime(time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC))
	earlier := metav1.NewTime(later.Add(-time.Minute))
	g.AddEvents(
		corev1.Event{ObjectMeta: metav1.ObjectMeta{Name: "later", Namespace: "ns"}, InvolvedObject: corev1.ObjectReference{Kind: "Pod", Namespace: "ns", Name: "api"}, LastTimestamp: later},
		corev1.Event{ObjectMeta: metav1.ObjectMeta{Name: "earlier", Namespace: "ns"}, InvolvedObject: corev1.ObjectReference{Kind: "Pod", Namespace: "ns", Name: "api"}, LastTimestamp: earlier},
	)

	events := g.EventsFor(ref)
	require.Equal(t, []string{"earlier", "later"}, []string{events[0].Name, events[1].Name})
}

func TestEventsForUsesSeriesLastObservedTime(t *testing.T) {
	ref := ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api"}
	g := New(ref)
	base := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	firstObserved := metav1.NewMicroTime(base)
	singleObserved := metav1.NewMicroTime(base.Add(time.Hour))
	seriesObserved := metav1.NewMicroTime(base.Add(2 * time.Hour))
	g.AddEvents(
		corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: "series-latest", Namespace: "ns"},
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Namespace: "ns", Name: "api"},
			EventTime:      firstObserved,
			Series:         &corev1.EventSeries{LastObservedTime: seriesObserved},
		},
		corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: "single", Namespace: "ns"},
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Namespace: "ns", Name: "api"},
			EventTime:      singleObserved,
		},
	)

	events := g.EventsFor(ref)
	require.Equal(t, []string{"single", "series-latest"}, []string{events[0].Name, events[1].Name})
}

func TestUnknownNodeIsNotMissing(t *testing.T) {
	ref := ResourceRef{Kind: "Secret", Namespace: "ns", Name: "credentials"}
	g := New(ref)
	node := g.AddUnknownNode(ref)
	require.True(t, node.ExistenceUnknown())
	require.False(t, node.Missing())

	g.AddNode(ref, nil)
	require.True(t, node.Missing(), "a definitive not-found result must upgrade unknown to missing")
}
