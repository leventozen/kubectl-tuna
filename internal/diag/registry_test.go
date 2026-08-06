package diag

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/leventozen/kubectl-tuna/internal/graph"
)

type countingRule struct {
	id    string
	calls *int
}

type emittingRule struct {
	id          string
	findingType FindingType
}

func (r emittingRule) ID() string { return r.id }

func (r emittingRule) Evaluate(g *graph.Graph) []*Finding {
	return []*Finding{{
		Type: r.findingType, Severity: SeverityWarning, Confidence: ConfidenceHigh,
		Impact: ImpactCurrent, Resource: g.Focus, Summary: "test finding",
	}}
}

func (r countingRule) ID() string { return r.id }

func (r countingRule) Evaluate(*graph.Graph) []*Finding {
	*r.calls++
	return nil
}

func registration(rule Rule, min, max string) RuleRegistration {
	return RuleRegistration{
		Rule: rule,
		Metadata: RuleMetadata{
			ID:            rule.ID(),
			Family:        RuleFamilyTraffic,
			Description:   "test rule",
			FindingTypes:  []FindingType{FindingType(rule.ID())},
			Compatibility: KubernetesCompatibility{Min: min, Max: max},
		},
	}
}

func TestRegistryRejectsDuplicateFindingTypeOwners(t *testing.T) {
	firstCalls, secondCalls := 0, 0
	first := registration(countingRule{id: "first", calls: &firstCalls}, "1.34", "1.36")
	second := registration(countingRule{id: "second", calls: &secondCalls}, "1.34", "1.36")
	second.Metadata.FindingTypes = append([]FindingType(nil), first.Metadata.FindingTypes...)

	_, err := NewRegistry([]RuleRegistration{first, second})
	require.EqualError(t, err, `finding type "first" is declared by both "first" and "second"`)
}

func TestRegistryRejectsDuplicateRuleIDs(t *testing.T) {
	calls := 0
	rule := countingRule{id: "duplicate", calls: &calls}
	_, err := NewRegistry([]RuleRegistration{
		registration(rule, "1.34", "1.36"),
		registration(rule, "1.34", "1.36"),
	})
	require.EqualError(t, err, `duplicate rule ID "duplicate"`)
}

func TestRegistryRejectsEmptyRuleSet(t *testing.T) {
	_, err := NewRegistry(nil)
	require.EqualError(t, err, "rule registry is empty")
}

func TestDefaultRegistryDeclaresEveryBuiltInFindingType(t *testing.T) {
	registry, err := NewRegistry(DefaultRuleRegistrations())
	require.NoError(t, err)

	var actual []string
	for _, metadata := range registry.Metadata() {
		for _, findingType := range metadata.FindingTypes {
			actual = append(actual, string(findingType))
		}
	}
	sort.Strings(actual)
	expected := []string{
		string(ContainerOOMKilled), string(ContainerSIGKILL), string(CrashLoopBackOff),
		string(DeploymentUnavailable), string(ImagePullFailure), string(MissingConfigRef),
		string(NodePressure), string(PodEvicted), string(PodNotReady), string(PodUnschedulable),
		string(ReadinessProbeFailing), string(ReadinessProbePortInvalid), string(RolloutStuck),
		string(ServiceNoReadyEndpoints), string(ServiceSelectorNoPods),
		string(ServiceTargetPortMismatch), string(ServiceTerminatingOnly),
	}
	sort.Strings(expected)
	require.Equal(t, expected, actual)
}

func TestRegistryRejectsInvalidCompatibilityRange(t *testing.T) {
	calls := 0
	_, err := NewRegistry([]RuleRegistration{
		registration(countingRule{id: "bad-range", calls: &calls}, "1.36", "1.34"),
	})
	require.ErrorContains(t, err, "maximum Kubernetes version 1.34 is lower than minimum 1.36")
}

func TestEngineEvaluatesRuleInsideReviewedRange(t *testing.T) {
	calls := 0
	registry, err := NewRegistry([]RuleRegistration{
		registration(countingRule{id: "compatible", calls: &calls}, "1.34", "1.36"),
	})
	require.NoError(t, err)

	g := graph.New(graph.ResourceRef{Kind: "Service", Namespace: "ns", Name: "api"})
	require.NoError(t, g.SetKubernetesVersion("v1.36.2+k3s1"))
	result := NewEngineWithRegistry(registry).Evaluate(g)

	require.Equal(t, 1, calls)
	require.Equal(t, 1, result.Rules.Evaluated)
	require.Empty(t, result.Rules.Skipped)
	require.Equal(t, HealthOK, result.Health)
}

func TestEngineSkipsRuleOutsideReviewedRange(t *testing.T) {
	calls := 0
	registry, err := NewRegistry([]RuleRegistration{
		registration(countingRule{id: "not-reviewed", calls: &calls}, "1.34", "1.36"),
	})
	require.NoError(t, err)

	g := graph.New(graph.ResourceRef{Kind: "Service", Namespace: "ns", Name: "api"})
	require.NoError(t, g.SetKubernetesVersion("v1.37.0-alpha.1"))
	result := NewEngineWithRegistry(registry).Evaluate(g)

	require.Zero(t, calls)
	require.Zero(t, result.Rules.Evaluated)
	require.Len(t, result.Rules.Skipped, 1)
	require.Equal(t, "not-reviewed", result.Rules.Skipped[0].ID)
	require.Equal(t, HealthUnknown, result.Health)
	require.True(t, result.Partial)
	require.Equal(t, graph.SourceRuleCompatibility, result.Warnings[0].Source)
}

func TestSyntheticGraphWithoutVersionStillEvaluatesRules(t *testing.T) {
	calls := 0
	registry, err := NewRegistry([]RuleRegistration{
		registration(countingRule{id: "synthetic", calls: &calls}, "1.34", "1.36"),
	})
	require.NoError(t, err)

	g := graph.New(graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api"})
	result := NewEngineWithRegistry(registry).Evaluate(g)

	require.Equal(t, 1, calls)
	require.Equal(t, HealthOK, result.Health)
	require.False(t, result.Partial)
}

func TestTemporalIntegrityIssueSuspendsEveryRule(t *testing.T) {
	calls := 0
	registry, err := NewRegistry([]RuleRegistration{
		registration(countingRule{id: "must-not-run", calls: &calls}, "1.34", "1.36"),
	})
	require.NoError(t, err)

	focus := graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api"}
	related := graph.ResourceRef{Kind: "Deployment", Namespace: "ns", Name: "api"}
	g := graph.New(focus)
	require.NoError(t, g.SetKubernetesVersion("v1.36.2"))
	g.AddCollectionIssue(graph.CollectionIssue{
		Source: graph.SourceTemporalIntegrity, Resource: related,
		Message: "focus changed", AffectsHealth: true,
	})
	result := NewEngineWithRegistry(registry).Evaluate(g)

	require.Zero(t, calls)
	require.Zero(t, result.Rules.Evaluated)
	require.NotEmpty(t, result.Rules.SuspendedReason)
	require.Empty(t, result.Findings)
	require.Equal(t, HealthUnknown, result.Health)
}

func TestEngineDiscardsUndeclaredFindingType(t *testing.T) {
	rule := emittingRule{id: "declared-rule", findingType: "unexpected-type"}
	registered := registration(rule, "1.34", "1.36")
	registered.Metadata.FindingTypes = []FindingType{"declared-type"}
	registry, err := NewRegistry([]RuleRegistration{registered})
	require.NoError(t, err)

	g := graph.New(graph.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api"})
	result := NewEngineWithRegistry(registry).Evaluate(g)

	require.Empty(t, result.Findings)
	require.Equal(t, HealthUnknown, result.Health)
	require.True(t, result.Partial)
	require.Equal(t, graph.SourceRuleExecution, result.Warnings[0].Source)
}
