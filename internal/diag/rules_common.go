package diag

import (
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/leventozen/kubectl-tuna/internal/graph"
)

const (
	minReviewedKubernetes = "1.34"
	maxReviewedKubernetes = "1.36"
)

func builtInRule(rule Rule, family RuleFamily, description string, findingTypes ...FindingType) RuleRegistration {
	return RuleRegistration{
		Rule: rule,
		Metadata: RuleMetadata{
			ID:           rule.ID(),
			Family:       family,
			Description:  description,
			FindingTypes: append([]FindingType(nil), findingTypes...),
			Compatibility: KubernetesCompatibility{
				Min: minReviewedKubernetes,
				Max: maxReviewedKubernetes,
			},
		},
	}
}

// DefaultRuleRegistrations returns the built-in rule set and the metadata
// every rule must declare before it can execute.
func DefaultRuleRegistrations() []RuleRegistration {
	return []RuleRegistration{
		// Traffic path
		builtInRule(serviceSelectorNoPodsRule{}, RuleFamilyTraffic, "report a Service selector that currently matches no Pods without guessing the intended workload", ServiceSelectorNoPods),
		builtInRule(serviceTargetPortMismatchRule{}, RuleFamilyTraffic, "validate Service target ports against selected Pod container ports", ServiceTargetPortMismatch),
		builtInRule(serviceNoReadyEndpointsRule{}, RuleFamilyTraffic, "classify EndpointSlice readiness for a Service", ServiceNoReadyEndpoints, ServiceTerminatingOnly),
		builtInRule(readinessProbePortRule{}, RuleFamilyTraffic, "validate Pod readiness probe ports against the target container", ReadinessProbePortInvalid),
		builtInRule(readinessProbeFailingRule{}, RuleFamilyTraffic, "report structured readiness failure state with narrowly matched events", ReadinessProbeFailing),
		builtInRule(podNotReadyRule{}, RuleFamilyTraffic, "report Pods whose Ready condition is not true", PodNotReady),
		// Workload lifecycle
		builtInRule(crashLoopRule{}, RuleFamilyWorkload, "report containers waiting in CrashLoopBackOff", CrashLoopBackOff),
		builtInRule(imagePullRule{}, RuleFamilyWorkload, "report container image pull failures", ImagePullFailure),
		builtInRule(missingConfigRefRule{}, RuleFamilyWorkload, "report definitively missing non-optional ConfigMap or Secret references", MissingConfigRef),
		builtInRule(oomKilledRule{}, RuleFamilyWorkload, "classify confirmed OOMKilled and ambiguous SIGKILL termination state", ContainerOOMKilled, ContainerSIGKILL),
		builtInRule(deploymentUnavailableRule{}, RuleFamilyWorkload, "report a Deployment below its desired availability", DeploymentUnavailable),
		builtInRule(rolloutStuckRule{}, RuleFamilyRollout, "report Deployment ProgressDeadlineExceeded state", RolloutStuck),
		// Scheduling and resources
		builtInRule(podUnschedulableRule{}, RuleFamilyScheduling, "classify a PodScheduled false condition and supporting scheduling events", PodUnschedulable),
		// Node and eviction
		builtInRule(nodePressureRule{}, RuleFamilyNode, "report active Node memory, disk, or PID pressure conditions", NodePressure),
		builtInRule(podEvictedRule{}, RuleFamilyNode, "report a Pod whose terminal reason is Evicted", PodEvicted),
	}
}

// podNodes returns every Pod in the graph together with its typed object.
func podNodes(g *graph.Graph) []podNode {
	var out []podNode
	for _, n := range g.NodesOfKind("Pod") {
		if pod, ok := n.Object.(*corev1.Pod); ok {
			out = append(out, podNode{ref: n.Ref, pod: pod})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ref.Name < out[j].ref.Name })
	return out
}

type podNode struct {
	ref graph.ResourceRef
	pod *corev1.Pod
}

func podIsReady(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func podCondition(pod *corev1.Pod, t corev1.PodConditionType) *corev1.PodCondition {
	for i := range pod.Status.Conditions {
		if pod.Status.Conditions[i].Type == t {
			return &pod.Status.Conditions[i]
		}
	}
	return nil
}

func formatLabels(m map[string]string) string {
	if len(m) == 0 {
		return "<none>"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, m[k]))
	}
	return strings.Join(parts, ",")
}

func containerPortList(c corev1.Container) string {
	if len(c.Ports) == 0 {
		return "<none declared>"
	}
	var parts []string
	for _, p := range c.Ports {
		if p.Name != "" {
			parts = append(parts, fmt.Sprintf("%d (name: %s)", p.ContainerPort, p.Name))
		} else {
			parts = append(parts, fmt.Sprintf("%d", p.ContainerPort))
		}
	}
	return strings.Join(parts, ", ")
}

// eventsMatching returns events for ref whose reason is in reasons.
func eventsMatching(g *graph.Graph, ref graph.ResourceRef, reasons ...string) []corev1.Event {
	var out []corev1.Event
	for _, ev := range g.EventsFor(ref) {
		for _, r := range reasons {
			if ev.Reason == r {
				out = append(out, ev)
				break
			}
		}
	}
	return out
}

func eventCount(ev corev1.Event) int32 {
	if ev.Count > 0 {
		return ev.Count
	}
	return 1
}

func allContainerStatuses(pod *corev1.Pod) []corev1.ContainerStatus {
	out := make([]corev1.ContainerStatus, 0, len(pod.Status.InitContainerStatuses)+len(pod.Status.ContainerStatuses))
	out = append(out, pod.Status.InitContainerStatuses...)
	out = append(out, pod.Status.ContainerStatuses...)
	return out
}

func allPodContainers(pod *corev1.Pod) []corev1.Container {
	out := make([]corev1.Container, 0, len(pod.Spec.InitContainers)+len(pod.Spec.Containers))
	out = append(out, pod.Spec.InitContainers...)
	out = append(out, pod.Spec.Containers...)
	return out
}

func containerSpec(pod *corev1.Pod, name string) *corev1.Container {
	containers := allPodContainers(pod)
	for i := range containers {
		if containers[i].Name == name {
			return &containers[i]
		}
	}
	return nil
}
