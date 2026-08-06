package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/leventozen/kubectl-tuna/internal/diag"
	"github.com/leventozen/kubectl-tuna/internal/graph"
)

func TestJSONIncludesImpactSubjectAndPartialEvidence(t *testing.T) {
	ref := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api"}
	finding := &diag.Finding{
		ID: "f-1", RuleID: "container-oomkilled", Type: diag.ContainerOOMKilled, Severity: diag.SeverityWarning,
		Confidence: diag.ConfidenceMedium, Impact: diag.ImpactHistorical,
		Resource: ref, Subject: &diag.Subject{Container: "app"}, Summary: "recovered",
	}
	res := &diag.Result{
		Focus: ref,
		Cluster: graph.ClusterInfo{
			KubernetesVersion: "v1.36.2", Major: 1, Minor: 36,
		},
		Health: diag.HealthOK, Findings: []*diag.Finding{finding}, Standalone: []*diag.Finding{finding}, Partial: true,
		ReplicaOwners: map[string]graph.ResourceRef{"f-1": {Kind: "Deployment", Namespace: "ns", Name: "api"}},
		Warnings:      []graph.CollectionIssue{{Source: graph.SourceEvents, Resource: ref, Message: "forbidden"}},
		Rules:         diag.RuleEvaluationSummary{Evaluated: 15},
	}
	var out bytes.Buffer
	require.NoError(t, RenderJSON(&out, res))
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &decoded))
	require.Equal(t, true, decoded["partial"])
	require.Len(t, decoded["warnings"], 1)
	require.Equal(t, "v1.36.2", decoded["cluster"].(map[string]any)["kubernetesVersion"])
	require.Equal(t, float64(15), decoded["rules"].(map[string]any)["evaluated"])
	findings := decoded["findings"].([]any)
	got := findings[0].(map[string]any)
	require.Equal(t, "historical", got["impact"])
	require.Equal(t, "container-oomkilled", got["ruleId"])
	require.Equal(t, "app", got["subject"].(map[string]any)["container"])
	require.NotContains(t, decoded, "replicaOwners", "presentation grouping must not change the JSON contract")
}

func TestConsoleShowsKubernetesVersionAndSkippedRule(t *testing.T) {
	ref := graph.ResourceRef{Kind: "Service", Namespace: "ns", Name: "api"}
	res := &diag.Result{
		Focus: ref,
		Cluster: graph.ClusterInfo{
			KubernetesVersion: "v1.37.0", Major: 1, Minor: 37,
		},
		Health:  diag.HealthUnknown,
		Partial: true,
		Warnings: []graph.CollectionIssue{{
			Source: graph.SourceRuleCompatibility, Resource: ref, Message: "one rule skipped", AffectsHealth: true,
		}},
		Rules: diag.RuleEvaluationSummary{Skipped: []diag.RuleSkip{{
			ID: "example-rule", Reason: "cluster v1.37.0 is outside the reviewed range 1.34-1.36",
			Compatibility: diag.KubernetesCompatibility{Min: "1.34", Max: "1.36"},
		}}},
	}

	var out bytes.Buffer
	(&ConsoleReporter{Out: &out}).Render(res)
	require.Contains(t, out.String(), "Kubernetes: v1.37.0")
	require.Contains(t, out.String(), "Skipped rules")
	require.Contains(t, out.String(), "example-rule")
}

func TestConsoleHealthyResultStillRendersRiskFinding(t *testing.T) {
	ref := graph.ResourceRef{Kind: "Service", Namespace: "ns", Name: "api"}
	finding := &diag.Finding{
		ID: "f-1", Type: diag.ServiceTargetPortMismatch, Severity: diag.SeverityWarning,
		Confidence: diag.ConfidenceMedium, Impact: diag.ImpactRisk,
		Resource: ref, Summary: "numeric port is undeclared",
	}
	res := &diag.Result{
		Focus: ref, Health: diag.HealthOK, Findings: []*diag.Finding{finding}, Standalone: []*diag.Finding{finding},
	}
	var out bytes.Buffer
	(&ConsoleReporter{Out: &out}).Render(res)
	require.Contains(t, out.String(), "Health:    HEALTHY")
	require.Contains(t, out.String(), "impact: risk")
	require.NotContains(t, out.String(), "No findings")
}

func TestConsoleUnknownExplainsUnavailableEvidence(t *testing.T) {
	ref := graph.ResourceRef{Kind: "Service", Namespace: "ns", Name: "api"}
	res := &diag.Result{
		Focus: ref, Health: diag.HealthUnknown, Partial: true,
		Warnings: []graph.CollectionIssue{{Source: graph.SourceEndpointSlices, Resource: ref, Message: "forbidden", AffectsHealth: true}},
	}
	var out bytes.Buffer
	(&ConsoleReporter{Out: &out}).Render(res)
	require.Contains(t, out.String(), "Health:    UNKNOWN")
	require.Contains(t, out.String(), "Incomplete evidence")
	require.Contains(t, out.String(), "Health could not be established")
}

func TestJSONIncludesCollectionBoundsAndFocusStability(t *testing.T) {
	ref := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api"}
	started := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	endRevision := graph.FocusRevision{ResourceVersion: "11", Generation: 2}
	res := &diag.Result{
		Focus: ref,
		Collection: &graph.CollectionInfo{
			StartedAt: started, CompletedAt: started.Add(time.Second),
			FocusStart: graph.FocusRevision{ResourceVersion: "10", Generation: 1},
			FocusEnd:   &endRevision, FocusStability: graph.FocusStabilityChanged,
		},
		Health: diag.HealthUnknown,
	}

	var out bytes.Buffer
	require.NoError(t, RenderJSON(&out, res))
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &decoded))
	collection := decoded["collection"].(map[string]any)
	require.Equal(t, "changed", collection["focusStability"])
	require.Equal(t, "10", collection["focusStart"].(map[string]any)["resourceVersion"])
	require.Equal(t, "11", collection["focusEnd"].(map[string]any)["resourceVersion"])
}

func TestConsoleExplainsSuspendedRuleEvaluation(t *testing.T) {
	ref := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api"}
	res := &diag.Result{
		Focus: ref, Health: diag.HealthUnknown, Partial: true,
		Warnings: []graph.CollectionIssue{{
			Source: graph.SourceTemporalIntegrity, Resource: ref,
			Message: "focus changed", AffectsHealth: true,
		}},
		Rules: diag.RuleEvaluationSummary{
			SuspendedReason: "resource stability or controller freshness was not established for the collected graph",
		},
	}

	var out bytes.Buffer
	(&ConsoleReporter{Out: &out}).Render(res)
	require.Contains(t, out.String(), "Rule evaluation suspended")
	require.Contains(t, out.String(), "temporal integrity not established")
}

func TestConsoleGroupsEquivalentOwnedReplicasWithoutDroppingEvidence(t *testing.T) {
	dep := graph.ResourceRef{Kind: "Deployment", Namespace: "ns", Name: "api"}
	podA := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api-a"}
	podB := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api-b"}
	notReadyA := &diag.Finding{
		ID: "f-3", RuleID: "pod-not-ready", Type: diag.PodNotReady,
		Severity: diag.SeverityWarning, Confidence: diag.ConfidenceHigh, Impact: diag.ImpactCurrent,
		Resource: podA, Summary: "Pod A is NotReady",
	}
	notReadyB := &diag.Finding{
		ID: "f-4", RuleID: "pod-not-ready", Type: diag.PodNotReady,
		Severity: diag.SeverityWarning, Confidence: diag.ConfidenceHigh, Impact: diag.ImpactCurrent,
		Resource: podB, Summary: "Pod B is NotReady",
	}
	rootA := &diag.Finding{
		ID: "f-1", RuleID: "crashloop-backoff", Type: diag.CrashLoopBackOff,
		Severity: diag.SeverityCritical, Confidence: diag.ConfidenceHigh, Impact: diag.ImpactCurrent,
		Resource: podA, Subject: &diag.Subject{Container: "app"}, Summary: "Container restarted 3 times",
		Evidence:        []diag.Evidence{{Source: "restartCount", Value: "3"}},
		Recommendations: []string{"Inspect previous logs."}, Causes: []*diag.Finding{notReadyA},
	}
	rootB := &diag.Finding{
		ID: "f-2", RuleID: "crashloop-backoff", Type: diag.CrashLoopBackOff,
		Severity: diag.SeverityCritical, Confidence: diag.ConfidenceHigh, Impact: diag.ImpactCurrent,
		Resource: podB, Subject: &diag.Subject{Container: "app"}, Summary: "Container restarted 5 times",
		Evidence:        []diag.Evidence{{Source: "restartCount", Value: "5"}},
		Recommendations: []string{"Inspect previous logs."}, Causes: []*diag.Finding{notReadyB},
	}
	notReadyA.CausedBy = []*diag.Finding{rootA}
	notReadyB.CausedBy = []*diag.Finding{rootB}
	res := &diag.Result{
		Focus: dep, Health: diag.HealthDegraded,
		Findings:   []*diag.Finding{rootA, rootB, notReadyA, notReadyB},
		RootCauses: []*diag.Finding{rootA, rootB},
		ReplicaOwners: map[string]graph.ResourceRef{
			"f-1": dep, "f-2": dep, "f-3": dep, "f-4": dep,
		},
	}

	var out bytes.Buffer
	(&ConsoleReporter{Out: &out}).Render(res)
	rendered := out.String()
	require.Contains(t, rendered, "crashloop-backoff × 2 replicas")
	require.Contains(t, rendered, "owner Deployment/api (ns)")
	require.Contains(t, rendered, "Pod/api-a (ns)")
	require.Contains(t, rendered, "Pod/api-b (ns)")
	require.Contains(t, rendered, "restartCount: 3")
	require.Contains(t, rendered, "restartCount: 5")
	require.Contains(t, rendered, "Causal chains by replica")
	require.Contains(t, rendered, "pod-not-ready × 2 replicas")
	require.Equal(t, 1, strings.Count(rendered, "Inspect previous logs."), "shared recommendation should render once")
}

func TestReplicaGroupingKeepsDifferentOwnersAndContainersSeparate(t *testing.T) {
	depA := graph.ResourceRef{Kind: "Deployment", Namespace: "ns", Name: "api"}
	depB := graph.ResourceRef{Kind: "Deployment", Namespace: "ns", Name: "worker"}
	base := func(id, pod, container string) *diag.Finding {
		return &diag.Finding{
			ID: id, RuleID: "crashloop-backoff", Type: diag.CrashLoopBackOff,
			Severity: diag.SeverityCritical, Confidence: diag.ConfidenceHigh, Impact: diag.ImpactCurrent,
			Resource: graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: pod},
			Subject:  &diag.Subject{Container: container},
		}
	}
	findings := []*diag.Finding{
		base("f-1", "api-a", "app"),
		base("f-2", "api-b", "sidecar"),
		base("f-3", "worker-a", "app"),
	}
	groups := groupReplicaFindings(findings, map[string]graph.ResourceRef{
		"f-1": depA, "f-2": depA, "f-3": depB,
	})
	require.Len(t, groups, 3, "ownership and container identity are hard grouping boundaries")
}
