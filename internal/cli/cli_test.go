package cli

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/leventozen/kdiag/internal/diag"
)

func TestExitErrorForHealth(t *testing.T) {
	tests := []struct {
		name   string
		health diag.Health
		code   int
		hasErr bool
	}{
		{name: "healthy", health: diag.HealthOK},
		{name: "degraded", health: diag.HealthDegraded, code: 2, hasErr: true},
		{name: "unknown", health: diag.HealthUnknown, code: 1, hasErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := exitErrorForHealth(tt.health)
			if !tt.hasErr {
				require.NoError(t, err)
				return
			}
			code, ok := ExitCode(err)
			require.True(t, ok)
			require.Equal(t, tt.code, code)
		})
	}
}

func TestExitCodeRejectsOperationalError(t *testing.T) {
	_, ok := ExitCode(errors.New("API unavailable"))
	require.False(t, ok)
}
