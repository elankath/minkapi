package api

import (
	"errors"
	"fmt"
)

var (
	ErrInitFailed    = errors.New(fmt.Sprintf("%s init failed", ProgramName))
	ErrStartFailed   = errors.New(fmt.Sprintf("%s start failed", ProgramName))
	ErrServiceFailed = errors.New(fmt.Sprintf("%s service faied", ProgramName))

	ErrMissingOpt = errors.New("missing option")

	ErrLoadConfigTemplate    = errors.New("cannot load config template")
	ErrExecuteConfigTemplate = errors.New("cannot execute config template")

	ErrStoreNotFound = errors.New("store not found")
	ErrCreateObject  = errors.New("cannot create object")
	ErrDeleteObject  = errors.New("cannot delete object")
	ErrListObjects   = errors.New("cannot list objects")
)
