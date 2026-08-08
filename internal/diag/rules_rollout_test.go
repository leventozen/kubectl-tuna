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

func TestCurrentTemplateReplicaSetIgnoresHigherRevisionOnNonMatchingTemplate(t *testing.T) {
	dep, depRef, g := rolloutSelectionGraph(t, "busybox:match")
	addOwnedReplicaSet(g, depRef, replicaSetFixture{
		name: "rs-old-match", uid: "uid-old", created: time.Unix(100, 0),
		revision: "1", image: "busybox:match", hash: "oldhash", replicas: 1,
	})
	addOwnedReplicaSet(g, depRef, replicaSetFixture{
		name: "rs-high-rev-mismatch", uid: "uid-high", created: time.Unix(200, 0),
		revision: "99", image: "busybox:other", hash: "newhash", replicas: 1,
	})

	target := currentTemplateReplicaSet(g, depRef, dep)
	require.NotNil(t, target)
	require.Equal(t, "rs-old-match", target.Name)
}

func TestCurrentTemplateReplicaSetIgnoresMissingAndMalformedRevisions(t *testing.T) {
	dep, depRef, g := rolloutSelectionGraph(t, "busybox:match")
	addOwnedReplicaSet(g, depRef, replicaSetFixture{
		name: "rs-match-no-rev", uid: "uid-match", created: time.Unix(100, 0),
		revision: "", image: "busybox:match", hash: "match", replicas: 1,
	})
	addOwnedReplicaSet(g, depRef, replicaSetFixture{
		name: "rs-mismatch-bad-rev", uid: "uid-bad", created: time.Unix(200, 0),
		revision: "not-a-number", image: "busybox:other", hash: "other", replicas: 1,
	})

	target := currentTemplateReplicaSet(g, depRef, dep)
	require.NotNil(t, target)
	require.Equal(t, "rs-match-no-rev", target.Name)
}

func TestEqualIgnoreHashIgnoresOnlyPodTemplateHash(t *testing.T) {
	left := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
			"app": "api", appsv1.DefaultDeploymentUniqueLabelKey: "aaaa",
		}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "busybox:1"}}},
	}
	right := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
			"app": "api", appsv1.DefaultDeploymentUniqueLabelKey: "bbbb",
		}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "busybox:1"}}},
	}
	require.True(t, equalIgnoreHash(&left, &right))

	right.Spec.Containers[0].Image = "busybox:2"
	require.False(t, equalIgnoreHash(&left, &right))

	right.Spec.Containers[0].Image = "busybox:1"
	right.Spec.Containers[0].Command = []string{"/bin/false"}
	require.False(t, equalIgnoreHash(&left, &right))
}

func TestCurrentTemplateReplicaSetDoesNotMutateTemplateLabels(t *testing.T) {
	dep, depRef, g := rolloutSelectionGraph(t, "busybox:match")
	dep.Spec.Template.Labels[appsv1.DefaultDeploymentUniqueLabelKey] = "dep-hash"
	dep.Spec.Template.Labels["keep"] = "me"
	rs := addOwnedReplicaSet(g, depRef, replicaSetFixture{
		name: "rs-match", uid: "uid-match", created: time.Unix(100, 0),
		revision: "2", image: "busybox:match", hash: "rs-hash", replicas: 1,
	})
	rs.Spec.Template.Labels["keep"] = "me"

	beforeDep := map[string]string{}
	for k, v := range dep.Spec.Template.Labels {
		beforeDep[k] = v
	}
	beforeRS := map[string]string{}
	for k, v := range rs.Spec.Template.Labels {
		beforeRS[k] = v
	}

	require.NotNil(t, currentTemplateReplicaSet(g, depRef, dep))
	require.Equal(t, beforeDep, dep.Spec.Template.Labels)
	require.Equal(t, beforeRS, rs.Spec.Template.Labels)
}

func TestCurrentTemplateReplicaSetOldestMatchingWins(t *testing.T) {
	dep, depRef, g := rolloutSelectionGraph(t, "busybox:match")
	addOwnedReplicaSet(g, depRef, replicaSetFixture{
		name: "rs-newer-match", uid: "uid-new", created: time.Unix(200, 0),
		revision: "3", image: "busybox:match", hash: "new", replicas: 1,
	})
	addOwnedReplicaSet(g, depRef, replicaSetFixture{
		name: "rs-older-match", uid: "uid-old", created: time.Unix(100, 0),
		revision: "2", image: "busybox:match", hash: "old", replicas: 1,
	})

	target := currentTemplateReplicaSet(g, depRef, dep)
	require.NotNil(t, target)
	require.Equal(t, "rs-older-match", target.Name)
}

func TestCurrentTemplateReplicaSetNameTieBreakWhenCreationEqual(t *testing.T) {
	dep, depRef, g := rolloutSelectionGraph(t, "busybox:match")
	created := time.Unix(100, 0)
	addOwnedReplicaSet(g, depRef, replicaSetFixture{
		name: "rs-b", uid: "uid-b", created: created,
		revision: "2", image: "busybox:match", hash: "b", replicas: 1,
	})
	addOwnedReplicaSet(g, depRef, replicaSetFixture{
		name: "rs-a", uid: "uid-a", created: created,
		revision: "9", image: "busybox:match", hash: "a", replicas: 1,
	})

	target := currentTemplateReplicaSet(g, depRef, dep)
	require.NotNil(t, target)
	require.Equal(t, "rs-a", target.Name)
}

func TestCurrentTemplateReplicaSetReturnsNilWhenNoMatch(t *testing.T) {
	dep, depRef, g := rolloutSelectionGraph(t, "busybox:desired")
	addOwnedReplicaSet(g, depRef, replicaSetFixture{
		name: "rs-other", uid: "uid-other", created: time.Unix(100, 0),
		revision: "5", image: "busybox:other", hash: "x", replicas: 1,
	})

	require.Nil(t, currentTemplateReplicaSet(g, depRef, dep))
}

func TestRolloutStuckEvidenceNamesTargetReplicaSet(t *testing.T) {
	dep, depRef, g := rolloutSelectionGraph(t, "busybox:match")
	dep.Status.Conditions = []appsv1.DeploymentCondition{{
		Type: appsv1.DeploymentProgressing, Status: corev1.ConditionFalse,
		Reason: "ProgressDeadlineExceeded", Message: "progress deadline exceeded",
	}}
	addOwnedReplicaSet(g, depRef, replicaSetFixture{
		name: "rs-target", uid: "uid-target", created: time.Unix(200, 0),
		revision: "2", image: "busybox:match", hash: "target", replicas: 1,
	})
	addOwnedReplicaSet(g, depRef, replicaSetFixture{
		name: "rs-old", uid: "uid-old", created: time.Unix(100, 0),
		revision: "1", image: "busybox:old", hash: "old", replicas: 1,
	})

	findings := rolloutStuckRule{}.Evaluate(g)
	require.Len(t, findings, 1)
	sources := make([]string, 0, len(findings[0].Evidence))
	for _, ev := range findings[0].Evidence {
		sources = append(sources, ev.Source)
	}
	require.Contains(t, sources, "ReplicaSet/rs-target (target)")
	require.Contains(t, sources, "ReplicaSet/rs-old (older)")
	require.NotContains(t, sources, "ReplicaSet/rs-target (newer)")
}

type replicaSetFixture struct {
	name     string
	uid      types.UID
	created  time.Time
	revision string
	image    string
	hash     string
	replicas int32
}

func rolloutSelectionGraph(t *testing.T, image string) (*appsv1.Deployment, graph.ResourceRef, *graph.Graph) {
	t.Helper()
	depRef := graph.ResourceRef{Kind: "Deployment", Namespace: "ns", Name: "api"}
	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns", UID: "dep-uid"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api"}},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{
					Name: "app", Image: image,
				}}},
			},
		},
	}
	g := graph.New(depRef)
	g.AddNode(depRef, dep)
	return dep, depRef, g
}

func addOwnedReplicaSet(g *graph.Graph, depRef graph.ResourceRef, fx replicaSetFixture) *appsv1.ReplicaSet {
	rsRef := graph.ResourceRef{Kind: "ReplicaSet", Namespace: depRef.Namespace, Name: fx.name}
	replicas := fx.replicas
	labels := map[string]string{"app": "api"}
	if fx.hash != "" {
		labels[appsv1.DefaultDeploymentUniqueLabelKey] = fx.hash
	}
	annotations := map[string]string{}
	if fx.revision != "" {
		annotations["deployment.kubernetes.io/revision"] = fx.revision
	}
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:              fx.name,
			Namespace:         depRef.Namespace,
			UID:               fx.uid,
			CreationTimestamp: metav1.NewTime(fx.created),
			Annotations:       annotations,
		},
		Spec: appsv1.ReplicaSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{
					Name: "app", Image: fx.image,
				}}},
			},
		},
	}
	g.AddNode(rsRef, rs)
	g.AddEdge(depRef, rsRef, graph.EdgeOwns)
	return rs
}
