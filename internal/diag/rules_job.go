package diag

import (
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/leventozen/kubectl-tuna/internal/graph"
)

// jobFailedRule relies only on controller-authored Job conditions. Failed is
// terminal; FailureTarget means failure criteria have been met while Pods are
// still terminating. Pod counters alone do not prove either outcome.
type jobFailedRule struct{}

func (jobFailedRule) ID() string { return "job-failed" }

func (jobFailedRule) Evaluate(g *graph.Graph) []*Finding {
	var findings []*Finding
	for _, n := range g.NodesOfKind("Job") {
		job, ok := n.Object.(*batchv1.Job)
		if !ok {
			continue
		}
		condition := trueJobCondition(job, batchv1.JobFailed)
		terminal := condition != nil
		if condition == nil {
			condition = trueJobCondition(job, batchv1.JobFailureTarget)
		}
		if condition == nil {
			continue
		}

		state := "has failed"
		detail := "The Job controller set the terminal Failed condition. Pod failures, retry counts, or elapsed time alone are not used to infer this state."
		if !terminal {
			state = "has met failure criteria and is terminating"
			detail = "The Job controller set FailureTarget after failure criteria were met. Failed is not terminal yet because owned Pods may still be terminating."
		}
		evidence := []Evidence{
			{Source: fmt.Sprintf("Job.status.conditions[%s]", condition.Type), Value: formatJobCondition(*condition)},
			{Source: "Job.status", Value: fmt.Sprintf("active=%d succeeded=%d failed=%d", job.Status.Active, job.Status.Succeeded, job.Status.Failed)},
		}
		findings = append(findings, &Finding{
			Type:       JobFailed,
			Severity:   SeverityCritical,
			Confidence: ConfidenceHigh,
			Resource:   n.Ref,
			Summary:    fmt.Sprintf("Job %s", state),
			Detail:     detail,
			Evidence:   evidence,
			Recommendations: []string{
				fmt.Sprintf("Inspect Job events and logs: kubectl describe job %s -n %s; kubectl logs job/%s -n %s", job.Name, job.Namespace, job.Name, job.Namespace),
			},
		})
	}
	return findings
}

func trueJobCondition(job *batchv1.Job, conditionType batchv1.JobConditionType) *batchv1.JobCondition {
	for i := range job.Status.Conditions {
		condition := &job.Status.Conditions[i]
		if condition.Type == conditionType && condition.Status == corev1.ConditionTrue {
			return condition
		}
	}
	return nil
}

func formatJobCondition(condition batchv1.JobCondition) string {
	value := fmt.Sprintf("status=%s", condition.Status)
	if condition.Reason != "" {
		value += " reason=" + condition.Reason
	}
	if condition.Message != "" {
		value += " message=" + condition.Message
	}
	return value
}
