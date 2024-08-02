package instructions

import "github.com/moby/buildkit/frontend/dockerfile/instructions"

type (
	// BFlags contains all flags information for the builder
	BFlags = instructions.BFlags

	// Flag contains all information for a flag
	Flag = instructions.Flag
)

// NewBFlagsWithArgs returns the new BFlags struct with Args set to args
func NewBFlagsWithArgs(args []string) *BFlags {
	return instructions.NewBFlagsWithArgs(args)
}
