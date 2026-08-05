// Package report renders diagnostic results for humans (console) and
// machines (JSON).
package report

import (
	"fmt"
	"io"
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
		for i, root := range res.RootCauses {
			r.renderFinding(w, i+1, root)
			chain := diag.Chain(root)
			if len(chain) > 1 {
				fmt.Fprintf(w, "\n    %s\n", r.color(ansiBold, "Causal chain:"))
				for j, f := range chain {
					indent := strings.Repeat("  ", j)
					arrow := ""
					if j > 0 {
						arrow = r.color(ansiCyan, "→ ")
					}
					fmt.Fprintf(w, "      %s%s%s  %s\n", indent, arrow, f.Type, r.color(ansiDim, formatRef(f.Resource)))
				}
			}
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
		for _, f := range symptoms {
			fmt.Fprintf(w, "  - %s %s  %s\n", r.severity(f.Severity), f.Type, r.color(ansiDim, formatRef(f.Resource)))
			fmt.Fprintf(w, "    %s\n", f.Summary)
		}
	}

	if len(res.Standalone) > 0 {
		fmt.Fprintf(w, "\n%s %s\n", r.color(ansiBold, "Other findings"), r.color(ansiDim, "(no causal link established)"))
		for i, f := range res.Standalone {
			r.renderFinding(w, i+1, f)
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
