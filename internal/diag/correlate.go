package diag

import (
	"sort"

	"github.com/leventozen/kubectl-tuna/internal/graph"
)

// causalRule couples a Kubernetes mechanism with the exact graph relation it
// requires. Finding types alone are not enough: two resources can be nearby
// in an undirected graph without one being able to cause the other.
type causalRule struct {
	cause   FindingType
	effect  FindingType
	related func(cause, effect *Finding, g *graph.Graph) bool
}

var causalRules = []causalRule{
	{ReadinessProbePortInvalid, ReadinessProbeFailing, samePodComponent},
	{ReadinessProbeFailing, PodNotReady, sameResource},
	{MissingConfigRef, CrashLoopBackOff, samePodComponent},
	{MissingConfigRef, PodNotReady, sameResource},
	{ContainerOOMKilled, CrashLoopBackOff, samePodComponent},
	{ContainerSIGKILL, CrashLoopBackOff, samePodComponent},
	{CrashLoopBackOff, PodNotReady, sameResource},
	{ImagePullFailure, PodNotReady, sameResource},
	{PodUnschedulable, PodNotReady, sameResource},
	{PodNotReady, ServiceNoReadyEndpoints, selectedByService},
	{PodNotReady, DeploymentUnavailable, ownedByDeployment},
	{PodNotReady, RolloutStuck, ownedByDeployment},
	{ServiceSelectorNoPods, ServiceNoReadyEndpoints, sameResource},
	{NodePressure, PodEvicted, scheduledOnNode},
	{PodEvicted, PodNotReady, sameResource},
	{RolloutStuck, DeploymentUnavailable, sameResource},
}

// Correlate links findings into causal chains using explicit directional and
// typed graph predicates, mutating Causes/CausedBy on each finding.
func Correlate(findings []*Finding, g *graph.Graph) {
	for _, f := range findings {
		f.Causes = nil
		f.CausedBy = nil
	}
	for _, rule := range causalRules {
		for _, cause := range findings {
			if cause.Type != rule.cause {
				continue
			}
			for _, effect := range findings {
				if cause == effect || effect.Type != rule.effect || !rule.related(cause, effect, g) {
					continue
				}
				cause.Causes = append(cause.Causes, effect)
				effect.CausedBy = append(effect.CausedBy, cause)
			}
		}
	}
}

func sameResource(cause, effect *Finding, _ *graph.Graph) bool {
	return cause.Resource == effect.Resource
}

func samePodComponent(cause, effect *Finding, _ *graph.Graph) bool {
	if cause.Resource != effect.Resource || cause.Subject == nil || effect.Subject == nil {
		return false
	}
	return cause.Subject.Container != "" && cause.Subject.Container == effect.Subject.Container
}

func selectedByService(cause, effect *Finding, g *graph.Graph) bool {
	return g.HasEdge(effect.Resource, cause.Resource, graph.EdgeSelects)
}

func ownedByDeployment(cause, effect *Finding, g *graph.Graph) bool {
	return g.HasTypedPath(effect.Resource, cause.Resource, graph.EdgeOwns, graph.EdgeOwns)
}

func scheduledOnNode(cause, effect *Finding, g *graph.Graph) bool {
	return g.HasEdge(effect.Resource, cause.Resource, graph.EdgeScheduledOn)
}

// RootCauses returns findings that explain other findings but are not
// themselves explained, ordered deterministically by severity and identity.
func RootCauses(findings []*Finding) []*Finding {
	var roots []*Finding
	for _, f := range findings {
		if f.IsRootCandidate() {
			roots = append(roots, f)
		}
	}
	sortBySeverity(roots)
	return roots
}

// Standalone returns findings with no causal links in either direction.
func Standalone(findings []*Finding) []*Finding {
	var out []*Finding
	for _, f := range findings {
		if len(f.Causes) == 0 && len(f.CausedBy) == 0 {
			out = append(out, f)
		}
	}
	sortBySeverity(out)
	return out
}

// Chain returns one longest deterministic causal path starting at root.
func Chain(root *Finding) []*Finding {
	path := []*Finding{root}
	seen := map[*Finding]bool{root: true}
	cur := root
	for {
		var next *Finding
		best := -1
		for _, c := range cur.Causes {
			if seen[c] {
				continue
			}
			if l := chainDepth(c, seen); l > best {
				best = l
				next = c
			}
		}
		if next == nil {
			return path
		}
		seen[next] = true
		path = append(path, next)
		cur = next
	}
}

func chainDepth(f *Finding, seen map[*Finding]bool) int {
	best := 0
	for _, c := range f.Causes {
		if seen[c] {
			continue
		}
		seen2 := map[*Finding]bool{}
		for k, v := range seen {
			seen2[k] = v
		}
		seen2[c] = true
		if d := 1 + chainDepth(c, seen2); d > best {
			best = d
		}
	}
	return best
}

func sortBySeverity(fs []*Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		if MoreSevere(fs[i].Severity, fs[j].Severity) {
			return true
		}
		if MoreSevere(fs[j].Severity, fs[i].Severity) {
			return false
		}
		return findingLess(fs[i], fs[j])
	})
}
