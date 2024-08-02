package command

// Define constants for the command strings
const (
	Add      = "add"
	Arg      = "arg"
	Cat      = "cat"
	Cmd      = "cmd"
	Copy     = "copy"
	Convert  = "convert"
	From     = "from"
	Label    = "label"
	Quantize = "quantize"
)

// Commands is list of all GGUFPackerfile commands
var Commands = map[string]struct{}{
	Add:      {},
	Arg:      {},
	Cat:      {},
	Cmd:      {},
	Copy:     {},
	Convert:  {},
	From:     {},
	Label:    {},
	Quantize: {},
}

func IsHeredocDirective(d string) bool {
	switch d {
	case Add, Copy, Cat:
		return true
	default:
		return false
	}
}
