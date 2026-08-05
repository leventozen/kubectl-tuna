package diag_test

import (
	"os"
	"testing"

	"github.com/leventozen/kdiag/internal/report"
)

// TestRenderDemo prints the console rendering of a scenario; used to eyeball
// the report format. Run with: go test -run TestRenderDemo -v
func TestRenderDemo(t *testing.T) {
	if os.Getenv("KDIAG_DEMO") == "" {
		t.Skip("set KDIAG_DEMO=1 to render the demo output")
	}
	res := evaluateScenario(t, "broken-readiness-port", "service", "finance", "payment")
	r := &report.ConsoleReporter{Out: os.Stdout, Color: false}
	r.Render(res)
}
