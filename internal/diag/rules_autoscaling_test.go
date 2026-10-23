package diag

import (
	"testing"

	"github.com/stretchr/testify/require"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"

	"github.com/leventozen/kubectl-tuna/internal/graph"
)

func TestHPAMetricFailureIsRiskNotWorkloadOutage(t *testing.T) {
	g := hpaContractGraph(autoscalingv2.HorizontalPodAutoscalerCondition{
		Type: autoscalingv2.ScalingActive, Status: corev1.ConditionFalse,
		Reason: "FailedGetExternalMetric", Message: "external metrics API unavailable",
	}, 2, 5)

	findings := horizontalPodAutoscalerRule{}.Evaluate(g)
	require.Len(t, findings, 1)
	require.Equal(t, HPAMetricsUnavailable, findings[0].Type)
	require.Equal(t, ImpactRisk, findings[0].Impact)
	require.Equal(t, ConfidenceHigh, findings[0].Confidence)
	require.Contains(t, findings[0].Detail, "does not prove")
}

func TestHPARefusesNonMetricAndLowerBoundConditions(t *testing.T) {
	tests := []struct {
		name      string
		condition autoscalingv2.HorizontalPodAutoscalerCondition
		desired   int32
		max       int32
	}{
		{
			name: "manual zero replicas is not a metric failure",
			condition: autoscalingv2.HorizontalPodAutoscalerCondition{
				Type: autoscalingv2.ScalingActive, Status: corev1.ConditionFalse, Reason: "ScalingDisabled",
			},
			desired: 0, max: 5,
		},
		{
			name: "invalid selector is not mislabeled as unavailable metrics",
			condition: autoscalingv2.HorizontalPodAutoscalerCondition{
				Type: autoscalingv2.ScalingActive, Status: corev1.ConditionFalse, Reason: "InvalidSelector",
			},
			desired: 2, max: 5,
		},
		{
			name: "lower bound is not a scale up capacity limit",
			condition: autoscalingv2.HorizontalPodAutoscalerCondition{
				Type: autoscalingv2.ScalingLimited, Status: corev1.ConditionTrue, Reason: "TooFewReplicas",
			},
			desired: 1, max: 5,
		},
		{
			name: "inconsistent desired count fails closed",
			condition: autoscalingv2.HorizontalPodAutoscalerCondition{
				Type: autoscalingv2.ScalingLimited, Status: corev1.ConditionTrue, Reason: "TooManyReplicas",
			},
			desired: 4, max: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Empty(t, horizontalPodAutoscalerRule{}.Evaluate(hpaContractGraph(tt.condition, tt.desired, tt.max)))
		})
	}
}

func TestHPARequiresExactGraphTarget(t *testing.T) {
	g := hpaContractGraph(autoscalingv2.HorizontalPodAutoscalerCondition{
		Type: autoscalingv2.ScalingActive, Status: corev1.ConditionFalse,
		Reason: "FailedGetResourceMetric",
	}, 2, 5)
	hpaRef := graph.ResourceRef{Kind: "HorizontalPodAutoscaler", Namespace: "ns", Name: "api"}
	unrelated := graph.New(g.Focus)
	node, ok := g.Node(hpaRef)
	require.True(t, ok)
	unrelated.AddNode(hpaRef, node.Object)

	require.Empty(t, horizontalPodAutoscalerRule{}.Evaluate(unrelated))
}

func TestHPAStaleControllerStatusFailsClosed(t *testing.T) {
	g := hpaContractGraph(autoscalingv2.HorizontalPodAutoscalerCondition{
		Type: autoscalingv2.ScalingActive, Status: corev1.ConditionFalse,
		Reason: "FailedGetResourceMetric",
	}, 2, 5)
	hpaRef := graph.ResourceRef{Kind: "HorizontalPodAutoscaler", Namespace: "ns", Name: "api"}
	node, ok := g.Node(hpaRef)
	require.True(t, ok)
	hpa := node.Object.(*autoscalingv2.HorizontalPodAutoscaler)
	hpa.Generation = 2

	require.Empty(t, horizontalPodAutoscalerRule{}.Evaluate(g))
}

func TestHPAMissingObservedGenerationRemainsCompatible(t *testing.T) {
	g := hpaContractGraph(autoscalingv2.HorizontalPodAutoscalerCondition{
		Type: autoscalingv2.ScalingActive, Status: corev1.ConditionFalse,
		Reason: "FailedGetResourceMetric",
	}, 2, 5)
	hpaRef := graph.ResourceRef{Kind: "HorizontalPodAutoscaler", Namespace: "ns", Name: "api"}
	node, ok := g.Node(hpaRef)
	require.True(t, ok)
	node.Object.(*autoscalingv2.HorizontalPodAutoscaler).Status.ObservedGeneration = nil

	findings := horizontalPodAutoscalerRule{}.Evaluate(g)
	require.Len(t, findings, 1)
	require.Equal(t, HPAMetricsUnavailable, findings[0].Type)
}
