package instructions

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/moby/buildkit/util/suggest"
	"github.com/pkg/errors"

	"github.com/gpustack/gguf-packer-go/buildkit/frontend/ggufpackerfile/command"
	"github.com/gpustack/gguf-packer-go/buildkit/frontend/ggufpackerfile/linter"
	"github.com/gpustack/gguf-packer-go/buildkit/frontend/ggufpackerfile/parser"
)

type parseRequest struct {
	command    string
	args       []string
	heredocs   []parser.Heredoc
	attributes map[string]bool
	flags      *BFlags
	original   string
	location   []parser.Range
	comments   []string
}

func nodeArgs(node *parser.Node) []string {
	result := []string{}
	for ; node.Next != nil; node = node.Next {
		arg := node.Next
		if len(arg.Children) == 0 {
			result = append(result, arg.Value)
		} else if len(arg.Children) == 1 {
			// sub command
			result = append(result, arg.Children[0].Value)
			result = append(result, nodeArgs(arg.Children[0])...)
		}
	}
	return result
}

func newParseRequestFromNode(node *parser.Node) parseRequest {
	return parseRequest{
		command:    node.Value,
		args:       nodeArgs(node),
		heredocs:   node.Heredocs,
		attributes: node.Attributes,
		original:   node.Original,
		flags:      NewBFlagsWithArgs(node.Flags),
		location:   node.Location(),
		comments:   node.PrevComment,
	}
}

func ParseInstructionWithLinter(node *parser.Node, lint *linter.Linter) (v interface{}, err error) {
	defer func() {
		if err != nil {
			err = parser.WithLocation(err, node.Location())
		}
	}()
	req := newParseRequestFromNode(node)
	switch strings.ToLower(node.Value) {

	case command.Add:
		return parseAdd(req)
	case command.Arg:
		return parseArg(req)
	case command.Cat:
		return parseCat(req)
	case command.Cmd:
		return parseCmd(req)
	case command.Copy:
		return parseCopy(req)
	case command.Convert:
		return parseConvert(req)
	case command.From:
		if !isLowerCaseStageName(req.args) {
			msg := linter.RuleStageNameCasing.Format(req.args[2])
			lint.Run(&linter.RuleStageNameCasing, node.Location(), msg)
		}
		if !doesFromCaseMatchAsCase(req) {
			msg := linter.RuleFromAsCasing.Format(req.command, req.args[1])
			lint.Run(&linter.RuleFromAsCasing, node.Location(), msg)
		}
		return parseFrom(req)
	case command.Label:
		return parseLabel(req)
	case command.Quantize:
		return parseQuantize(req)
	}
	return nil, suggest.WrapError(&UnknownInstructionError{Instruction: node.Value, Line: node.StartLine}, node.Value, allInstructionNames(), false)
}

// UnknownInstructionError represents an error occurring when a command is unresolvable
type UnknownInstructionError struct {
	Line        int
	Instruction string
}

func (e *UnknownInstructionError) Error() string {
	return fmt.Sprintf("unknown instruction: %s", e.Instruction)
}

type parseError struct {
	inner error
	node  *parser.Node
}

func (e *parseError) Error() string {
	return fmt.Sprintf("ggufpackerfile parse error on line %d: %v", e.node.StartLine, e.inner.Error())
}

func (e *parseError) Unwrap() error {
	return e.inner
}

// Parse a GGUFPackerfile into a collection of buildable stages.
// metaArgs is a collection of ARG instructions that occur before the first FROM.
func Parse(ast *parser.Node, lint *linter.Linter) (stages []Stage, metaArgs []ArgCommand, err error) {
	for _, n := range ast.Children {
		cmd, err := ParseInstructionWithLinter(n, lint)
		if err != nil {
			return nil, nil, &parseError{inner: err, node: n}
		}
		if len(stages) == 0 {
			// meta arg case
			if a, isArg := cmd.(*ArgCommand); isArg {
				metaArgs = append(metaArgs, *a)
				continue
			}
		}
		switch c := cmd.(type) {
		case *Stage:
			stages = append(stages, *c)
		case Command:
			stage, err := CurrentStage(stages)
			if err != nil {
				return nil, nil, parser.WithLocation(err, n.Location())
			}
			if cc, isCmd := cmd.(*CmdCommand); isCmd {
				stage.CmdCommand = cc
			}
			stage.AddCommand(c)
		default:
			return nil, nil, parser.WithLocation(errors.Errorf("%T is not a command type", cmd), n.Location())
		}
	}
	return stages, metaArgs, nil
}

func parseAdd(req parseRequest) (*AddCommand, error) {
	if len(req.args) < 2 {
		return nil, errNoDestinationArgument("ADD")
	}

	flExcludes := req.flags.AddStrings("exclude")
	flChown := req.flags.AddString("chown", "")
	flChmod := req.flags.AddString("chmod", "")
	flLink := req.flags.AddBool("link", false)
	flKeepGitDir := req.flags.AddBool("keep-git-dir", false)
	flChecksum := req.flags.AddString("checksum", "")
	if err := req.flags.Parse(); err != nil {
		return nil, err
	}

	sourcesAndDest, err := parseSourcesAndDest(req, "ADD")
	if err != nil {
		return nil, err
	}

	return &AddCommand{
		withNameAndCode: newWithNameAndCode(req),
		SourcesAndDest:  *sourcesAndDest,
		Chown:           flChown.Value,
		Chmod:           flChmod.Value,
		Link:            flLink.Value == "true",
		KeepGitDir:      flKeepGitDir.Value == "true",
		Checksum:        flChecksum.Value,
		ExcludePatterns: stringValuesFromFlagIfPossible(flExcludes),
	}, nil
}

func parseArg(req parseRequest) (*ArgCommand, error) {
	if len(req.args) < 1 {
		return nil, errAtLeastOneArgument("ARG")
	}

	pairs := make([]KeyValuePairOptional, len(req.args))

	for i, arg := range req.args {
		kvpo := KeyValuePairOptional{}

		// 'arg' can just be a name or name-value pair. Note that this is different
		// from 'env' that handles the split of name and value at the parser level.
		// The reason for doing it differently for 'arg' is that we support just
		// defining an arg and not assign it a value (while 'env' always expects a
		// name-value pair). If possible, it will be good to harmonize the two.
		if strings.Contains(arg, "=") {
			parts := strings.SplitN(arg, "=", 2)
			if len(parts[0]) == 0 {
				return nil, errBlankCommandNames("ARG")
			}

			kvpo.Key = parts[0]
			kvpo.Value = &parts[1]
		} else {
			kvpo.Key = arg
		}
		kvpo.Comment = getComment(req.comments, kvpo.Key)
		pairs[i] = kvpo
	}

	return &ArgCommand{
		Args:            pairs,
		withNameAndCode: newWithNameAndCode(req),
	}, nil
}

func parseCat(req parseRequest) (*CatCommand, error) {
	if len(req.args) < 2 {
		return nil, errNoDestinationArgument("CAT")
	}

	if err := req.flags.Parse(); err != nil {
		return nil, err
	}

	sourcesAndDest, err := parseSourcesAndDest(req, "CAT")
	if err != nil {
		return nil, err
	}

	if len(sourcesAndDest.SourcePaths) != 0 {
		var sb strings.Builder
		for i, source := range sourcesAndDest.SourcePaths {
			sb.WriteString(source)
			if i > 0 {
				sb.WriteString(" ")
			}
		}
		sourcesAndDest.SourcePaths = nil
		sourcesAndDest.SourceContents = []SourceContent{
			{
				Path:   "STDIN",
				Data:   sb.String(),
				Expand: true,
			},
		}
	}

	return &CatCommand{
		withNameAndCode: newWithNameAndCode(req),
		SourcesAndDest:  *sourcesAndDest,
	}, nil
}

func parseCmd(req parseRequest) (*CmdCommand, error) {
	if err := req.flags.Parse(); err != nil {
		return nil, err
	}

	if !req.attributes["json"] {
		return nil, errors.New("CMD: must use JSON form")
	}

	args := handleJSONArgs(req.args, req.attributes)
	if len(args) == 0 {
		return nil, errors.New("CMD: no arguments provided")
	}
	if len(args[0]) == 0 {
		return nil, errors.New("CMD: illegal first argument: blank")
	}
	if args[0][0] != '-' {
		return nil, errors.New("CMD: illegal first argument: must starts with '-'")
	}

	var (
		model     *CmdParameter
		drafter   *CmdParameter
		projector *CmdParameter
		adapters  []CmdParameter
	)
	for i, s := 0, len(args); i < s; i++ {
		switch args[i] {
		default:
			continue
		case "-m", "--model":
			if i+1 >= s {
				return nil, errors.New("CMD: -m/--model argument requires a value")
			}
			i++
			if args[i] == "" {
				continue
			}
			model = &CmdParameter{
				Type:  "model",
				Value: args[i],
				Index: i,
			}
		case "-md", "--model-draft":
			if i+1 >= s {
				return nil, errors.New("CMD: -md/--model-draft argument requires a value")
			}
			i++
			if args[i] == "" {
				continue
			}
			drafter = &CmdParameter{
				Type:  "drafter",
				Value: args[i],
				Index: i,
			}
		case "--mmproj":
			if i+1 >= s {
				return nil, errors.New("CMD: --mmproj argument requires a value")
			}
			i++
			if args[i] == "" {
				continue
			}
			projector = &CmdParameter{
				Type:  "projector",
				Value: args[i],
				Index: i,
			}
		case "--lora":
			if i+1 >= s {
				return nil, errors.New("CMD: --lora argument requires a value")
			}
			i++
			if args[i] == "" {
				continue
			}
			adapters = append(adapters, CmdParameter{
				Type:  "adapter",
				Value: args[i],
				Index: i,
			})
		}
	}

	return &CmdCommand{
		withNameAndCode: newWithNameAndCode(req),
		Args:            args,
		Model:           model,
		Drafter:         drafter,
		Projector:       projector,
		Adapters:        adapters,
	}, nil
}

func parseCopy(req parseRequest) (*CopyCommand, error) {
	if len(req.args) < 2 {
		return nil, errNoDestinationArgument("COPY")
	}

	flExcludes := req.flags.AddStrings("exclude")
	flParents := req.flags.AddBool("parents", false)
	flChown := req.flags.AddString("chown", "")
	flFrom := req.flags.AddString("from", "")
	flChmod := req.flags.AddString("chmod", "")
	flLink := req.flags.AddBool("link", false)

	if err := req.flags.Parse(); err != nil {
		return nil, err
	}

	sourcesAndDest, err := parseSourcesAndDest(req, "COPY")
	if err != nil {
		return nil, err
	}

	return &CopyCommand{
		withNameAndCode: newWithNameAndCode(req),
		SourcesAndDest:  *sourcesAndDest,
		From:            flFrom.Value,
		Chown:           flChown.Value,
		Chmod:           flChmod.Value,
		Link:            flLink.Value == "true",
		Parents:         flParents != nil && flParents.Value == "true",
		ExcludePatterns: stringValuesFromFlagIfPossible(flExcludes),
	}, nil
}

func parseConvert(req parseRequest) (*ConvertCommand, error) {
	if len(req.args) < 2 {
		return nil, errNoDestinationArgument("CONVERT")
	}

	flFrom := req.flags.AddString("from", "")
	flType := req.flags.AddString("type", "FP16")

	if err := req.flags.Parse(); err != nil {
		return nil, err
	}

	if flType.Value == "" {
		return nil, errors.New("CONVERT: type is required")
	}

	sourcesAndDest, err := parseSourcesAndDest(req, "CONVERT")
	if err != nil {
		return nil, err
	}
	if len(sourcesAndDest.SourcePaths) != 1 {
		return nil, errors.New("CONVERT: only one source file is allowed")
	}

	return &ConvertCommand{
		withNameAndCode: newWithNameAndCode(req),
		SourcesAndDest:  *sourcesAndDest,
		From:            flFrom.Value,
		Type:            flType.Value,
	}, nil
}

func parseLabel(req parseRequest) (*LabelCommand, error) {
	if err := req.flags.Parse(); err != nil {
		return nil, err
	}

	labels, err := parseKvps(req.args, "LABEL")
	if err != nil {
		return nil, err
	}

	return &LabelCommand{
		withNameAndCode: newWithNameAndCode(req),
		Labels:          labels,
	}, nil
}

func parseQuantize(req parseRequest) (*QuantizeCommand, error) {
	if len(req.args) < 2 {
		return nil, errNoDestinationArgument("QUANTIZE")
	}

	flFrom := req.flags.AddString("from", "")
	flType := req.flags.AddString("type", "Q5_K_M")
	flImatrix := req.flags.AddString("imatrix", "")
	flIncudeWeights := req.flags.AddStrings("include-weights")
	flExcludeWeights := req.flags.AddStrings("exclude-weights")
	flLeaveOutputTensor := req.flags.AddBool("leave-output-tensor", false)
	flPure := req.flags.AddBool("pure", false)
	flOutputTensorType := req.flags.AddString("output-tensor-type", "")
	flTokenEmbeddingType := req.flags.AddString("token-embedding-type", "")

	if err := req.flags.Parse(); err != nil {
		return nil, err
	}

	if flType.Value == "" {
		return nil, errors.New("QUANTIZE: type is required")
	}
	if len(flIncudeWeights.StringValues) > 0 && len(flExcludeWeights.StringValues) > 0 {
		return nil, errors.New("QUANTIZE: include-weights and exclude-weights can't be used together")
	}

	sourcesAndDest, err := parseSourcesAndDest(req, "QUANTIZE")
	if err != nil {
		return nil, err
	}
	if len(sourcesAndDest.SourcePaths) != 1 {
		return nil, errors.New("QUANTIZE: only one source file is allowed")
	}

	return &QuantizeCommand{
		withNameAndCode:    newWithNameAndCode(req),
		SourcesAndDest:     *sourcesAndDest,
		From:               flFrom.Value,
		Type:               flType.Value,
		Imatrix:            flImatrix.Value,
		IncludeWeights:     slices.Clone(flIncudeWeights.StringValues),
		ExcludeWeights:     slices.Clone(flExcludeWeights.StringValues),
		LeaveOutputTensor:  flLeaveOutputTensor.Value == "true",
		Pure:               flPure.Value == "true",
		OutputTensorType:   flOutputTensorType.Value,
		TokenEmbeddingType: flTokenEmbeddingType.Value,
	}, nil
}

func parseFrom(req parseRequest) (*Stage, error) {
	stageName, err := parseBuildStageName(req.args)
	if err != nil {
		return nil, err
	}

	flPlatform := req.flags.AddString("platform", "")
	if err := req.flags.Parse(); err != nil {
		return nil, err
	}

	code := strings.TrimSpace(req.original)
	return &Stage{
		BaseName:   req.args[0],
		Name:       stageName,
		SourceCode: code,
		Commands:   []Command{},
		Platform:   flPlatform.Value,
		Location:   req.location,
		Comment:    getComment(req.comments, stageName),
	}, nil
}

var validStageName = regexp.MustCompile("^[a-z][a-z0-9-_.]*$")

func parseBuildStageName(args []string) (stageName string, err error) {
	switch {
	case len(args) == 3 && strings.EqualFold(args[1], "as"):
		stageName = strings.ToLower(args[2])
		if !validStageName.MatchString(stageName) {
			return "", errors.Errorf("invalid name for build stage: %q, name can't start with a number or contain symbols", args[2])
		}
	case len(args) != 1:
		return "", errors.New("FROM requires either one or three arguments")
	}

	return stageName, nil
}

func parseSourcesAndDest(req parseRequest, command string) (*SourcesAndDest, error) {
	srcs := req.args[:len(req.args)-1]
	dest := req.args[len(req.args)-1]
	if heredoc := parser.MustParseHeredoc(dest); heredoc != nil {
		return nil, errBadHeredoc(command, "a destination")
	}

	heredocLookup := make(map[string]parser.Heredoc)
	for _, heredoc := range req.heredocs {
		heredocLookup[heredoc.Name] = heredoc
	}

	var sourcePaths []string
	var sourceContents []SourceContent
	for _, src := range srcs {
		if heredoc := parser.MustParseHeredoc(src); heredoc != nil {
			content := heredocLookup[heredoc.Name].Content
			if heredoc.Chomp {
				content = parser.ChompHeredocContent(content)
			}
			sourceContents = append(sourceContents,
				SourceContent{
					Data:   content,
					Path:   heredoc.Name,
					Expand: heredoc.Expand,
				},
			)
		} else {
			sourcePaths = append(sourcePaths, src)
		}
	}

	return &SourcesAndDest{
		DestPath:       dest,
		SourcePaths:    sourcePaths,
		SourceContents: sourceContents,
	}, nil
}

func parseKvps(args []string, cmdName string) (KeyValuePairs, error) {
	if len(args) == 0 {
		return nil, errAtLeastOneArgument(cmdName)
	}
	if len(args)%3 != 0 {
		// should never get here, but just in case
		return nil, errTooManyArguments(cmdName)
	}
	var res KeyValuePairs
	for j := 0; j < len(args); j += 3 {
		if len(args[j]) == 0 {
			return nil, errBlankCommandNames(cmdName)
		}
		name, value, delim := args[j], args[j+1], args[j+2]
		res = append(res, KeyValuePair{Key: name, Value: value, NoDelim: delim == ""})
	}
	return res, nil
}

func stringValuesFromFlagIfPossible(f *Flag) []string {
	if f == nil {
		return nil
	}

	return f.StringValues
}

func errAtLeastOneArgument(command string) error {
	return errors.Errorf("%s requires at least one argument", command)
}

func errNoDestinationArgument(command string) error {
	return errors.Errorf("%s requires at least two arguments, but only one was provided. Destination could not be determined", command)
}

func errBadHeredoc(command string, option string) error {
	return errors.Errorf("%s cannot accept a heredoc as %s", command, option)
}

func errBlankCommandNames(command string) error {
	return errors.Errorf("%s names can not be blank", command)
}

func errTooManyArguments(command string) error {
	return errors.Errorf("Bad input to %s, too many arguments", command)
}

func getComment(comments []string, name string) string {
	if name == "" {
		return ""
	}
	for _, line := range comments {
		if strings.HasPrefix(line, name+" ") {
			return strings.TrimPrefix(line, name+" ")
		}
	}
	return ""
}

func allInstructionNames() []string {
	out := make([]string, len(command.Commands))
	i := 0
	for name := range command.Commands {
		out[i] = strings.ToUpper(name)
		i++
	}
	return out
}

func isLowerCaseStageName(cmdArgs []string) bool {
	if len(cmdArgs) != 3 {
		return true
	}
	stageName := cmdArgs[2]
	return stageName == strings.ToLower(stageName)
}

func doesFromCaseMatchAsCase(req parseRequest) bool {
	if len(req.args) < 3 {
		return true
	}
	// consistent casing for the command is handled elsewhere.
	// If the command is not consistent, there's no need to
	// add an additional lint warning for the `as` argument.
	fromHasLowerCasing := req.command == strings.ToLower(req.command)
	fromHasUpperCasing := req.command == strings.ToUpper(req.command)
	if !fromHasLowerCasing && !fromHasUpperCasing {
		return true
	}

	if fromHasLowerCasing {
		return req.args[1] == strings.ToLower(req.args[1])
	}
	return req.args[1] == strings.ToUpper(req.args[1])
}
