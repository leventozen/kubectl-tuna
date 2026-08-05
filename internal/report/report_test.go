package report

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/leventozen/kdiag/internal/diag"
	"github.com/leventozen/kdiag/internal/graph"
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
		Warnings: []graph.CollectionIssue{{Source: graph.SourceEvents, Resource: ref, Message: "forbidden"}},
		Rules:    diag.RuleEvaluationSummary{Evaluated: 15},
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
