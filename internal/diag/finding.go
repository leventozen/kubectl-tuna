// Package diag contains Tuna's diagnostic engine: the rules that evaluate a
// resource graph, the findings they produce, and the correlation logic that
// links findings into causal chains and separates root causes from
// propagated symptoms.
package diag

import "github.com/leventozen/kubectl-tuna/internal/graph"

type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityWarning  Severity = "warning"
	SeverityInfo     Severity = "info"
)

type Confidence string

const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

// Impact separates an active problem from a risk or historical observation.
// Only current findings on the inspected resource make its health degraded.
type Impact string

const (
	ImpactCurrent    Impact = "current"
	ImpactRisk       Impact = "risk"
	ImpactHistorical Impact = "historical"
)

// Subject identifies the component inside a resource that a finding is
// about. Container identity prevents cross-container causal links in a
// multi-container Pod.
type Subject struct {
	Container string `json:"container,omitempty"`
}

// FindingType identifies a class of diagnosis. Causal relationships are
// declared between finding types, not between individual rules.
type FindingType string

const (
	// Traffic path
	ServiceSelectorNoPods     FindingType = "service-selector-no-pods"
	ServiceTargetPortMismatch FindingType = "service-target-port-mismatch"
	ServiceNoReadyEndpoints   FindingType = "service-no-ready-endpoints"
	ServiceTerminatingOnly    FindingType = "service-terminating-endpoints-only"
	ReadinessProbePortInvalid FindingType = "readiness-probe-port-mismatch"
	ReadinessProbeFailing     FindingType = "readiness-probe-failing"
	PodNotReady               FindingType = "pod-not-ready"

	// Workload lifecycle
	CrashLoopBackOff      FindingType = "crashloop-backoff"
	ImagePullFailure      FindingType = "image-pull-failure"
	MissingConfigRef      FindingType = "missing-config-reference"
	ContainerOOMKilled    FindingType = "container-oomkilled"
	ContainerSIGKILL      FindingType = "container-sigkill"
	DeploymentUnavailable FindingType = "deployment-unavailable"
	RolloutStuck          FindingType = "rollout-stuck"

	// Scheduling and resources
	PodUnschedulable FindingType = "pod-unschedulable"

	// Node and eviction
	NodePressure FindingType = "node-pressure"
	PodEvicted   FindingType = "pod-evicted"
)

// Evidence is a single observed fact supporting a finding. Source names
// where the fact comes from (a field path, an event, a computed count) so
// the user can verify it themselves.
type Evidence struct {
	Source string `json:"source"`
	Value  string `json:"value"`
}

// Finding is a single diagnostic conclusion about one resource, backed by
// evidence. Causes/CausedBy are populated by the correlation engine after
// all rules have run.
type Finding struct {
	ID         string            `json:"id"`
	RuleID     string            `json:"ruleId"`
	Type       FindingType       `json:"type"`
	Severity   Severity          `json:"severity"`
	Confidence Confidence        `json:"confidence"`
	Impact     Impact            `json:"impact"`
	Resource   graph.ResourceRef `json:"resource"`
	Subject    *Subject          `json:"subject,omitempty"`
	Summary    string            `json:"summary"`
	Detail     string            `json:"detail,omitempty"`
	Evidence   []Evidence        `json:"evidence,omitempty"`

	Recommendations []string `json:"recommendations,omitempty"`

	// Causes lists findings that this finding explains (downstream symptoms).
	// CausedBy lists findings that explain this finding (upstream causes).
	Causes   []*Finding `json:"-"`
	CausedBy []*Finding `json:"-"`
}

// IsRootCandidate reports whether the finding explains other findings while
// not being explained by any finding itself.
func (f *Finding) IsRootCandidate() bool {
	return len(f.CausedBy) == 0 && len(f.Causes) > 0
}

// IsSymptom reports whether the finding is explained by another finding.
func (f *Finding) IsSymptom() bool { return len(f.CausedBy) > 0 }

var severityRank = map[Severity]int{
	SeverityCritical: 0,
	SeverityWarning:  1,
	SeverityInfo:     2,
}

// MoreSevere reports whether a outranks b.
func MoreSevere(a, b Severity) bool { return severityRank[a] < severityRank[b] }

func containerSubject(name string) *Subject {
	if name == "" {
		return nil
	}
	return &Subject{Container: name}
}
