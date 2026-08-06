package diag

import (
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/leventozen/kubectl-tuna/internal/graph"
)

// crashLoopRule fires when a container is in CrashLoopBackOff.
type crashLoopRule struct{}

func (crashLoopRule) ID() string { return "crashloop-backoff" }

func (crashLoopRule) Evaluate(g *graph.Graph) []*Finding {
	var findings []*Finding
	for _, pn := range podNodes(g) {
		for _, cs := range allContainerStatuses(pn.pod) {
			if cs.State.Waiting == nil || cs.State.Waiting.Reason != "CrashLoopBackOff" {
				continue
			}
			f := &Finding{
				Type:       CrashLoopBackOff,
				Severity:   SeverityCritical,
				Confidence: ConfidenceHigh,
				Resource:   pn.ref,
				Subject:    containerSubject(cs.Name),
				Summary:    fmt.Sprintf("Container %q is in CrashLoopBackOff (%d restarts)", cs.Name, cs.RestartCount),
				Evidence: []Evidence{
					{Source: fmt.Sprintf("containerStatuses[%s].state.waiting.reason", cs.Name), Value: "CrashLoopBackOff"},
					{Source: "restartCount", Value: fmt.Sprintf("%d", cs.RestartCount)},
				},
				Recommendations: []string{
					fmt.Sprintf("Inspect the previous container logs: kubectl logs %s -n %s -c %s --previous",
						pn.pod.Name, pn.pod.Namespace, cs.Name),
				},
			}
			if term := cs.LastTerminationState.Terminated; term != nil {
				f.Evidence = append(f.Evidence, Evidence{
					Source: "lastState.terminated",
					Value:  fmt.Sprintf("reason=%s exitCode=%d", term.Reason, term.ExitCode),
				})
				if term.Reason != "OOMKilled" {
					f.Detail = fmt.Sprintf("The container keeps exiting with code %d. The structured state confirms the restart loop, but the cause may be in the application, runtime, or lifecycle hooks; inspect the previous container logs.", term.ExitCode)
				}
			}
			findings = append(findings, f)
		}
	}
	return findings
}

// imagePullRule fires when a container image cannot be pulled.
type imagePullRule struct{}

func (imagePullRule) ID() string { return "image-pull-failure" }

var imagePullReasons = map[string]bool{
	"ImagePullBackOff": true,
	"ErrImagePull":     true,
	"InvalidImageName": true,
}

func (imagePullRule) Evaluate(g *graph.Graph) []*Finding {
	var findings []*Finding
	for _, pn := range podNodes(g) {
		for _, cs := range allContainerStatuses(pn.pod) {
			if cs.State.Waiting == nil || !imagePullReasons[cs.State.Waiting.Reason] {
				continue
			}
			f := &Finding{
				Type:       ImagePullFailure,
				Severity:   SeverityCritical,
				Confidence: ConfidenceHigh,
				Resource:   pn.ref,
				Subject:    containerSubject(cs.Name),
				Summary:    fmt.Sprintf("Container %q cannot pull image %q (%s)", cs.Name, cs.Image, cs.State.Waiting.Reason),
				Evidence: []Evidence{
					{Source: fmt.Sprintf("containerStatuses[%s].state.waiting.reason", cs.Name), Value: cs.State.Waiting.Reason},
					{Source: "image", Value: cs.Image},
				},
				Recommendations: []string{
					"Verify the image name and tag exist in the registry.",
					"If the registry is private, verify imagePullSecrets are configured and valid.",
				},
			}
			pulls := eventsMatching(g, pn.ref, "Failed")
			for i := len(pulls) - 1; i >= 0; i-- {
				pull := pulls[i]
				if strings.Contains(strings.ToLower(pull.Message), "pull") || strings.Contains(pull.Message, cs.Image) {
					f.Evidence = append(f.Evidence, Evidence{Source: "events (reason: Failed)", Value: pull.Message})
					break
				}
			}
			findings = append(findings, f)
		}
	}
	return findings
}

// missingConfigRefRule fires when a Pod references a ConfigMap or Secret
// that does not exist.
type missingConfigRefRule struct{}

func (missingConfigRefRule) ID() string { return "missing-config-reference" }

func (missingConfigRefRule) Evaluate(g *graph.Graph) []*Finding {
	var findings []*Finding
	for _, pn := range podNodes(g) {
		for _, ref := range groupConfigRefs(configRefs(pn.pod)) {
			node, ok := g.Node(graph.ResourceRef{Kind: ref.kind, Namespace: pn.pod.Namespace, Name: ref.name})
			if !ok || !node.Missing() {
				continue
			}
			evidence := make([]Evidence, 0, len(ref.usages)+1)
			for _, usage := range ref.usages {
				evidence = append(evidence, Evidence{Source: usage, Value: fmt.Sprintf("%s %q (optional: false)", ref.kind, ref.name)})
			}
			evidence = append(evidence, Evidence{
				Source: "lookup", Value: fmt.Sprintf("%s %q not found in namespace %q", ref.kind, ref.name, pn.pod.Namespace),
			})
			f := &Finding{
				Type:       MissingConfigRef,
				Severity:   SeverityCritical,
				Confidence: ConfidenceHigh,
				Resource:   pn.ref,
				Subject:    containerSubject(ref.container),
				Summary:    fmt.Sprintf("%s %q is referenced by the Pod but does not exist", ref.kind, ref.name),
				Detail:     fmt.Sprintf("Referenced via %s. Kubernetes cannot start (or restart) containers that depend on a missing %s.", strings.Join(ref.usages, ", "), ref.kind),
				Evidence:   evidence,
				Recommendations: []string{
					fmt.Sprintf("Create the missing %s %q in namespace %q, or fix the reference name.", ref.kind, ref.name, pn.pod.Namespace),
				},
			}
			for _, cs := range allContainerStatuses(pn.pod) {
				if ref.container != "" && cs.Name != ref.container {
					continue
				}
				if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CreateContainerConfigError" {
					f.Evidence = append(f.Evidence, Evidence{
						Source: fmt.Sprintf("containerStatuses[%s].state.waiting", cs.Name),
						Value:  fmt.Sprintf("CreateContainerConfigError: %s", cs.State.Waiting.Message),
					})
				}
			}
			findings = append(findings, f)
		}
	}
	return findings
}

type configRef struct {
	kind      string // "ConfigMap" or "Secret"
	name      string
	usage     string // where the Pod references it
	container string
}

type configRefGroup struct {
	kind      string
	name      string
	container string
	usages    []string
}

func groupConfigRefs(refs []configRef) []configRefGroup {
	var groups []configRefGroup
	indexes := map[string]int{}
	for _, ref := range refs {
		key := ref.kind + "\x00" + ref.name + "\x00" + ref.container
		if idx, ok := indexes[key]; ok {
			duplicate := false
			for _, usage := range groups[idx].usages {
				if usage == ref.usage {
					duplicate = true
					break
				}
			}
			if !duplicate {
				groups[idx].usages = append(groups[idx].usages, ref.usage)
			}
			continue
		}
		indexes[key] = len(groups)
		groups = append(groups, configRefGroup{
			kind: ref.kind, name: ref.name, container: ref.container, usages: []string{ref.usage},
		})
	}
	return groups
}

// configRefs lists all non-optional ConfigMap/Secret references in a Pod spec.
func configRefs(pod *corev1.Pod) []configRef {
	var refs []configRef
	notOptional := func(o *bool) bool { return o == nil || !*o }

	containers := append(append([]corev1.Container{}, pod.Spec.InitContainers...), pod.Spec.Containers...)
	for _, c := range containers {
		for _, ef := range c.EnvFrom {
			if ef.ConfigMapRef != nil && notOptional(ef.ConfigMapRef.Optional) {
				refs = append(refs, configRef{"ConfigMap", ef.ConfigMapRef.Name, fmt.Sprintf("container[%s].envFrom.configMapRef", c.Name), c.Name})
			}
			if ef.SecretRef != nil && notOptional(ef.SecretRef.Optional) {
				refs = append(refs, configRef{"Secret", ef.SecretRef.Name, fmt.Sprintf("container[%s].envFrom.secretRef", c.Name), c.Name})
			}
		}
		for _, env := range c.Env {
			if env.ValueFrom == nil {
				continue
			}
			if r := env.ValueFrom.ConfigMapKeyRef; r != nil && notOptional(r.Optional) {
				refs = append(refs, configRef{"ConfigMap", r.Name, fmt.Sprintf("container[%s].env[%s].valueFrom.configMapKeyRef", c.Name, env.Name), c.Name})
			}
			if r := env.ValueFrom.SecretKeyRef; r != nil && notOptional(r.Optional) {
				refs = append(refs, configRef{"Secret", r.Name, fmt.Sprintf("container[%s].env[%s].valueFrom.secretKeyRef", c.Name, env.Name), c.Name})
			}
		}
	}
	for _, v := range pod.Spec.Volumes {
		container := soleVolumeConsumer(pod, v.Name)
		if v.ConfigMap != nil && notOptional(v.ConfigMap.Optional) {
			refs = append(refs, configRef{"ConfigMap", v.ConfigMap.Name, fmt.Sprintf("volume[%s].configMap", v.Name), container})
		}
		if v.Secret != nil && notOptional(v.Secret.Optional) {
			refs = append(refs, configRef{"Secret", v.Secret.SecretName, fmt.Sprintf("volume[%s].secret", v.Name), container})
		}
		if v.Projected != nil {
			for i, source := range v.Projected.Sources {
				if source.ConfigMap != nil && notOptional(source.ConfigMap.Optional) {
					refs = append(refs, configRef{"ConfigMap", source.ConfigMap.Name, fmt.Sprintf("volume[%s].projected.sources[%d].configMap", v.Name, i), container})
				}
				if source.Secret != nil && notOptional(source.Secret.Optional) {
					refs = append(refs, configRef{"Secret", source.Secret.Name, fmt.Sprintf("volume[%s].projected.sources[%d].secret", v.Name, i), container})
				}
			}
		}
	}
	return refs
}

func soleVolumeConsumer(pod *corev1.Pod, volumeName string) string {
	consumer := ""
	for _, container := range allPodContainers(pod) {
		for _, mount := range container.VolumeMounts {
			if mount.Name != volumeName {
				continue
			}
			if consumer != "" && consumer != container.Name {
				return ""
			}
			consumer = container.Name
		}
	}
	return consumer
}

// oomKilledRule fires when a container was terminated by the kernel OOM
// killer, and distinguishes cgroup limit kills from node-pressure eviction.
type oomKilledRule struct{}

func (oomKilledRule) ID() string { return "container-oomkilled" }

func (oomKilledRule) Evaluate(g *graph.Graph) []*Finding {
	var findings []*Finding
	for _, pn := range podNodes(g) {
		for _, cs := range allContainerStatuses(pn.pod) {
			term := cs.LastTerminationState.Terminated
			if term == nil && cs.State.Terminated != nil {
				term = cs.State.Terminated
			}
			if term == nil {
				continue
			}

			var limit string
			hasMemoryLimit := false
			if container := containerSpec(pn.pod, cs.Name); container != nil {
				if l, ok := container.Resources.Limits[corev1.ResourceMemory]; ok {
					limit = l.String()
					hasMemoryLimit = true
				} else {
					limit = "<no memory limit set>"
				}
			}

			evicted := len(eventsMatching(g, pn.ref, "Evicted")) > 0
			oomEvents := eventsMatching(g, pn.ref, "OOMKilling")
			explicit := term.Reason == "OOMKilled" || len(oomEvents) > 0

			// Error/137 is an ambiguous SIGKILL signal, not an OOM fact. Keep
			// it as a separate finding only when a memory limit and restart
			// loop make the signal actionable and no collected eviction or
			// kubelet Killing event already explains it.
			ambiguousSIGKILL := !explicit &&
				term.ExitCode == 137 &&
				hasMemoryLimit &&
				cs.RestartCount >= 1 &&
				!g.HasCollectionSourceIssue(graph.SourceEvents) &&
				!evicted &&
				len(eventsMatching(g, pn.ref, "Killing")) == 0

			if !explicit && !ambiguousSIGKILL {
				continue
			}

			findingType := ContainerOOMKilled
			if ambiguousSIGKILL {
				findingType = ContainerSIGKILL
			}
			f := &Finding{
				Type:     findingType,
				Severity: SeverityCritical,
				Resource: pn.ref,
				Subject:  containerSubject(cs.Name),
				Evidence: []Evidence{
					{Source: fmt.Sprintf("containerStatuses[%s].lastState.terminated", cs.Name), Value: fmt.Sprintf("reason=%s exitCode=%d", term.Reason, term.ExitCode)},
					{Source: fmt.Sprintf("container[%s].resources.limits.memory", cs.Name), Value: limit},
					{Source: "restartCount", Value: fmt.Sprintf("%d", cs.RestartCount)},
					{Source: "Pod.status.qosClass", Value: string(pn.pod.Status.QOSClass)},
				},
				Recommendations: []string{
					"Inspect the application's memory usage pattern before raising the limit; a leak will consume any limit.",
					"If the working set is legitimately larger than the limit, raise resources.limits.memory.",
				},
			}

			if len(oomEvents) > 0 {
				f.Evidence = append(f.Evidence, Evidence{
					Source: "events (reason: OOMKilling)",
					Value:  oomEvents[len(oomEvents)-1].Message,
				})
			}

			if podIsReady(pn.pod) {
				f.Impact = ImpactHistorical
			}

			switch {
			case explicit && hasMemoryLimit && !evicted:
				f.Confidence = ConfidenceHigh
				f.Summary = fmt.Sprintf("Container %q was OOMKilled; its memory limit is the likely boundary", cs.Name)
				f.Detail = "The runtime reported OOMKilled and the container has a memory limit. This is consistent with a cgroup limit kill, but cluster events alone cannot exclude every node-level contributing factor."
			case explicit && evicted:
				f.Confidence = ConfidenceMedium
				f.Summary = fmt.Sprintf("Container %q was OOMKilled while eviction evidence was also present", cs.Name)
				f.Detail = "Eviction events were also observed on this Pod; node memory pressure may be involved."
			case explicit:
				f.Confidence = ConfidenceMedium
				f.Summary = fmt.Sprintf("Container %q was OOMKilled", cs.Name)
				f.Detail = "The runtime confirms an OOM kill, but this container has no memory limit. The available evidence cannot attribute the kill to a container cgroup limit; inspect node pressure and kernel logs."
			default: // ambiguous SIGKILL; OOM is only one possible explanation
				f.Confidence = ConfidenceMedium
				f.Summary = fmt.Sprintf("Container %q exited with code 137 (SIGKILL); an OOM kill is possible but unconfirmed", cs.Name)
				f.Detail = "The runtime reported reason=Error, not OOMKilled. A memory limit and restart loop make OOM worth investigating, but exit 137 can have other causes. Confirm with node kernel logs, runtime evidence, or application lifecycle behavior before attributing it to memory."
			}
			findings = append(findings, f)
		}
	}
	return findings
}

// deploymentUnavailableRule fires when a Deployment has fewer available
// replicas than desired. Usually a propagated symptom.
type deploymentUnavailableRule struct{}

func (deploymentUnavailableRule) ID() string { return "deployment-unavailable" }

func (deploymentUnavailableRule) Evaluate(g *graph.Graph) []*Finding {
	var findings []*Finding
	for _, n := range g.NodesOfKind("Deployment") {
		dep, ok := n.Object.(*appsv1.Deployment)
		if !ok {
			continue
		}
		desired := int32(1)
		if dep.Spec.Replicas != nil {
			desired = *dep.Spec.Replicas
		}
		if desired == 0 || dep.Status.AvailableReplicas >= desired {
			continue
		}

		f := &Finding{
			Type:       DeploymentUnavailable,
			Severity:   SeverityWarning,
			Confidence: ConfidenceHigh,
			Resource:   n.Ref,
			Summary: fmt.Sprintf("Deployment has %d/%d available replicas",
				dep.Status.AvailableReplicas, desired),
			Evidence: []Evidence{
				{Source: "Deployment.status", Value: fmt.Sprintf("desired=%d updated=%d ready=%d available=%d",
					desired, dep.Status.UpdatedReplicas, dep.Status.ReadyReplicas, dep.Status.AvailableReplicas)},
			},
		}
		for _, cond := range dep.Status.Conditions {
			if cond.Type == appsv1.DeploymentProgressing && cond.Reason == "ProgressDeadlineExceeded" {
				f.Detail = "The rollout is stuck: the Deployment exceeded its progress deadline."
				f.Severity = SeverityCritical
				f.Evidence = append(f.Evidence, Evidence{
					Source: "condition Progressing", Value: fmt.Sprintf("%s: %s", cond.Reason, cond.Message)})
			}
		}
		findings = append(findings, f)
	}
	return findings
}
