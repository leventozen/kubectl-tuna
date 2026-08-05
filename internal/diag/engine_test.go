package diag_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	versioninfo "k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"

	"github.com/leventozen/kdiag/internal/diag"
	"github.com/leventozen/kdiag/internal/kube"
	"github.com/leventozen/kdiag/internal/report"
)

// evaluateScenario loads every YAML manifest under testdata/<scenario> into
// a fake clientset, runs the collector for the focus resource, and evaluates
// the diagnostic engine — the same path a live inspection takes.
func evaluateScenario(t *testing.T, scenario, focusKind, namespace, name string) *diag.Result {
	t.Helper()

	dir := filepath.Join("..", "..", "testdata", scenario)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	decoder := scheme.Codecs.UniversalDeserializer()
	var objs []runtime.Object
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		require.NoError(t, err)
		for _, doc := range strings.Split(string(data), "\n---") {
			if strings.TrimSpace(doc) == "" {
				continue
			}
			obj, _, err := decoder.Decode([]byte(doc), nil, nil)
			require.NoError(t, err, "decoding %s", e.Name())
			objs = append(objs, obj)
		}
	}

	client := fake.NewClientset(objs...)
	client.Discovery().(*fakediscovery.FakeDiscovery).FakedServerVersion = &versioninfo.Info{GitVersion: "v1.36.2"}
	collector := kube.NewCollector(client)
	ctx := context.Background()

	switch focusKind {
	case "service":
		graph, err := collector.CollectService(ctx, namespace, name)
		require.NoError(t, err)
		return diag.NewEngine().Evaluate(graph)
	case "deployment":
		graph, err := collector.CollectDeployment(ctx, namespace, name)
		require.NoError(t, err)
		return diag.NewEngine().Evaluate(graph)
	case "pod":
		graph, err := collector.CollectPod(ctx, namespace, name)
		require.NoError(t, err)
		return diag.NewEngine().Evaluate(graph)
	default:
		t.Fatalf("unsupported focus kind %q", focusKind)
		return nil
	}
}

func findingOfType(res *diag.Result, ft diag.FindingType) *diag.Finding {
	for _, f := range res.Findings {
		if f.Type == ft {
			return f
		}
	}
	return nil
}

func chainTypes(root *diag.Finding) []diag.FindingType {
	var out []diag.FindingType
	for _, f := range diag.Chain(root) {
		out = append(out, f.Type)
	}
	return out
}

func TestBrokenReadinessPort(t *testing.T) {
	res := evaluateScenario(t, "broken-readiness-port", "service", "finance", "payment")
	require.Equal(t, diag.HealthDegraded, res.Health)

	require.Len(t, res.RootCauses, 1, "expected exactly one root cause")
	root := res.RootCauses[0]
	require.Equal(t, diag.ReadinessProbePortInvalid, root.Type)
	require.Equal(t, diag.ConfidenceHigh, root.Confidence, "probe failure events should raise confidence")

	require.Equal(t, []diag.FindingType{
		diag.ReadinessProbePortInvalid,
		diag.ReadinessProbeFailing,
		diag.PodNotReady,
		diag.ServiceNoReadyEndpoints,
	}, chainTypes(root))

	// The service-level symptom must be explained, not reported standalone.
	symptom := findingOfType(res, diag.ServiceNoReadyEndpoints)
	require.NotNil(t, symptom)
	require.True(t, symptom.IsSymptom())

	// deployment-unavailable is also explained by pod-not-ready.
	dep := findingOfType(res, diag.DeploymentUnavailable)
	require.NotNil(t, dep)
	require.True(t, dep.IsSymptom())
}

func TestServiceSelectorWithNoPodsDoesNotGuessIntendedWorkload(t *testing.T) {
	res := evaluateScenario(t, "service-selector-mismatch", "service", "production", "checkout-api")
	require.Equal(t, diag.HealthDegraded, res.Health)

	require.Len(t, res.RootCauses, 1)
	root := res.RootCauses[0]
	require.Equal(t, diag.ServiceSelectorNoPods, root.Type)

	// The empty selector match is factual. The intended Deployment is not.
	var evidence []string
	for _, ev := range root.Evidence {
		evidence = append(evidence, ev.Source+"="+ev.Value)
	}
	require.Contains(t, strings.Join(evidence, "\n"), "app=checkout")
	require.NotContains(t, strings.Join(evidence, "\n"), "Deployment/")
	require.Contains(t, root.Detail, "cause is not")

	require.Equal(t, []diag.FindingType{
		diag.ServiceSelectorNoPods,
		diag.ServiceNoReadyEndpoints,
	}, chainTypes(root))
}

func TestOOMKilled(t *testing.T) {
	res := evaluateScenario(t, "oomkilled", "deployment", "finance", "billing")
	require.Equal(t, diag.HealthDegraded, res.Health)

	require.Len(t, res.RootCauses, 1)
	root := res.RootCauses[0]
	require.Equal(t, diag.ContainerOOMKilled, root.Type)
	require.Equal(t, diag.ConfidenceHigh, root.Confidence, "no eviction events: cgroup limit kill is deterministic")

	require.Equal(t, []diag.FindingType{
		diag.ContainerOOMKilled,
		diag.CrashLoopBackOff,
		diag.PodNotReady,
		diag.RolloutStuck,
		diag.DeploymentUnavailable,
	}, chainTypes(root))

	crash := findingOfType(res, diag.CrashLoopBackOff)
	require.NotNil(t, crash)
	require.True(t, crash.IsSymptom(), "the crash loop is a symptom of the OOM kill, not a root cause")
}

func TestFailedScheduling(t *testing.T) {
	res := evaluateScenario(t, "failed-scheduling", "deployment", "data", "analytics")
	require.Equal(t, diag.HealthDegraded, res.Health)

	require.Len(t, res.RootCauses, 1)
	root := res.RootCauses[0]
	require.Equal(t, diag.PodUnschedulable, root.Type)
	require.Contains(t, root.Detail, "allocatable resources", "message should be classified as a resource shortage")

	require.Equal(t, []diag.FindingType{
		diag.PodUnschedulable,
		diag.PodNotReady,
		diag.DeploymentUnavailable,
	}, chainTypes(root))
}

func TestMissingConfigMap(t *testing.T) {
	res := evaluateScenario(t, "missing-configmap", "deployment", "messaging", "notifier")
	require.Equal(t, diag.HealthDegraded, res.Health)

	require.Len(t, res.RootCauses, 1)
	root := res.RootCauses[0]
	require.Equal(t, diag.MissingConfigRef, root.Type)
	require.Contains(t, root.Summary, "notifier-config")

	require.Equal(t, []diag.FindingType{
		diag.MissingConfigRef,
		diag.PodNotReady,
		diag.DeploymentUnavailable,
	}, chainTypes(root))
}

// TestAmbiguousSIGKILL covers reason=Error/exitCode=137. Even with a memory
// limit, that is not proof of OOMKilled and must retain a distinct identity.
func TestAmbiguousSIGKILL(t *testing.T) {
	res := evaluateScenario(t, "oomkilled-implicit", "deployment", "jobs", "worker")
	require.Equal(t, diag.HealthDegraded, res.Health)

	require.Len(t, res.RootCauses, 1)
	root := res.RootCauses[0]
	require.Equal(t, diag.ContainerSIGKILL, root.Type)
	require.Equal(t, diag.ConfidenceMedium, root.Confidence, "Error/137 must not be labeled as a confirmed OOM kill")

	require.Equal(t, []diag.FindingType{
		diag.ContainerSIGKILL,
		diag.CrashLoopBackOff,
		diag.PodNotReady,
		diag.DeploymentUnavailable,
	}, chainTypes(root))
	// The fixture has no ProgressDeadlineExceeded, so rollout-stuck
	// does not join the chain (unlike the explicit OOMKilled fixture).
}

func TestEvictedPod(t *testing.T) {
	res := evaluateScenario(t, "evicted-pod", "pod", "prod", "cache-7f9c2-b4xk1")
	require.Equal(t, diag.HealthDegraded, res.Health)

	require.Len(t, res.RootCauses, 1)
	root := res.RootCauses[0]
	require.Equal(t, diag.NodePressure, root.Type)
	require.Equal(t, "Node", root.Resource.Kind, "the root cause lives on the Node, not the Pod")

	require.Equal(t, []diag.FindingType{
		diag.NodePressure,
		diag.PodEvicted,
		diag.PodNotReady,
	}, chainTypes(root))

	evicted := findingOfType(res, diag.PodEvicted)
	require.NotNil(t, evicted)
	require.True(t, evicted.IsSymptom(), "the eviction is a symptom of node pressure")
}

// TestPodFocusBrokenReadinessPort verifies the pod entry point discovers the
// Service selecting the Pod, so a pod-focused inspection still reaches the
// service-level symptom.
func TestPodFocusBrokenReadinessPort(t *testing.T) {
	res := evaluateScenario(t, "broken-readiness-port", "pod", "finance", "payment-7b889d-x8p2")
	require.Equal(t, diag.HealthDegraded, res.Health)

	require.Len(t, res.RootCauses, 1)
	root := res.RootCauses[0]
	require.Equal(t, diag.ReadinessProbePortInvalid, root.Type)

	require.Equal(t, []diag.FindingType{
		diag.ReadinessProbePortInvalid,
		diag.ReadinessProbeFailing,
		diag.PodNotReady,
		diag.ServiceNoReadyEndpoints,
	}, chainTypes(root))
}

func TestHealthyService(t *testing.T) {
	res := evaluateScenario(t, "healthy-service", "service", "default", "web")
	require.Equal(t, diag.HealthOK, res.Health)
	require.Empty(t, res.Findings)
}

func TestJSONOutputIsDeterministic(t *testing.T) {
	var first string
	for i := 0; i < 10; i++ {
		res := evaluateScenario(t, "broken-readiness-port", "service", "finance", "payment")
		var out bytes.Buffer
		require.NoError(t, report.RenderJSON(&out, res))
		if i == 0 {
			first = out.String()
			continue
		}
		require.Equal(t, first, out.String())
	}
}
