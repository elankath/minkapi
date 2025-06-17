package objutil

import (
	"fmt"
	"os"

	"sigs.k8s.io/yaml"
)

func LoadYamlIntoObj(path string, obj any) (err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		err = fmt.Errorf("failed to read %q: %w", path, err)
		return
	}
	err = yaml.Unmarshal(data, obj)
	if err != nil {
		err = fmt.Errorf("failed to unmarshal object in %q: %w", path, err)
		return
	}
	return
}
