package hyperkube

import (
	"github.com/spf13/pflag"
)

var (
	nilKube = &nilKubeType{}
)

type Interface interface {
	// FindServer will find a specific server named name.
	FindServer(name string) bool

	// The executable name, used for help and soft-link invocation
	Name() string

	// Flags returns a flagset for "global" flags.
	Flags() *pflag.FlagSet
}

type nilKubeType struct{}

func (n *nilKubeType) FindServer(_ string) bool {
	return false
}

func (n *nilKubeType) Name() string {
	return ""
}

func (n *nilKubeType) Flags() *pflag.FlagSet {
	return nil
}

func Nil() Interface {
	return nilKube
}
