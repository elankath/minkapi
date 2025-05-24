package cli

const (
	ExitSuccess int = iota
	ExitErrParseOpts

	ExitErrShutdown = 254
	ExitGeneral     = 255
)
