package diag_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/leventozen/kdiag/internal/diag"
)

const seedCorpusEvidenceClass = "synthetic-fixture"

type seedCorpusManifest struct {
	SchemaVersion int              `json:"schemaVersion"`
	Cases         []seedCorpusCase `json:"cases"`
}

type seedCorpusCase struct {
	ID                string                 `json:"id"`
	EvidenceClass     string                 `json:"evidenceClass"`
	Fixture           string                 `json:"fixture"`
	KubernetesVersion string                 `json:"kubernetesVersion"`
	Labels            []string               `json:"labels"`
	Limitations       []string               `json:"limitations"`
	Inspections       []seedCorpusInspection `json:"inspections"`
}

type seedCorpusInspection struct {
	Focus    seedCorpusFocus       `json:"focus"`
	Expected seedCorpusExpectation `json:"expected"`
}

type seedCorpusFocus struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

type seedCorpusExpectation struct {
	Health         diag.Health        `json:"health"`
	Partial        *bool              `json:"partial"`
	RootCauseTypes []diag.FindingType `json:"rootCauseTypes"`
}

func TestSeedCorpusExpectations(t *testing.T) {
	manifest := loadSeedCorpus(t)
	require.Equal(t, 1, manifest.SchemaVersion)
	require.NotEmpty(t, manifest.Cases)

	seenIDs := make(map[string]struct{}, len(manifest.Cases))
	inspectionCount := 0
	healthyControlCount := 0

	for _, corpusCase := range manifest.Cases {
		corpusCase := corpusCase
		t.Run(corpusCase.ID, func(t *testing.T) {
			validateSeedCorpusCase(t, corpusCase, seenIDs)

			for _, inspection := range corpusCase.Inspections {
				inspection := inspection
				inspectionCount++
				if inspection.Expected.Health == diag.HealthOK {
					healthyControlCount++
				}

				name := fmt.Sprintf("%s/%s/%s", inspection.Focus.Kind, inspection.Focus.Namespace, inspection.Focus.Name)
				t.Run(name, func(t *testing.T) {
					result := evaluateScenario(
						t,
						corpusCase.Fixture,
						inspection.Focus.Kind,
						inspection.Focus.Namespace,
						inspection.Focus.Name,
					)

					require.Equal(t, inspection.Expected.Health, result.Health)
					require.Equal(t, *inspection.Expected.Partial, result.Partial)
					require.Equal(t, sortedFindingTypes(inspection.Expected.RootCauseTypes), rootCauseTypes(result))
				})
			}
		})
	}

	t.Logf(
		"seed corpus: %d synthetic fixtures, %d inspections, %d healthy controls; this is not real-cluster validation",
		len(manifest.Cases), inspectionCount, healthyControlCount,
	)
}

func loadSeedCorpus(t *testing.T) seedCorpusManifest {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "corpus", "manifest.json"))
	require.NoError(t, err)

	var manifest seedCorpusManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(&manifest))
	var trailing any
	require.ErrorIs(t, decoder.Decode(&trailing), io.EOF, "manifest must contain one JSON document")
	return manifest
}

func validateSeedCorpusCase(t *testing.T, corpusCase seedCorpusCase, seenIDs map[string]struct{}) {
	t.Helper()

	require.NotEmpty(t, corpusCase.ID)
	_, duplicate := seenIDs[corpusCase.ID]
	require.False(t, duplicate, "duplicate corpus case id %q", corpusCase.ID)
	seenIDs[corpusCase.ID] = struct{}{}

	require.Equal(t, seedCorpusEvidenceClass, corpusCase.EvidenceClass,
		"seed fixtures must not be represented as real-cluster evidence")
	require.Equal(t, fixtureKubernetesVersion, corpusCase.KubernetesVersion,
		"manifest version must match the fake discovery version used by the fixture harness")
	require.NotEmpty(t, corpusCase.Labels)
	require.NotEmpty(t, corpusCase.Limitations)
	require.NotEmpty(t, corpusCase.Inspections)
	require.NotEmpty(t, corpusCase.Fixture)
	require.NotContains(t, corpusCase.Fixture, "..")
	require.False(t, strings.ContainsAny(corpusCase.Fixture, `/\\`))

	info, err := os.Stat(filepath.Join("..", "..", "testdata", corpusCase.Fixture))
	require.NoError(t, err)
	require.True(t, info.IsDir())

	for _, inspection := range corpusCase.Inspections {
		require.Contains(t, []string{"service", "deployment", "pod"}, inspection.Focus.Kind)
		require.NotEmpty(t, inspection.Focus.Namespace)
		require.NotEmpty(t, inspection.Focus.Name)
		require.Contains(t, []diag.Health{diag.HealthOK, diag.HealthDegraded, diag.HealthUnknown}, inspection.Expected.Health)
		require.NotNil(t, inspection.Expected.Partial)
		require.NotNil(t, inspection.Expected.RootCauseTypes)
		if inspection.Expected.Health == diag.HealthOK {
			require.Empty(t, inspection.Expected.RootCauseTypes,
				"a healthy control cannot declare a root cause")
		}
	}
}

func rootCauseTypes(result *diag.Result) []diag.FindingType {
	types := make([]diag.FindingType, 0, len(result.RootCauses))
	for _, root := range result.RootCauses {
		types = append(types, root.Type)
	}
	return sortedFindingTypes(types)
}

func sortedFindingTypes(types []diag.FindingType) []diag.FindingType {
	sorted := append([]diag.FindingType(nil), types...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted
}
