package diag

import (
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	utilversion "k8s.io/apimachinery/pkg/util/version"

	"github.com/leventozen/kubectl-tuna/internal/graph"
)

// Rule evaluates the resource graph and reports zero or more findings.
type Rule interface {
	ID() string
	Evaluate(g *graph.Graph) []*Finding
}

// Health summarizes the state of the inspected resource.
type Health string

const (
	HealthOK       Health = "healthy"
	HealthDegraded Health = "degraded"
	HealthUnknown  Health = "unknown"
)

// Result is the full output of one inspection.
type Result struct {
	Focus         graph.ResourceRef            `json:"focus"`
	Cluster       graph.ClusterInfo            `json:"cluster"`
	Collection    *graph.CollectionInfo        `json:"collection,omitempty"`
	Health        Health                       `json:"health"`
	Findings      []*Finding                   `json:"findings"`
	RootCauses    []*Finding                   `json:"-"`
	Standalone    []*Finding                   `json:"-"`
	ReplicaOwners map[string]graph.ResourceRef `json:"-"`
	Partial       bool                         `json:"partial"`
	Warnings      []graph.CollectionIssue      `json:"warnings,omitempty"`
	Rules         RuleEvaluationSummary        `json:"rules"`
}

type RuleSkip struct {
	ID            string                  `json:"id"`
	Reason        string                  `json:"reason"`
	Compatibility KubernetesCompatibility `json:"kubernetes"`
}

type RuleEvaluationSummary struct {
	Evaluated       int        `json:"evaluated"`
	Skipped         []RuleSkip `json:"skipped,omitempty"`
	SuspendedReason string     `json:"suspendedReason,omitempty"`
}

// Engine runs a set of rules over a graph and correlates their findings.
type Engine struct {
	registry *Registry
}

// NewEngine returns an engine with the default rule set.
func NewEngine() *Engine {
	return &Engine{registry: mustRegistry(DefaultRuleRegistrations())}
}

// NewEngineWithRegistry creates an engine from an already validated registry.
// The registry is still internal while the finding and graph contracts are
// pre-release; exposing it as a stable third-party API would freeze them too
// early.
func NewEngineWithRegistry(registry *Registry) *Engine {
	if registry == nil {
		panic("diag: nil rule registry")
	}
	return &Engine{registry: registry}
}

func (e *Engine) Evaluate(g *graph.Graph) *Result {
	var findings []*Finding
	var evaluationWarnings []graph.CollectionIssue
	ruleSummary := RuleEvaluationSummary{}
	versionUnavailable := g.HasCollectionIssue(graph.SourceServerVersion, g.Focus)
	temporalIntegrityUnavailable := g.HasCollectionSourceIssue(graph.SourceTemporalIntegrity)
	if temporalIntegrityUnavailable {
		ruleSummary.SuspendedReason = "resource stability or controller freshness was not established for the collected graph"
	}
	for _, registration := range e.registry.Registrations() {
		if temporalIntegrityUnavailable {
			continue
		}
		metadata := registration.Metadata
		var skipReason string
		switch {
		case versionUnavailable:
			skipReason = "Kubernetes API server version is unavailable"
		case g.HasKubernetesVersion():
			skipReason = compatibilitySkipReason(g, metadata)
		}
		if skipReason != "" {
			ruleSummary.Skipped = append(ruleSummary.Skipped, RuleSkip{
				ID: metadata.ID, Reason: skipReason, Compatibility: metadata.Compatibility,
			})
			continue
		}

		ruleSummary.Evaluated++
		for _, finding := range registration.Rule.Evaluate(g) {
			if finding == nil {
				continue
			}
			if !metadata.declares(finding.Type) {
				evaluationWarnings = append(evaluationWarnings, graph.CollectionIssue{
					Source: graph.SourceRuleExecution, Resource: g.Focus,
					Message: fmt.Sprintf(
						"rule %q emitted undeclared finding type %q; finding was discarded",
						metadata.ID, finding.Type,
					),
					AffectsHealth: true,
				})
				continue
			}
			finding.RuleID = metadata.ID
			findings = append(findings, finding)
		}
	}
	for _, f := range findings {
		if f.Impact == "" {
			f.Impact = ImpactCurrent
		}
	}
	sort.SliceStable(findings, func(i, j int) bool { return findingLess(findings[i], findings[j]) })
	for i, f := range findings {
		f.ID = fmt.Sprintf("f-%d", i+1)
	}

	Correlate(findings, g)

	res := &Result{
		Focus:         g.Focus,
		Cluster:       g.Cluster,
		Collection:    g.Collection,
		Health:        HealthOK,
		Findings:      findings,
		RootCauses:    RootCauses(findings),
		Standalone:    Standalone(findings),
		ReplicaOwners: replicaOwners(findings, g),
		Warnings:      g.CollectionIssues(),
		Rules:         ruleSummary,
	}
	res.Warnings = append(res.Warnings, evaluationWarnings...)
	if len(ruleSummary.Skipped) > 0 && !versionUnavailable {
		res.Warnings = append(res.Warnings, graph.CollectionIssue{
			Source: graph.SourceRuleCompatibility, Resource: g.Focus,
			Message: fmt.Sprintf(
				"%d rule(s) were not evaluated because required Kubernetes component compatibility was not established for %s; inspect JSON rules.skipped for details",
				len(ruleSummary.Skipped), g.Cluster.KubernetesVersion,
			),
			AffectsHealth: true,
		})
	}
	sort.Slice(ruleSummary.Skipped, func(i, j int) bool { return ruleSummary.Skipped[i].ID < ruleSummary.Skipped[j].ID })
	sort.Slice(res.Warnings, func(i, j int) bool {
		if res.Warnings[i].Source != res.Warnings[j].Source {
			return res.Warnings[i].Source < res.Warnings[j].Source
		}
		if res.Warnings[i].Resource != res.Warnings[j].Resource {
			return res.Warnings[i].Resource.String() < res.Warnings[j].Resource.String()
		}
		return res.Warnings[i].Message < res.Warnings[j].Message
	})
	res.Partial = len(res.Warnings) > 0
	for _, f := range findings {
		if f.Impact == ImpactCurrent && f.Resource == g.Focus {
			res.Health = HealthDegraded
			break
		}
	}
	if res.Health == HealthOK {
		for _, warning := range res.Warnings {
			if warning.AffectsHealth {
				res.Health = HealthUnknown
				break
			}
		}
	}
	return res
}

func compatibilitySkipReason(g *graph.Graph, metadata RuleMetadata) string {
	compatibility := metadata.Compatibility
	if !compatibility.supports(g.Cluster.Major, g.Cluster.Minor) {
		return fmt.Sprintf(
			"API server %s is outside the reviewed range %s-%s",
			g.Cluster.KubernetesVersion,
			compatibility.Min,
			compatibility.Max,
		)
	}
	if !metadata.usesVersionSource(VersionSourceKubelet) {
		return ""
	}
	return kubeletCompatibilitySkipReason(g, compatibility)
}

// kubeletCompatibilitySkipReason validates every related Node because a rule
// is evaluated once over the whole focused graph. Evaluating only the subset
// of Pods on reviewed kubelets would make the rule summary look complete while
// silently dropping evidence from the same inspection.
func kubeletCompatibilitySkipReason(g *graph.Graph, compatibility KubernetesCompatibility) string {
	server := utilversion.MajorMinor(g.Cluster.Major, g.Cluster.Minor)
	for _, graphNode := range g.NodesOfKind("Node") {
		node, ok := graphNode.Object.(*corev1.Node)
		if !ok || node.Status.NodeInfo.KubeletVersion == "" {
			return fmt.Sprintf("kubelet version for %s is unavailable", graphNode.Ref)
		}

		raw := node.Status.NodeInfo.KubeletVersion
		version, err := utilversion.ParseGeneric(raw)
		if err != nil || version.Major() == 0 {
			return fmt.Sprintf("kubelet version %q for %s is not usable", raw, graphNode.Ref)
		}
		minor := utilversion.MajorMinor(version.Major(), version.Minor())
		if server.LessThan(minor) {
			return fmt.Sprintf(
				"kubelet %s for %s is newer than API server %s",
				raw, graphNode.Ref, g.Cluster.KubernetesVersion,
			)
		}
		if !compatibility.supports(version.Major(), version.Minor()) {
			return fmt.Sprintf(
				"kubelet %s for %s is outside the reviewed range %s-%s",
				raw, graphNode.Ref, compatibility.Min, compatibility.Max,
			)
		}
	}
	return ""
}

// replicaOwners returns the exact Deployment owner for Pod findings when the
// graph proves Deployment --owns--> ReplicaSet --owns--> Pod. The map is
// presentation-only: console output may group equivalent replicas, while JSON
// keeps every original finding and causal edge unchanged.
func replicaOwners(findings []*Finding, g *graph.Graph) map[string]graph.ResourceRef {
	owners := make(map[string]graph.ResourceRef)
	deployments := g.NodesOfKind("Deployment")
	for _, finding := range findings {
		if finding.ID == "" || finding.Resource.Kind != "Pod" {
			continue
		}
		for _, deployment := range deployments {
			if g.HasTypedPath(deployment.Ref, finding.Resource, graph.EdgeOwns, graph.EdgeOwns) {
				owners[finding.ID] = deployment.Ref
				break
			}
		}
	}
	if len(owners) == 0 {
		return nil
	}
	return owners
}

func findingLess(a, b *Finding) bool {
	if a.Type != b.Type {
		return a.Type < b.Type
	}
	if a.Resource != b.Resource {
		return a.Resource.String() < b.Resource.String()
	}
	ac, bc := "", ""
	if a.Subject != nil {
		ac = a.Subject.Container
	}
	if b.Subject != nil {
		bc = b.Subject.Container
	}
	if ac != bc {
		return ac < bc
	}
	if a.Summary != b.Summary {
		return a.Summary < b.Summary
	}
	return a.Severity < b.Severity
}
