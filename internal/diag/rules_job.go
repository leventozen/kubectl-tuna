package diag

import (
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/leventozen/kubectl-tuna/internal/graph"
)

const cronJobScheduledTimestampAnnotation = "batch.kubernetes.io/cronjob-scheduled-timestamp"

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

// cronJobLatestRunFailedRule deliberately depends on a collector-established
// latest-run edge. Ordinary CronJob ownership is insufficient because retained
// historical failures must not make a newer healthy or active run look broken.
type cronJobLatestRunFailedRule struct{}

func (cronJobLatestRunFailedRule) ID() string { return "cronjob-latest-run-failed" }

func (cronJobLatestRunFailedRule) Evaluate(g *graph.Graph) []*Finding {
	var findings []*Finding
	for _, node := range g.NodesOfKind("CronJob") {
		cronJob, ok := node.Object.(*batchv1.CronJob)
		if !ok {
			continue
		}
		for _, edge := range g.EdgesFrom(node.Ref, graph.EdgeLatestRun) {
			jobNode, exists := g.Node(edge.To)
			if !exists {
				continue
			}
			job, ok := jobNode.Object.(*batchv1.Job)
			if !ok {
				continue
			}
			condition := trueJobCondition(job, batchv1.JobFailed)
			if condition == nil {
				condition = trueJobCondition(job, batchv1.JobFailureTarget)
			}
			if condition == nil {
				continue
			}

			scheduledAt := job.Annotations[cronJobScheduledTimestampAnnotation]
			findings = append(findings, &Finding{
				Type:       CronJobLatestRunFailed,
				Severity:   SeverityCritical,
				Confidence: ConfidenceHigh,
				Resource:   node.Ref,
				Summary:    fmt.Sprintf("CronJob latest scheduled run %q met failure criteria", job.Name),
				Detail:     "The uniquely latest owned Job, ordered by the controller-authored scheduled timestamp, has a Failed or FailureTarget condition. Older retained Job failures are not used for this conclusion.",
				Evidence: []Evidence{
					{Source: fmt.Sprintf("Job/%s.metadata.annotations[%s]", job.Name, cronJobScheduledTimestampAnnotation), Value: scheduledAt},
					{Source: fmt.Sprintf("Job/%s.status.conditions[%s]", job.Name, condition.Type), Value: formatJobCondition(*condition)},
				},
				Recommendations: []string{
					fmt.Sprintf("Inspect the latest Job and its logs: kubectl describe job %s -n %s; kubectl logs job/%s -n %s", job.Name, cronJob.Namespace, job.Name, cronJob.Namespace),
				},
			})
		}
	}
	return findings
}
