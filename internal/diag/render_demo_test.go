package diag_test

import (
	"os"
	"testing"

	"github.com/leventozen/kubectl-tuna/internal/report"
)

// TestRenderDemo prints the console rendering of a scenario; used to eyeball
// the report format. Run with: go test -run TestRenderDemo -v
func TestRenderDemo(t *testing.T) {
	if os.Getenv("TUNA_DEMO") == "" {
		t.Skip("set TUNA_DEMO=1 to render the demo output")
	}
	res := evaluateScenario(t, "broken-readiness-port", "service", "finance", "payment")
	// Keep the generated artifact aligned with the disposable Kind example
	// without duplicating a second set of diagnostic fixtures.
	res.Focus.Namespace = "tuna-demo"
	for _, finding := range res.Findings {
		if finding.Resource.Namespace != "" {
			finding.Resource.Namespace = "tuna-demo"
		}
	}
	for i := range res.Warnings {
		if res.Warnings[i].Resource.Namespace != "" {
			res.Warnings[i].Resource.Namespace = "tuna-demo"
		}
	}
	r := &report.ConsoleReporter{Out: os.Stdout, Color: false}
	r.Render(res)
}
