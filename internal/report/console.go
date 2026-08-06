// Package report renders diagnostic results for humans (console) and
// machines (JSON).
package report

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/leventozen/kdiag/internal/diag"
	"github.com/leventozen/kdiag/internal/graph"
)

const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiRed    = "\033[31m"
	ansiYellow = "\033[33m"
	ansiGreen  = "\033[32m"
	ansiCyan   = "\033[36m"
	ansiDim    = "\033[2m"
)

type ConsoleReporter struct {
	Out   io.Writer
	Color bool
}

func (r *ConsoleReporter) color(code, s string) string {
	if !r.Color {
		return s
	}
	return code + s + ansiReset
}

func (r *ConsoleReporter) severity(s diag.Severity) string {
	label := strings.ToUpper(string(s))
	switch s {
	case diag.SeverityCritical:
		return r.color(ansiRed+ansiBold, label)
	case diag.SeverityWarning:
		return r.color(ansiYellow, label)
	default:
		return r.color(ansiDim, label)
	}
}

// Render writes a human-readable diagnosis: health, root causes with their
// causal chains and evidence, then standalone findings.
func (r *ConsoleReporter) Render(res *diag.Result) {
	w := r.Out

	fmt.Fprintln(w)
	r.writeFocus(w, res.Focus)
	if res.Cluster.KubernetesVersion != "" {
		fmt.Fprintf(w, "Kubernetes: %s\n", res.Cluster.KubernetesVersion)
	}
	switch res.Health {
	case diag.HealthOK:
		fmt.Fprintf(w, "Health:    %s\n", r.color(ansiGreen+ansiBold, "HEALTHY"))
	case diag.HealthUnknown:
		fmt.Fprintf(w, "Health:    %s\n", r.color(ansiYellow+ansiBold, "UNKNOWN"))
	default:
		fmt.Fprintf(w, "Health:    %s\n", r.color(ansiRed+ansiBold, "DEGRADED"))
	}

	if len(res.Warnings) > 0 {
		fmt.Fprintf(w, "\n%s %s\n", r.color(ansiBold, "Incomplete evidence"), r.color(ansiDim, "(no conclusion was inferred from unavailable data)"))
		for _, warning := range res.Warnings {
			fmt.Fprintf(w, "  - %s %s: %s\n", warning.Source, r.color(ansiDim, formatRef(warning.Resource)), warning.Message)
		}
	}
	if len(res.Rules.Skipped) > 0 {
		fmt.Fprintf(w, "\n%s %s\n", r.color(ansiBold, "Skipped rules"), r.color(ansiDim, "(Kubernetes compatibility not established)"))
		for _, skipped := range res.Rules.Skipped {
			fmt.Fprintf(w, "  - %s: %s\n", skipped.ID, skipped.Reason)
		}
	}
	if res.Rules.SuspendedReason != "" {
		fmt.Fprintf(w, "\n%s %s\n", r.color(ansiBold, "Rule evaluation suspended"), r.color(ansiDim, "(temporal integrity not established)"))
		fmt.Fprintf(w, "  - %s\n", res.Rules.SuspendedReason)
	}

	if len(res.Findings) == 0 {
		if res.Health == diag.HealthOK {
			fmt.Fprintf(w, "\nNo current problems were found by the evaluated rules.\n\n")
		} else {
			fmt.Fprintf(w, "\nHealth could not be established because required evidence is unavailable.\n\n")
		}
		return
	}

	if len(res.RootCauses) > 0 {
		fmt.Fprintf(w, "\n%s\n", r.color(ansiBold, "Root cause candidates"))
		for i, group := range groupReplicaFindings(res.RootCauses, res.ReplicaOwners) {
			if len(group.Findings) == 1 {
				r.renderFinding(w, i+1, group.Findings[0])
				r.renderCausalChain(w, group.Findings[0])
				continue
			}
			r.renderReplicaGroup(w, i+1, group, true)
		}
	}

	// Symptoms explained by a root cause: list compactly.
	var symptoms []*diag.Finding
	for _, f := range res.Findings {
		if f.IsSymptom() {
			symptoms = append(symptoms, f)
		}
	}
	if len(symptoms) > 0 {
		fmt.Fprintf(w, "\n%s %s\n", r.color(ansiBold, "Propagated symptoms"), r.color(ansiDim, "(explained by the causes above)"))
		for _, group := range groupReplicaFindings(symptoms, res.ReplicaOwners) {
			if len(group.Findings) == 1 {
				f := group.Findings[0]
				fmt.Fprintf(w, "  - %s %s  %s\n", r.severity(f.Severity), f.Type, r.color(ansiDim, formatRef(f.Resource)))
				fmt.Fprintf(w, "    %s\n", f.Summary)
				continue
			}
			r.renderCompactReplicaGroup(w, group)
		}
	}

	if len(res.Standalone) > 0 {
		fmt.Fprintf(w, "\n%s %s\n", r.color(ansiBold, "Other findings"), r.color(ansiDim, "(no causal link established)"))
		for i, group := range groupReplicaFindings(res.Standalone, res.ReplicaOwners) {
			if len(group.Findings) == 1 {
				r.renderFinding(w, i+1, group.Findings[0])
				continue
			}
			r.renderReplicaGroup(w, i+1, group, false)
		}
	}
	fmt.Fprintln(w)
}

func (r *ConsoleReporter) writeFocus(w io.Writer, ref graph.ResourceRef) {
	fmt.Fprintf(w, "Kind:      %s\n", r.color(ansiBold, ref.Kind))
	if ref.Namespace != "" {
		fmt.Fprintf(w, "Namespace: %s\n", ref.Namespace)
	}
	fmt.Fprintf(w, "Name:      %s\n", ref.Name)
}

func formatRef(ref graph.ResourceRef) string {
	if ref.Namespace == "" {
		return fmt.Sprintf("%s/%s", ref.Kind, ref.Name)
	}
	return fmt.Sprintf("%s/%s (%s)", ref.Kind, ref.Name, ref.Namespace)
}

func (r *ConsoleReporter) renderFinding(w io.Writer, idx int, f *diag.Finding) {
	fmt.Fprintf(w, "\n  [%d] %s %s  %s\n", idx, r.severity(f.Severity), r.color(ansiBold, string(f.Type)), r.color(ansiDim, "(confidence: "+string(f.Confidence)+", impact: "+string(f.Impact)+")"))
	fmt.Fprintf(w, "      %s\n", r.color(ansiDim, formatRef(f.Resource)))
	if f.Subject != nil && f.Subject.Container != "" {
		fmt.Fprintf(w, "      %s\n", r.color(ansiDim, "container/"+f.Subject.Container))
	}
	fmt.Fprintf(w, "      %s\n", f.Summary)
	if f.Detail != "" {
		fmt.Fprintf(w, "      %s\n", r.color(ansiDim, f.Detail))
	}
	if len(f.Evidence) > 0 {
		fmt.Fprintf(w, "\n    %s\n", r.color(ansiBold, "Evidence:"))
		for _, ev := range f.Evidence {
			fmt.Fprintf(w, "      - %s: %s\n", r.color(ansiCyan, ev.Source), ev.Value)
		}
	}
	if len(f.Recommendations) > 0 {
		fmt.Fprintf(w, "\n    %s\n", r.color(ansiBold, "Recommendations:"))
		for _, rec := range f.Recommendations {
			fmt.Fprintf(w, "      - %s\n", rec)
		}
	}
}

type replicaFindingGroup struct {
	Owner    graph.ResourceRef
	Findings []*diag.Finding
}

func groupReplicaFindings(findings []*diag.Finding, owners map[string]graph.ResourceRef) []replicaFindingGroup {
	groups := make([]replicaFindingGroup, 0, len(findings))
	indexes := make(map[string]int)
	for _, finding := range findings {
		owner, ok := owners[finding.ID]
		if !ok {
			groups = append(groups, replicaFindingGroup{Findings: []*diag.Finding{finding}})
			continue
		}
		key := replicaGroupKey(finding, owner)
		if index, exists := indexes[key]; exists {
			groups[index].Findings = append(groups[index].Findings, finding)
			continue
		}
		indexes[key] = len(groups)
		groups = append(groups, replicaFindingGroup{Owner: owner, Findings: []*diag.Finding{finding}})
	}
	return groups
}

func replicaGroupKey(finding *diag.Finding, owner graph.ResourceRef) string {
	container := ""
	if finding.Subject != nil {
		container = finding.Subject.Container
	}
	return strings.Join([]string{
		owner.String(), finding.RuleID, string(finding.Type), string(finding.Severity),
		string(finding.Confidence), string(finding.Impact), container,
		causalTypeSignature(finding.CausedBy), causalTypeSignature(finding.Causes),
	}, "\x00")
}

func causalTypeSignature(findings []*diag.Finding) string {
	types := make([]string, 0, len(findings))
	for _, finding := range findings {
		types = append(types, string(finding.Type))
	}
	sort.Strings(types)
	return strings.Join(types, ",")
}

func (r *ConsoleReporter) renderReplicaGroup(w io.Writer, idx int, group replicaFindingGroup, withChains bool) {
	representative := group.Findings[0]
	fmt.Fprintf(w, "\n  [%d] %s %s × %d replicas  %s\n",
		idx,
		r.severity(representative.Severity),
		r.color(ansiBold, string(representative.Type)),
		len(group.Findings),
		r.color(ansiDim, "(confidence: "+string(representative.Confidence)+", impact: "+string(representative.Impact)+")"),
	)
	fmt.Fprintf(w, "      %s\n", r.color(ansiDim, "owner "+formatRef(group.Owner)))
	fmt.Fprintf(w, "\n    %s\n", r.color(ansiBold, "Affected replicas and evidence:"))
	for _, finding := range group.Findings {
		fmt.Fprintf(w, "      - %s", r.color(ansiDim, formatRef(finding.Resource)))
		if finding.Subject != nil && finding.Subject.Container != "" {
			fmt.Fprintf(w, " %s", r.color(ansiDim, "container/"+finding.Subject.Container))
		}
		fmt.Fprintln(w)
		fmt.Fprintf(w, "        %s\n", finding.Summary)
		if finding.Detail != "" {
			fmt.Fprintf(w, "        %s\n", r.color(ansiDim, finding.Detail))
		}
		for _, evidence := range finding.Evidence {
			fmt.Fprintf(w, "        - %s: %s\n", r.color(ansiCyan, evidence.Source), evidence.Value)
		}
	}

	recommendations := uniqueRecommendations(group.Findings)
	if len(recommendations) > 0 {
		fmt.Fprintf(w, "\n    %s\n", r.color(ansiBold, "Recommendations:"))
		for _, recommendation := range recommendations {
			fmt.Fprintf(w, "      - %s\n", recommendation)
		}
	}
	if withChains {
		fmt.Fprintf(w, "\n    %s\n", r.color(ansiBold, "Causal chains by replica:"))
		for _, finding := range group.Findings {
			chain := diag.Chain(finding)
			parts := make([]string, 0, len(chain))
			for _, member := range chain {
				parts = append(parts, fmt.Sprintf("%s %s", member.Type, formatRef(member.Resource)))
			}
			fmt.Fprintf(w, "      - %s: %s\n",
				r.color(ansiDim, formatRef(finding.Resource)),
				strings.Join(parts, r.color(ansiCyan, " → ")),
			)
		}
	}
}

func (r *ConsoleReporter) renderCompactReplicaGroup(w io.Writer, group replicaFindingGroup) {
	representative := group.Findings[0]
	fmt.Fprintf(w, "  - %s %s × %d replicas  %s\n",
		r.severity(representative.Severity), representative.Type, len(group.Findings),
		r.color(ansiDim, "owner "+formatRef(group.Owner)),
	)
	for _, finding := range group.Findings {
		fmt.Fprintf(w, "    - %s: %s\n", r.color(ansiDim, formatRef(finding.Resource)), finding.Summary)
	}
}

func (r *ConsoleReporter) renderCausalChain(w io.Writer, root *diag.Finding) {
	chain := diag.Chain(root)
	if len(chain) <= 1 {
		return
	}
	fmt.Fprintf(w, "\n    %s\n", r.color(ansiBold, "Causal chain:"))
	for i, finding := range chain {
		indent := strings.Repeat("  ", i)
		arrow := ""
		if i > 0 {
			arrow = r.color(ansiCyan, "→ ")
		}
		fmt.Fprintf(w, "      %s%s%s  %s\n", indent, arrow, finding.Type, r.color(ansiDim, formatRef(finding.Resource)))
	}
}

func uniqueRecommendations(findings []*diag.Finding) []string {
	seen := make(map[string]struct{})
	var recommendations []string
	for _, finding := range findings {
		for _, recommendation := range finding.Recommendations {
			if _, exists := seen[recommendation]; exists {
				continue
			}
			seen[recommendation] = struct{}{}
			recommendations = append(recommendations, recommendation)
		}
	}
	return recommendations
}
