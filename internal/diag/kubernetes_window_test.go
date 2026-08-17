package diag

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
	utilversion "k8s.io/apimachinery/pkg/util/version"
)

type kubernetesWindowManifest struct {
	SchemaVersion      int                     `json:"schemaVersion"`
	ReviewedRuleWindow KubernetesCompatibility `json:"reviewedRuleWindow"`
	Entries            []kubernetesWindowEntry `json:"entries"`
}

type kubernetesWindowEntry struct {
	Minor               string `json:"minor"`
	UpstreamLatestPatch string `json:"upstreamLatestPatch"`
	TestedPatch         string `json:"testedPatch"`
	NodeImage           string `json:"nodeImage"`
	ImageLag            bool   `json:"imageLag"`
}

func TestKubernetesWindowManifestMatchesRulesAndE2EMatrix(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "compatibility", "kubernetes-window.json"))
	require.NoError(t, err)

	var manifest kubernetesWindowManifest
	require.NoError(t, json.Unmarshal(raw, &manifest))
	require.Equal(t, 1, manifest.SchemaVersion)
	require.NoError(t, manifest.ReviewedRuleWindow.validate())

	for _, registration := range DefaultRuleRegistrations() {
		require.Equal(t, manifest.ReviewedRuleWindow, registration.Metadata.Compatibility,
			"rule %s must match the centrally reviewed Kubernetes window", registration.Metadata.ID)
	}

	min := utilversion.MustParseMajorMinor(manifest.ReviewedRuleWindow.Min)
	max := utilversion.MustParseMajorMinor(manifest.ReviewedRuleWindow.Max)
	expectedCount := int(max.Minor()-min.Minor()) + 1
	require.Equal(t, expectedCount, len(manifest.Entries))

	imagePattern := regexp.MustCompile(`^kindest/node:v([0-9]+\.[0-9]+\.[0-9]+)@sha256:[0-9a-f]{64}$`)
	seen := make(map[string]struct{}, len(manifest.Entries))
	for i, entry := range manifest.Entries {
		expectedMinor := fmt.Sprintf("%d.%d", min.Major(), int(min.Minor())+i)
		require.Equal(t, expectedMinor, entry.Minor, "entries must be unique, sorted, and contiguous")
		_, duplicate := seen[entry.Minor]
		require.False(t, duplicate, "duplicate Kubernetes minor %s", entry.Minor)
		seen[entry.Minor] = struct{}{}

		tested, err := utilversion.ParseSemantic(entry.TestedPatch)
		require.NoError(t, err, "invalid tested patch for Kubernetes %s", entry.Minor)
		upstream, err := utilversion.ParseSemantic(entry.UpstreamLatestPatch)
		require.NoError(t, err, "invalid upstream patch for Kubernetes %s", entry.Minor)
		require.Equal(t, entry.Minor, fmt.Sprintf("%d.%d", tested.Major(), tested.Minor()))
		require.Equal(t, entry.Minor, fmt.Sprintf("%d.%d", upstream.Major(), upstream.Minor()))
		require.False(t, upstream.LessThan(tested), "tested patch cannot exceed the recorded upstream patch")

		match := imagePattern.FindStringSubmatch(entry.NodeImage)
		require.Len(t, match, 2, "node image must be an exact digest-pinned kindest/node reference")
		require.Equal(t, entry.TestedPatch, match[1], "node image tag must match testedPatch")
		require.Equal(t, entry.TestedPatch != entry.UpstreamLatestPatch, entry.ImageLag,
			"imageLag must describe the recorded patch difference")
	}
}
