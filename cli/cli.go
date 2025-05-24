package cli

import (
	"flag"
	"fmt"
	"github.com/elankath/minkapi/api"
	"github.com/spf13/pflag"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	"os"
	"runtime/debug"
)

// MainOpts is a struct that encapsulates target fields for CLI options parsing.
type MainOpts struct {
	api.MinKAPIConfig
}

func ParseProgramFlags(args []string) (*MainOpts, error) {
	flagSet, mainOpts := SetupFlagsToOpts()
	err := flagSet.Parse(args)
	if err != nil {
		return nil, err
	}
	err = ValidateMainOpts(mainOpts)
	if err != nil {
		return nil, err
	}
	return mainOpts, nil
}

func SetupFlagsToOpts() (*pflag.FlagSet, *MainOpts) {
	var mainOpts MainOpts
	flagSet := pflag.NewFlagSet(api.ProgramName, pflag.ContinueOnError)

	mainOpts.KubeConfigPath = os.Getenv(clientcmd.RecommendedConfigPathEnvVar)
	if mainOpts.KubeConfigPath == "" {
		mainOpts.KubeConfigPath = api.DefaultKubeConfigPath
	}
	flagSet.StringVarP(&mainOpts.KubeConfigPath, clientcmd.RecommendedConfigPathFlag, "k", mainOpts.KubeConfigPath, "path to generate kubeconfig - fallback to KUBECONFIG env-var or "+api.DefaultKubeConfigPath)
	//downloadFlags.StringVarP(&mainOpts.ControlKubeConfigPath, "kubeconfig-control", "c", os.Getenv("CONTROL_KUBECONFIG"), "kubeconfig path of shoot control plane (seed kubeconfig) - defaults to CONTROL_KUBECONFIG env-var")
	flagSet.StringVarP(&mainOpts.Host, "host", "H", api.DefaultHost, "host name to bind the MinKAPI service. Use 0.0.0.0 for all interfaces")
	flagSet.IntVarP(&mainOpts.Port, "port", "P", api.DefaultPort, "listen port for REST API")
	flagSet.IntVarP(&mainOpts.WatchQueueSize, "watch-queue-size", "s", api.DefaultWatchQueueSize, "max number of events to queue per watcher")
	flagSet.DurationVarP(&mainOpts.WatchTimeout, "watch-timeout", "t", api.DefaultWatchTimeout, "watch timeout after which connection is closed and watch removed")

	klogFlagSet := flag.NewFlagSet("klog", flag.ContinueOnError)
	klog.InitFlags(klogFlagSet)

	// Merge klog flags into pflag
	flagSet.AddGoFlagSet(klogFlagSet)

	return flagSet, &mainOpts
}

func ValidateMainOpts(opts *MainOpts) error {
	if opts.KubeConfigPath == "" {
		return fmt.Errorf("%w: --kubeconfig/-k flag is required", api.ErrMissingOpt)
	}
	return nil
}

func PrintVersion() {
	info, ok := debug.ReadBuildInfo()
	if ok {
		if info.Main.Version != "" {
			fmt.Printf("%s version: %s\n", api.ProgramName, info.Main.Version)
		}
	} else {
		fmt.Printf("%s: binary build info not embedded", api.ProgramName)
	}
}
