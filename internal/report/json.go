package report

import (
	"encoding/json"
	"io"

	"github.com/leventozen/kdiag/internal/diag"
	"github.com/leventozen/kdiag/internal/graph"
)

// jsonFinding mirrors diag.Finding with causal links flattened to IDs so the
// output is cycle-free and easy to consume from scripts and CI.
type jsonFinding struct {
	ID              string            `json:"id"`
	RuleID          string            `json:"ruleId"`
	Type            diag.FindingType  `json:"type"`
	Severity        diag.Severity     `json:"severity"`
	Confidence      diag.Confidence   `json:"confidence"`
	Impact          diag.Impact       `json:"impact"`
	Resource        graph.ResourceRef `json:"resource"`
	Subject         *diag.Subject     `json:"subject,omitempty"`
	Summary         string            `json:"summary"`
	Detail          string            `json:"detail,omitempty"`
	Evidence        []diag.Evidence   `json:"evidence,omitempty"`
	Recommendations []string          `json:"recommendations,omitempty"`
	Causes          []string          `json:"causes,omitempty"`
	CausedBy        []string          `json:"causedBy,omitempty"`
	RootCause       bool              `json:"rootCause"`
}

type jsonResult struct {
	Focus      graph.ResourceRef          `json:"focus"`
	Cluster    graph.ClusterInfo          `json:"cluster"`
	Collection *graph.CollectionInfo      `json:"collection,omitempty"`
	Health     diag.Health                `json:"health"`
	Partial    bool                       `json:"partial"`
	Warnings   []graph.CollectionIssue    `json:"warnings,omitempty"`
	RootCauses []string                   `json:"rootCauses"`
	Findings   []jsonFinding              `json:"findings"`
	Rules      diag.RuleEvaluationSummary `json:"rules"`
}

// RenderJSON writes the result as indented JSON.
func RenderJSON(w io.Writer, res *diag.Result) error {
	out := jsonResult{
		Focus:      res.Focus,
		Cluster:    res.Cluster,
		Collection: res.Collection,
		Health:     res.Health,
		Partial:    res.Partial,
		Warnings:   res.Warnings,
		RootCauses: findingIDs(res.RootCauses),
		Findings:   make([]jsonFinding, 0, len(res.Findings)),
		Rules:      res.Rules,
	}
	for _, f := range res.Findings {
		out.Findings = append(out.Findings, jsonFinding{
			ID:              f.ID,
			RuleID:          f.RuleID,
			Type:            f.Type,
			Severity:        f.Severity,
			Confidence:      f.Confidence,
			Impact:          f.Impact,
			Resource:        f.Resource,
			Subject:         f.Subject,
			Summary:         f.Summary,
			Detail:          f.Detail,
			Evidence:        f.Evidence,
			Recommendations: f.Recommendations,
			Causes:          findingIDs(f.Causes),
			CausedBy:        findingIDs(f.CausedBy),
			RootCause:       f.IsRootCandidate(),
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func findingIDs(fs []*diag.Finding) []string {
	ids := make([]string, 0, len(fs))
	for _, f := range fs {
		ids = append(ids, f.ID)
	}
	return ids
}
