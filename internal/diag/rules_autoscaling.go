package diag

import (
	"fmt"
	"sort"
	"strings"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"

	"github.com/leventozen/kubectl-tuna/internal/graph"
)

// horizontalPodAutoscalerRule reports two structured HPA controller states:
// metric fetch failures that prevent replica calculation, and an upper replica
// bound that actively caps a scale-up recommendation. Both are capacity risks,
// not proof that the target workload is currently unavailable.
type horizontalPodAutoscalerRule struct{}

func (horizontalPodAutoscalerRule) ID() string { return "horizontal-pod-autoscaler" }

var hpaMetricFailureReasons = map[string]struct{}{
	"FailedGetContainerResourceMetric": {},
	"FailedGetExternalMetric":          {},
	"FailedGetObjectMetric":            {},
	"FailedGetPodsMetric":              {},
	"FailedGetResourceMetric":          {},
}

func (horizontalPodAutoscalerRule) Evaluate(g *graph.Graph) []*Finding {
	var findings []*Finding
	for _, node := range g.NodesOfKind("HorizontalPodAutoscaler") {
		hpa, ok := node.Object.(*autoscalingv2.HorizontalPodAutoscaler)
		if !ok || hpa == nil || !hpaStatusIsCurrent(hpa) {
			continue
		}
		target, ok := hpaDeploymentTarget(g, node.Ref)
		if !ok {
			continue
		}

		if condition := hpaCondition(hpa, autoscalingv2.ScalingActive); condition != nil &&
			condition.Status == corev1.ConditionFalse && isHPAMetricFailureReason(condition.Reason) {
			findings = append(findings, &Finding{
				Type:       HPAMetricsUnavailable,
				Severity:   SeverityWarning,
				Confidence: ConfidenceHigh,
				Impact:     ImpactRisk,
				Resource:   node.Ref,
				Summary:    fmt.Sprintf("HorizontalPodAutoscaler cannot calculate replicas for Deployment %q from its configured metrics", target.Name),
				Detail:     "The HPA controller reports a metric-fetch failure. Automatic capacity adjustment is impaired, but this condition alone does not prove that the Deployment is unavailable or underprovisioned.",
				Evidence: []Evidence{
					{Source: "HorizontalPodAutoscaler.spec.scaleTargetRef", Value: target.String()},
					{Source: "HorizontalPodAutoscaler.status.conditions[ScalingActive]", Value: formatHPACondition(condition)},
					{Source: "HorizontalPodAutoscaler.spec.metrics", Value: hpaMetricSummary(hpa.Spec.Metrics)},
				},
				Recommendations: []string{
					fmt.Sprintf("Inspect the HPA and its events: kubectl describe hpa %s -n %s", hpa.Name, hpa.Namespace),
					"Verify the relevant resource, custom, or external metrics API and the target Pods' metric prerequisites.",
				},
			})
		}

		if condition := hpaCondition(hpa, autoscalingv2.ScalingLimited); condition != nil &&
			condition.Status == corev1.ConditionTrue && condition.Reason == "TooManyReplicas" &&
			hpa.Spec.MaxReplicas > 0 && hpa.Status.DesiredReplicas == hpa.Spec.MaxReplicas {
			findings = append(findings, &Finding{
				Type:       HPAScaleUpLimited,
				Severity:   SeverityWarning,
				Confidence: ConfidenceHigh,
				Impact:     ImpactRisk,
				Resource:   node.Ref,
				Summary:    fmt.Sprintf("HorizontalPodAutoscaler capped Deployment %q at maxReplicas=%d", target.Name, hpa.Spec.MaxReplicas),
				Detail:     "The HPA controller calculated a scale-up beyond the configured upper bound and capped the desired replica count. This is a concrete capacity constraint, not by itself proof of user-visible saturation.",
				Evidence: []Evidence{
					{Source: "HorizontalPodAutoscaler.spec.scaleTargetRef", Value: target.String()},
					{Source: "HorizontalPodAutoscaler.status.conditions[ScalingLimited]", Value: formatHPACondition(condition)},
					{Source: "HorizontalPodAutoscaler.status", Value: fmt.Sprintf("currentReplicas=%d desiredReplicas=%d maxReplicas=%d", hpa.Status.CurrentReplicas, hpa.Status.DesiredReplicas, hpa.Spec.MaxReplicas)},
				},
				Recommendations: []string{
					"Confirm that the observed demand is sustained and that downstream dependencies can absorb additional replicas.",
					fmt.Sprintf("If capacity and cost allow, review spec.maxReplicas on HorizontalPodAutoscaler %q.", hpa.Name),
				},
			})
		}
	}
	return findings
}

func hpaStatusIsCurrent(hpa *autoscalingv2.HorizontalPodAutoscaler) bool {
	// HPA observedGeneration is not populated by the controller throughout the
	// whole reviewed window. Only reject concrete evidence that it is stale.
	if hpa.Generation <= 0 || hpa.Status.ObservedGeneration == nil {
		return true
	}
	return *hpa.Status.ObservedGeneration >= hpa.Generation
}

func hpaDeploymentTarget(g *graph.Graph, hpaRef graph.ResourceRef) (graph.ResourceRef, bool) {
	edges := g.EdgesFrom(hpaRef, graph.EdgeScales)
	if len(edges) != 1 || edges[0].To.Kind != "Deployment" {
		return graph.ResourceRef{}, false
	}
	return edges[0].To, true
}

func hpaCondition(hpa *autoscalingv2.HorizontalPodAutoscaler, conditionType autoscalingv2.HorizontalPodAutoscalerConditionType) *autoscalingv2.HorizontalPodAutoscalerCondition {
	for i := range hpa.Status.Conditions {
		if hpa.Status.Conditions[i].Type == conditionType {
			return &hpa.Status.Conditions[i]
		}
	}
	return nil
}

func isHPAMetricFailureReason(reason string) bool {
	_, ok := hpaMetricFailureReasons[reason]
	return ok
}

func formatHPACondition(condition *autoscalingv2.HorizontalPodAutoscalerCondition) string {
	return fmt.Sprintf("status=%s reason=%s message=%s", condition.Status, condition.Reason, condition.Message)
}

func hpaMetricSummary(metrics []autoscalingv2.MetricSpec) string {
	if len(metrics) == 0 {
		return "<default CPU utilization metric>"
	}
	types := make([]string, 0, len(metrics))
	for _, metric := range metrics {
		types = append(types, string(metric.Type))
	}
	sort.Strings(types)
	return strings.Join(types, ",")
}
