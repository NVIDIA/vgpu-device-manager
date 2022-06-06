package v1

import (
	"encoding/json"
	"fmt"
)

// Version indicates the version of the 'Spec' struct used to hold information on 'VGPUConfigs'.
const Version = "v1"

// Spec is a versioned struct used to hold information on 'VGPUConfigs'.
type Spec struct {
	Version     string              `json:"version" yaml:"version"`
	VGPUConfigs map[string][]string `json:"vgpu-configs,omitempty" yaml:"vgpu-configs,omitempty"`
}

// UnmarshalJSON unmarshals raw bytes into a versioned 'Spec'.
func (s *Spec) UnmarshalJSON(b []byte) error {
	spec := make(map[string]json.RawMessage)
	err := json.Unmarshal(b, &spec)
	if err != nil {
		return err
	}

	if !containsKey(spec, "version") && len(spec) > 0 {
		return fmt.Errorf("unable to parse with missing 'version' field")
	}

	result := Spec{}
	for k, v := range spec {
		switch k {
		case "version":
			var version string
			err = json.Unmarshal(v, &version)
			if err != nil {
				return err
			}
			if version != Version {
				return fmt.Errorf("unknown version: %v", version)
			}
			result.Version = version
		case "vgpu-configs":
			configs := map[string][]string{}
			err := json.Unmarshal(v, &configs)
			if err != nil {
				return err
			}
			if len(configs) == 0 {
				return fmt.Errorf("at least one entry in '%v' is required", k)
			}
			for c, s := range configs {
				if len(s) == 0 {
					return fmt.Errorf("at least one entry in '%v' is required", c)
				}
			}
			result.VGPUConfigs = configs
		default:
			return fmt.Errorf("unexpected field: %v", k)
		}
	}

	*s = result
	return nil
}

func containsKey(m map[string]json.RawMessage, s string) bool {
	_, exists := m[s]
	return exists
}
