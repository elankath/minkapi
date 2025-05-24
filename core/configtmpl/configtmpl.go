package configtmpl

import (
	"bytes"
	"embed"
	"fmt"
	"github.com/elankath/minkapi/api"
	"os"
	"text/template"
)

//go:embed *.yaml
var content embed.FS

var (
	kubeConfigTemplate              *template.Template
	kubeSchedulerConfigTemplate     *template.Template
	kubeSchedulerConfigTemplatePath string = "templates/kube-scheduler-config.yaml"
)

func LoadKubeConfigTemplate() error {
	var err error
	var data []byte

	if kubeConfigTemplate != nil {
		return nil
	}

	data, err = content.ReadFile("kubeconfig.yaml")
	if err != nil {
		return fmt.Errorf("%w: cannot read kubeconfig.yaml from content FS: %w", api.ErrLoadConfigTemplate, err)
	}
	kubeConfigTemplate, err = template.New("kubeconfig").Parse(string(data))
	if err != nil {
		return fmt.Errorf("%w: cannot parse kubeconfig.yaml template: %w", api.ErrLoadConfigTemplate, err)
	}
	return nil
}

type KubeSchedulerTmplParams struct {
	KubeConfigPath string
	QPS            float32
	Burst          int
}

type KubeConfigParams struct {
	KubeConfigPath string
	URL            string
}

func GenKubeConfig(params KubeConfigParams) error {
	err := LoadKubeConfigTemplate()
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	err = kubeConfigTemplate.Execute(&buf, params)
	if err != nil {
		return fmt.Errorf("%w: cannot render kubeconfig.yaml template with params %q: %w", api.ErrLoadConfigTemplate, params, err)
	}
	err = os.WriteFile(params.KubeConfigPath, buf.Bytes(), 0644)
	if err != nil {
		return fmt.Errorf("cannot write kubeconfig to %q: %w", params.KubeConfigPath, err)
	}
	return nil
}
