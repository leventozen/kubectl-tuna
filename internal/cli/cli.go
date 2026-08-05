// Package cli wires kdiag's cobra commands.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/leventozen/kdiag/internal/diag"
	"github.com/leventozen/kdiag/internal/graph"
	"github.com/leventozen/kdiag/internal/kube"
	"github.com/leventozen/kdiag/internal/report"
)

type options struct {
	kubeconfig  string
	kubeContext string
	namespace   string
	output      string
	noColor     bool
	timeout     time.Duration
}

// ExitError carries an intentional process status after output has already
// been rendered. It is distinct from an operational error, which main prints.
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// ExitCode extracts an intentional CLI status from err.
func ExitCode(err error) (int, bool) {
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code, true
	}
	return 0, false
}

// NewRootCommand builds the kdiag command tree.
// Invoked as kubectl-diag (krew / PATH plugin) the usage string becomes
// "kubectl diag"; standalone installs keep "kdiag".
func NewRootCommand(version string) *cobra.Command {
	opts := &options{}
	use := commandUse()

	root := &cobra.Command{
		Use:     use,
		Version: version,
		Short:   "Evidence-based Kubernetes diagnostics with causal chains",
		Long: `kdiag inspects a Kubernetes resource, discovers its relationships
(Pods, ReplicaSets, EndpointSlices, ConfigMaps, events, ...), evaluates
deterministic diagnostic rules over the resulting graph, and correlates
the findings into causal chains that separate root causes from
propagated symptoms.

Run standalone, or install the locally built binary as a kubectl PATH plugin:

  kdiag inspect service NAME -n NAMESPACE
  kubectl diag inspect service NAME -n NAMESPACE`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&opts.kubeconfig, "kubeconfig", "", "path to the kubeconfig file (default: standard loading rules)")
	root.PersistentFlags().StringVar(&opts.kubeContext, "context", "", "kubeconfig context to use")
	root.PersistentFlags().StringVarP(&opts.namespace, "namespace", "n", "", "namespace of the resource (default: current kubeconfig namespace)")
	root.PersistentFlags().StringVarP(&opts.output, "output", "o", "console", "output format: console | json")
	root.PersistentFlags().BoolVar(&opts.noColor, "no-color", false, "disable colored output")
	root.PersistentFlags().DurationVar(&opts.timeout, "timeout", 30*time.Second, "timeout for the whole inspection")

	inspect := &cobra.Command{
		Use:   "inspect",
		Short: "Inspect a resource and diagnose why it is degraded",
	}
	inspect.AddCommand(
		newInspectKindCommand(opts, "service", "Inspect a Service and its traffic path"),
		newInspectKindCommand(opts, "deployment", "Inspect a Deployment and its workload lifecycle"),
		newInspectKindCommand(opts, "pod", "Inspect a Pod, its owners, node, and the Services selecting it"),
	)
	root.AddCommand(inspect)
	return root
}

func newInspectKindCommand(opts *options, kind, short string) *cobra.Command {
	return &cobra.Command{
		Use:   kind + " NAME",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInspect(opts, kind, args[0])
		},
	}
}

func runInspect(opts *options, kind, name string) error {
	client, defaultNS, err := kube.NewClient(opts.kubeconfig, opts.kubeContext)
	if err != nil {
		return err
	}
	namespace := opts.namespace
	if namespace == "" {
		namespace = defaultNS
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()

	collector := kube.NewCollector(client)
	graphResult, err := collect(ctx, collector, kind, namespace, name)
	if err != nil {
		return err
	}

	result := diag.NewEngine().Evaluate(graphResult)

	switch opts.output {
	case "json":
		if err := report.RenderJSON(os.Stdout, result); err != nil {
			return err
		}
	case "console":
		r := &report.ConsoleReporter{
			Out:   os.Stdout,
			Color: !opts.noColor && term.IsTerminal(int(os.Stdout.Fd())),
		}
		r.Render(result)
	default:
		return fmt.Errorf("unknown output format %q (expected console or json)", opts.output)
	}

	return exitErrorForHealth(result.Health)
}

func exitErrorForHealth(health diag.Health) error {
	switch health {
	case diag.HealthDegraded:
		return &ExitError{Code: 2}
	case diag.HealthUnknown:
		return &ExitError{Code: 1}
	default:
		return nil
	}
}

func collect(ctx context.Context, c *kube.Collector, kind, namespace, name string) (*graph.Graph, error) {
	switch kind {
	case "service":
		return c.CollectService(ctx, namespace, name)
	case "deployment":
		return c.CollectDeployment(ctx, namespace, name)
	case "pod":
		return c.CollectPod(ctx, namespace, name)
	default:
		return nil, fmt.Errorf("unsupported kind %q", kind)
	}
}

// commandUse returns the cobra Use string based on how the binary was named.
// Releases ship as kubectl-diag (invoked via `kubectl diag`); go install
// keeps the standalone kdiag name. Cobra Use must not contain spaces.
func commandUse() string {
	base := filepath.Base(os.Args[0])
	base = strings.TrimSuffix(base, ".exe")
	if strings.Contains(base, "kubectl-diag") {
		return "kubectl-diag"
	}
	return "kdiag"
}
