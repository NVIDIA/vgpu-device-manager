package v1

import (
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
	"testing"
)

func TestSpec(t *testing.T) {
	testCases := []struct {
		Description     string
		Spec            string
		expectedFailure bool
	}{
		{
			"Empty",
			"",
			false,
		},
		{
			"Only version field",
			`{
				"version": "v1"
			}`,
			false,
		},
		{
			"Well formed",
			`{
				"version": "v1",
				"vgpu-configs": {
					"default": [
						"A100-4C",
						"A30-4C",
						"T4-1Q",
					]
				}
			}`,
			false,
		},
		{
			"Well formed - multiple 'vgpu-configs'",
			`{
				"version": "v1",
				"vgpu-configs": {
					"default": [
						"A100-4C",
						"A30-4C",
						"T4-1Q",
					],
					"a100-full-profile": [
						"A100-24C",
					]
				}
			}`,
			false,
		},
		{
			"Well formed - wrong version",
			`{
				"version": "v2",
				"vgpu-configs": {
					"default": [
						"A100-4C",
						"A30-4C",
						"T4-1Q",
					]
				}
			}`,
			true,
		},
		{
			"Missing version",
			`{
				"vgpu-configs": {
					"default": [
						"A100-4C",
						"A30-4C",
						"T4-1Q",
					]
				}
			}`,
			true,
		},
		{
			"Erroneous field",
			`{
				"bogus": "field",
				"version": "v1",
				"vgpu-configs": {
					"default": [
						"A100-4C",
						"A30-4C",
						"T4-1Q",
					]
				}
			}`,
			true,
		},
		{
			"Empty 'vgu-configs'",
			`{
				"version": "v1",
				"vgpu-configs": {}
			}`,
			true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Description, func(t *testing.T) {
			s := Spec{}
			err := yaml.Unmarshal([]byte(tc.Spec), &s)
			if tc.expectedFailure {
				require.NotNil(t, err, "Unexpected success yaml.Unmarshal")
			} else {
				require.Nil(t, err, "Unexpected failure yaml.Unmarshal")
			}
		})
	}
}
