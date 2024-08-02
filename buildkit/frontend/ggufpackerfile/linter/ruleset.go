package linter

import (
	"fmt"
)

var (
	RuleStageNameCasing = LinterRule[func(string) string]{
		Name:        "StageNameCasing",
		Description: "Stage names should be lowercase",
		URL:         "https://docs.gpustack.ai/overview/",
		Format: func(stageName string) string {
			return fmt.Sprintf("Stage name '%s' should be lowercase", stageName)
		},
	}
	RuleFromAsCasing = LinterRule[func(string, string) string]{
		Name:        "FromAsCasing",
		Description: "The 'as' keyword should match the case of the 'from' keyword",
		URL:         "https://docs.gpustack.ai/overview/",
		Format: func(from, as string) string {
			return fmt.Sprintf("'%s' and '%s' keywords' casing do not match", as, from)
		},
	}
	RuleNoEmptyContinuation = LinterRule[func() string]{
		Name:        "NoEmptyContinuation",
		Description: "Empty continuation lines will become errors in a future release",
		URL:         "https://docs.gpustack.ai/overview/",
		Format: func() string {
			return "Empty continuation line"
		},
	}
	RuleConsistentInstructionCasing = LinterRule[func(string, string) string]{
		Name:        "ConsistentInstructionCasing",
		Description: "All commands within the GGUFPackerfile should use the same casing (either upper or lower)",
		URL:         "https://docs.gpustack.ai/overview/",
		Format: func(violatingCommand, correctCasing string) string {
			return fmt.Sprintf("Command '%s' should match the case of the command majority (%s)", violatingCommand, correctCasing)
		},
	}
	RuleDuplicateStageName = LinterRule[func(string) string]{
		Name:        "DuplicateStageName",
		Description: "Stage names should be unique",
		URL:         "https://docs.gpustack.ai/overview/",
		Format: func(stageName string) string {
			return fmt.Sprintf("Duplicate stage name %q, stage names should be unique", stageName)
		},
	}
	RuleReservedStageName = LinterRule[func(string) string]{
		Name:        "ReservedStageName",
		Description: "Reserved words should not be used as stage names",
		URL:         "https://docs.gpustack.ai/overview/",
		Format: func(reservedStageName string) string {
			return fmt.Sprintf("Stage name should not use the same name as reserved stage %q", reservedStageName)
		},
	}
	RuleUndefinedArgInFrom = LinterRule[func(string, string) string]{
		Name:        "UndefinedArgInFrom",
		Description: "FROM command must use declared ARGs",
		URL:         "https://docs.gpustack.ai/overview/",
		Format: func(baseArg, suggest string) string {
			out := fmt.Sprintf("FROM argument '%s' is not declared", baseArg)
			if suggest != "" {
				out += fmt.Sprintf(" (did you mean %s?)", suggest)
			}
			return out
		},
	}
	RuleUndefinedVar = LinterRule[func(string, string) string]{
		Name:        "UndefinedVar",
		Description: "Variables should be defined before their use",
		URL:         "https://docs.gpustack.ai/overview/",
		Format: func(arg, suggest string) string {
			out := fmt.Sprintf("Usage of undefined variable '$%s'", arg)
			if suggest != "" {
				out += fmt.Sprintf(" (did you mean $%s?)", suggest)
			}
			return out
		},
	}
	RuleMultipleInstructionsDisallowed = LinterRule[func(instructionName string) string]{
		Name:        "MultipleInstructionsDisallowed",
		Description: "Multiple instructions of the same type should not be used in the same stage",
		URL:         "https://docs.gpustack.ai/overview/",
		Format: func(instructionName string) string {
			return fmt.Sprintf("Multiple %s instructions should not be used in the same stage because only the last one will be used", instructionName)
		},
	}
	RuleLegacyKeyValueFormat = LinterRule[func(cmdName string) string]{
		Name:        "LegacyKeyValueFormat",
		Description: "Legacy key/value format with whitespace separator should not be used",
		URL:         "https://docs.gpustack.ai/overview/",
		Format: func(cmdName string) string {
			return fmt.Sprintf("\"%s key=value\" should be used instead of legacy \"%s key value\" format", cmdName, cmdName)
		},
	}
	RuleInvalidBaseImagePlatform = LinterRule[func(string, string, string) string]{
		Name:        "InvalidBaseImagePlatform",
		Description: "Base image platform does not match expected target platform",
		URL:         "https://docs.gpustack.ai/overview/",
		Format: func(image, expected, actual string) string {
			return fmt.Sprintf("Base image %s was pulled with platform %q, expected %q for current build", image, actual, expected)
		},
	}
	RuleSecretsUsedInArgOrEnv = LinterRule[func(string, string) string]{
		Name:        "SecretsUsedInArgOrEnv",
		Description: "Sensitive data should not be used in the ARG commands",
		URL:         "https://docs.gpustack.ai/overview/",
		Format: func(instruction, secretKey string) string {
			return fmt.Sprintf("Do not use ARG or ENV instructions for sensitive data (%s %q)", instruction, secretKey)
		},
	}
	RuleInvalidDefaultArgInFrom = LinterRule[func(string) string]{
		Name:        "InvalidDefaultArgInFrom",
		Description: "Default value for global ARG results in an empty or invalid base image name",
		URL:         "https://docs.gpustack.ai/overview/",
		Format: func(baseName string) string {
			return fmt.Sprintf("Default value for ARG %v results in empty or invalid base image name", baseName)
		},
	}
	RuleCopyIgnoredFile = LinterRule[func(string, string) string]{
		Name:        "CopyIgnoredFile",
		Description: "Attempting to Copy file that is excluded by .ggufpackerignore",
		URL:         "https://docs.gpustack.ai/overview/",
		Format: func(cmd, file string) string {
			return fmt.Sprintf("Attempting to %s file %q that is excluded by .ggufpackerignore", cmd, file)
		},
	}
)
