package instructions

import (
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/pkg/errors"
)

type (
	// KeyValuePair represents an arbitrary named value.
	//
	// This is useful for commands containing key-value maps that want to preserve
	// the order of insertion, instead of map[string]string which does not.
	KeyValuePair = instructions.KeyValuePair

	// KeyValuePairOptional is identical to KeyValuePair, but allows for optional values.
	KeyValuePairOptional = instructions.KeyValuePairOptional

	// Command interface is implemented by every possible command in a GGUFPackerfile.
	//
	// The interface only exposes the minimal common elements shared between every
	// command, while more detailed information per-command can be extracted using
	// runtime type analysis, e.g. type-switches.
	Command = instructions.Command

	// KeyValuePairs is a slice of KeyValuePair
	KeyValuePairs = instructions.KeyValuePairs
)

// withNameAndCode is the base of every command in a GGUFPackerfile (String() returns its source code)
type withNameAndCode struct {
	code     string
	name     string
	location []parser.Range
}

func (c *withNameAndCode) String() string {
	return c.code
}

// Name of the command
func (c *withNameAndCode) Name() string {
	return c.name
}

// Location of the command in source
func (c *withNameAndCode) Location() []parser.Range {
	return c.location
}

func newWithNameAndCode(req parseRequest) withNameAndCode {
	return withNameAndCode{code: strings.TrimSpace(req.original), name: req.command, location: req.location}
}

type (
	// SingleWordExpander is a provider for variable expansion where a single word
	// corresponds to a single output.
	SingleWordExpander = instructions.SingleWordExpander

	// SupportsSingleWordExpansion interface allows a command to support variable.
	SupportsSingleWordExpansion = instructions.SupportsSingleWordExpansion

	// SupportsSingleWordExpansionRaw interface allows a command to support
	// variable expansion, while ensuring that minimal transformations are applied
	// during expansion, so that quotes and other special characters are preserved.
	SupportsSingleWordExpansionRaw = instructions.SupportsSingleWordExpansionRaw

	// PlatformSpecific adds platform checks to a command
	PlatformSpecific = instructions.PlatformSpecific

	// FromGetter is an interface for commands that be able to parameterize with --from flag.
	FromGetter interface {
		GetFrom() string
	}
)

func expandKvp(kvp KeyValuePair, expander SingleWordExpander) (KeyValuePair, error) {
	key, err := expander(kvp.Key)
	if err != nil {
		return KeyValuePair{}, err
	}
	value, err := expander(kvp.Value)
	if err != nil {
		return KeyValuePair{}, err
	}
	return KeyValuePair{Key: key, Value: value, NoDelim: kvp.NoDelim}, nil
}

func expandKvpsInPlace(kvps KeyValuePairs, expander SingleWordExpander) error {
	for i, kvp := range kvps {
		newKvp, err := expandKvp(kvp, expander)
		if err != nil {
			return err
		}
		kvps[i] = newKvp
	}
	return nil
}

func expandSliceInPlace(values []string, expander SingleWordExpander) error {
	for i, v := range values {
		newValue, err := expander(v)
		if err != nil {
			return err
		}
		values[i] = newValue
	}
	return nil
}

// AddCommand adds files from the provided sources to the target destination.
//
//	ADD foo /path
//
// ADD supports tarball and remote URL handling, which may not always be
// desired - if you do not wish to have this automatic handling, use COPY.
type AddCommand struct {
	withNameAndCode
	SourcesAndDest
	Chown           string
	Chmod           string
	Link            bool
	ExcludePatterns []string
	KeepGitDir      bool // whether to keep .git dir, only meaningful for git sources
	Checksum        string
}

func (c *AddCommand) Expand(expander SingleWordExpander) error {
	expandedChown, err := expander(c.Chown)
	if err != nil {
		return err
	}
	c.Chown = expandedChown

	expandedChecksum, err := expander(c.Checksum)
	if err != nil {
		return err
	}
	c.Checksum = expandedChecksum

	return c.SourcesAndDest.Expand(expander)
}

// ArgCommand adds the specified variable to the list of variables that can be
// passed to the builder using the --build-arg flag for expansion and
// substitution.
//
//	ARG name[=value]
type ArgCommand struct {
	withNameAndCode
	Args []KeyValuePairOptional
}

func (c *ArgCommand) Expand(expander SingleWordExpander) error {
	for i, v := range c.Args {
		p, err := expander(v.Key)
		if err != nil {
			return err
		}
		v.Key = p
		if v.Value != nil {
			p, err = expander(*v.Value)
			if err != nil {
				return err
			}
			v.Value = &p
		}
		c.Args[i] = v
	}
	return nil
}

// CatCommand concatenate content to a file.
//
//	CAT "hi" /path
type CatCommand struct {
	withNameAndCode
	SourcesAndDest
}

// CmdCommand sets the default command to run in the container on start.
//
//	CMD ["-m", "model.gguf"]  # echo hi
type CmdCommand struct {
	withNameAndCode
	Args      []string
	Model     *CmdParameter
	Drafter   *CmdParameter
	Projector *CmdParameter
	Adapters  []CmdParameter
}

func (c *CmdCommand) Expand(expander SingleWordExpander) error {
	for i, v := range c.Args {
		p, err := expander(v)
		if err != nil {
			return err
		}
		c.Args[i] = p
	}
	if c.Model != nil {
		p, err := expander(c.Model.Value)
		if err != nil {
			return err
		}
		if p == "" {
			c.Model = nil
		} else {
			c.Model.Value = p
		}
	}
	if c.Drafter != nil {
		p, err := expander(c.Drafter.Value)
		if err != nil {
			return err
		}
		if p == "" {
			c.Drafter = nil
		} else {
			c.Drafter.Value = p
		}
	}
	if c.Projector != nil {
		p, err := expander(c.Projector.Value)
		if err != nil {
			return err
		}
		if p == "" {
			c.Projector = nil
		} else {
			c.Projector.Value = p
		}
	}
	for i, v := range c.Adapters {
		p, err := expander(v.Value)
		if err != nil {
			return err
		}
		if p == "" {
			c.Adapters = append(c.Adapters[:i], c.Adapters[i+1:]...)
		} else {
			c.Adapters[i].Value = p
		}
	}
	return nil
}

// CopyCommand copies files from the provided sources to the target destination.
//
//	COPY foo /path
//
// Same as 'ADD' but without the magic additional tarball and remote URL handling.
type CopyCommand struct {
	withNameAndCode
	SourcesAndDest
	From            string
	Chown           string
	Chmod           string
	Link            bool
	ExcludePatterns []string
	Parents         bool // parents preserves directory structure
}

func (c *CopyCommand) GetFrom() string {
	return c.From
}

func (c *CopyCommand) Expand(expander SingleWordExpander) error {
	expandedChown, err := expander(c.Chown)
	if err != nil {
		return err
	}
	c.Chown = expandedChown

	return c.SourcesAndDest.Expand(expander)
}

// ConvertCommand converts a model to target type GGUF file.
//
// CONVERT foo /path
type ConvertCommand struct {
	withNameAndCode
	SourcesAndDest
	From      string
	Class     string
	Type      string
	BaseModel string
}

func (c *ConvertCommand) GetFrom() string {
	return c.From
}

func (c *ConvertCommand) Expand(expander SingleWordExpander) error {
	{
		type_, err := expander(c.Type)
		if err != nil {
			return err
		}
		c.Type = type_
	}
	{
		baseModel, err := expander(c.BaseModel)
		if err != nil {
			return err
		}
		c.BaseModel = baseModel
	}
	return c.SourcesAndDest.Expand(expander)
}

// Stage represents a bundled collection of commands.
//
// Each stage begins with a FROM command (which is consumed into the Stage),
// indicating the source or stage to derive from, and ends either at the
// end-of-the file, or the start of the next stage.
//
// Stages can be named, and can be additionally configured to use a specific
// platform, in the case of a multi-arch base image.
type Stage struct {
	Name       string    // name of the stage
	Commands   []Command // commands contained within the stage
	BaseName   string    // name of the base stage or source
	BaseDigest string    // digest of the base stage or source
	Platform   string    // platform of base source to use

	Comment string // doc-comment directly above the stage

	SourceCode string         // contents of the defining FROM command
	Location   []parser.Range // location of the defining FROM command

	CmdCommand *CmdCommand // CmdCommand of the stage
}

// AddCommand appends a command to the stage.
func (s *Stage) AddCommand(cmd Command) {
	// todo: validate cmd type
	s.Commands = append(s.Commands, cmd)
}

// LabelCommand sets an image label in the output
//
//	LABEL some json data describing the image
type LabelCommand struct {
	withNameAndCode
	Labels KeyValuePairs
}

func (c *LabelCommand) Expand(expander SingleWordExpander) error {
	return expandKvpsInPlace(c.Labels, expander)
}

// QuantizeCommand converts a GGUF file to target type GGUF file.
//
// Quantize foo /path
type QuantizeCommand struct {
	withNameAndCode
	SourcesAndDest
	From               string
	Type               string
	Imatrix            string
	IncludeWeights     []string
	ExcludeWeights     []string
	LeaveOutputTensor  bool
	Pure               bool
	OutputTensorType   string
	TokenEmbeddingType string
}

func (c *QuantizeCommand) GetFrom() string {
	return c.From
}

func (c *QuantizeCommand) Expand(expander SingleWordExpander) error {
	{
		type_, err := expander(c.Type)
		if err != nil {
			return err
		}
		c.Type = type_
	}
	{
		imatrix_, err := expander(c.Imatrix)
		if err != nil {
			return err
		}
		c.Imatrix = imatrix_
	}
	return c.SourcesAndDest.Expand(expander)
}

// CmdParameter represents a parameter to a CMD.
type CmdParameter struct {
	Type  string
	Value string
	Index int
}

// SourceContent represents an anonymous file object
type SourceContent struct {
	Path   string // path to the file
	Data   string // string content from the file
	Expand bool   // whether to expand file contents
}

// SourcesAndDest represent a collection of sources and a destination
type SourcesAndDest struct {
	DestPath       string          // destination to write output
	SourcePaths    []string        // file path sources
	SourceContents []SourceContent // anonymous file sources
}

func (s *SourcesAndDest) Expand(expander SingleWordExpander) error {
	err := expandSliceInPlace(s.SourcePaths, expander)
	if err != nil {
		return err
	}

	expandedDestPath, err := expander(s.DestPath)
	if err != nil {
		return err
	}
	s.DestPath = expandedDestPath

	return nil
}

func (s *SourcesAndDest) ExpandRaw(expander SingleWordExpander) error {
	for i, content := range s.SourceContents {
		if !content.Expand {
			continue
		}

		expandedData, err := expander(content.Data)
		if err != nil {
			return err
		}
		s.SourceContents[i].Data = expandedData
	}
	return nil
}

// CurrentStage returns the last stage from a list of stages.
func CurrentStage(s []Stage) (*Stage, error) {
	if len(s) == 0 {
		return nil, errors.New("no build stage in current context")
	}
	return &s[len(s)-1], nil
}
