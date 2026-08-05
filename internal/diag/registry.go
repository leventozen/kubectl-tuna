package diag

import (
	"fmt"
	"sort"

	utilversion "k8s.io/apimachinery/pkg/util/version"

	"github.com/leventozen/kdiag/internal/graph"
)

// RuleFamily groups rules for documentation and future rule-pack discovery.
// It is descriptive metadata, not a causal relationship.
type RuleFamily string

const (
	RuleFamilyTraffic    RuleFamily = "traffic"
	RuleFamilyWorkload   RuleFamily = "workload"
	RuleFamilyScheduling RuleFamily = "scheduling"
	RuleFamilyNode       RuleFamily = "node"
	RuleFamilyRollout    RuleFamily = "rollout"
)

// KubernetesCompatibility is the inclusive Kubernetes minor-version window
// whose semantics have been reviewed for a rule. It is deliberately explicit:
// a newly released minor is not assumed compatible until it is reviewed.
type KubernetesCompatibility struct {
	Min string `json:"min"`
	Max string `json:"max"`
}

func (c KubernetesCompatibility) validate() error {
	min, err := utilversion.ParseMajorMinor(c.Min)
	if err != nil {
		return fmt.Errorf("invalid minimum Kubernetes version %q: %w", c.Min, err)
	}
	max, err := utilversion.ParseMajorMinor(c.Max)
	if err != nil {
		return fmt.Errorf("invalid maximum Kubernetes version %q: %w", c.Max, err)
	}
	if max.LessThan(min) {
		return fmt.Errorf("maximum Kubernetes version %s is lower than minimum %s", c.Max, c.Min)
	}
	return nil
}

func (c KubernetesCompatibility) supports(cluster graph.ClusterInfo) bool {
	want := utilversion.MajorMinor(cluster.Major, cluster.Minor)
	min := utilversion.MustParseMajorMinor(c.Min)
	max := utilversion.MustParseMajorMinor(c.Max)
	return !want.LessThan(min) && !max.LessThan(want)
}

// RuleMetadata is the part of a rule registration that can be documented and
// checked without executing the rule. This is an internal pre-release
// contract; it is not yet a stable third-party plugin API.
type RuleMetadata struct {
	ID            string                  `json:"id"`
	Family        RuleFamily              `json:"family"`
	Description   string                  `json:"description"`
	FindingTypes  []FindingType           `json:"findingTypes"`
	Compatibility KubernetesCompatibility `json:"kubernetes"`
}

// RuleRegistration binds executable rule logic to reviewable metadata.
type RuleRegistration struct {
	Metadata RuleMetadata
	Rule     Rule
}

// Registry is an immutable, validated collection of rule registrations.
// Validation at construction time prevents duplicate identities and rules
// without an explicit Kubernetes compatibility decision.
type Registry struct {
	registrations []RuleRegistration
}

func NewRegistry(registrations []RuleRegistration) (*Registry, error) {
	if len(registrations) == 0 {
		return nil, fmt.Errorf("rule registry is empty")
	}
	seen := make(map[string]struct{}, len(registrations))
	findingOwners := make(map[FindingType]string)
	copyOf := append([]RuleRegistration(nil), registrations...)
	for i, registration := range copyOf {
		copyOf[i].Metadata.FindingTypes = append([]FindingType(nil), registration.Metadata.FindingTypes...)
		registration = copyOf[i]
		if registration.Rule == nil {
			return nil, fmt.Errorf("rule registration %d has no implementation", i)
		}
		metadata := registration.Metadata
		if metadata.ID == "" {
			return nil, fmt.Errorf("rule registration %d has no ID", i)
		}
		if registration.Rule.ID() != metadata.ID {
			return nil, fmt.Errorf("rule metadata ID %q does not match implementation ID %q", metadata.ID, registration.Rule.ID())
		}
		if _, exists := seen[metadata.ID]; exists {
			return nil, fmt.Errorf("duplicate rule ID %q", metadata.ID)
		}
		seen[metadata.ID] = struct{}{}
		if !knownRuleFamily(metadata.Family) {
			return nil, fmt.Errorf("rule %q has unknown family %q", metadata.ID, metadata.Family)
		}
		if metadata.Description == "" {
			return nil, fmt.Errorf("rule %q has no description", metadata.ID)
		}
		if len(metadata.FindingTypes) == 0 {
			return nil, fmt.Errorf("rule %q declares no finding types", metadata.ID)
		}
		for _, findingType := range metadata.FindingTypes {
			if findingType == "" {
				return nil, fmt.Errorf("rule %q declares an empty finding type", metadata.ID)
			}
			if owner, exists := findingOwners[findingType]; exists {
				return nil, fmt.Errorf("finding type %q is declared by both %q and %q", findingType, owner, metadata.ID)
			}
			findingOwners[findingType] = metadata.ID
		}
		if err := metadata.Compatibility.validate(); err != nil {
			return nil, fmt.Errorf("rule %q: %w", metadata.ID, err)
		}
	}
	return &Registry{registrations: copyOf}, nil
}

func knownRuleFamily(family RuleFamily) bool {
	switch family {
	case RuleFamilyTraffic, RuleFamilyWorkload, RuleFamilyScheduling, RuleFamilyNode, RuleFamilyRollout:
		return true
	default:
		return false
	}
}

func (m RuleMetadata) declares(findingType FindingType) bool {
	for _, declared := range m.FindingTypes {
		if declared == findingType {
			return true
		}
	}
	return false
}

func mustRegistry(registrations []RuleRegistration) *Registry {
	registry, err := NewRegistry(registrations)
	if err != nil {
		panic(err)
	}
	return registry
}

func (r *Registry) Registrations() []RuleRegistration {
	if r == nil {
		return nil
	}
	out := append([]RuleRegistration(nil), r.registrations...)
	for i := range out {
		out[i].Metadata.FindingTypes = append([]FindingType(nil), out[i].Metadata.FindingTypes...)
	}
	return out
}

// Metadata returns a deterministic copy suitable for documentation and
// discovery commands without exposing mutable registry state.
func (r *Registry) Metadata() []RuleMetadata {
	out := make([]RuleMetadata, 0, len(r.registrations))
	for _, registration := range r.registrations {
		metadata := registration.Metadata
		metadata.FindingTypes = append([]FindingType(nil), metadata.FindingTypes...)
		out = append(out, metadata)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
