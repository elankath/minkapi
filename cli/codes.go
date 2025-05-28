package cli

const (
	ExitSuccess int = iota
	ExitErrParseOpts
	ExitErrStart

	ExitErrShutdown = 254
	ExitGeneral     = 255
)
