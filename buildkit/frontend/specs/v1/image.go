package v1

import (
	"time"

	ggufparser "github.com/gpustack/gguf-parser-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type (
	Image struct {
		// Created is the combined date and time at which the image was created, formatted as defined by RFC 3339, section 5.6.
		Created *time.Time `json:"created,omitempty"`

		// Author defines the name and/or email address of the person or entity which created and is responsible for maintaining the image.
		Author string `json:"author,omitempty"`

		// Platform describes the platform which the image in the manifest runs on.
		Platform

		// Config defines the execution parameters which should be used as a base when running a container using the image.
		Config ImageConfig `json:"config,omitempty"`

		// RootFS references the layer content addresses used by the image.
		RootFS RootFS `json:"rootfs"`

		// History describes the history of each layer.
		History []History `json:"history,omitempty"`
	}

	Platform = ocispec.Platform

	ImageConfig struct {
		// Size represents the summarized size of all GGUFFiles.
		Size ggufparser.GGUFBytesScalar `json:"Size,omitempty"`

		// Model represents the main model of the image.
		Model *GGUFFile `json:"Model,omitempty"`

		// Drafter represents the drafter of the image.
		Drafter *GGUFFile `json:"Drafter,omitempty"`

		// Projector represents the projector of the image.
		Projector *GGUFFile `json:"Projector,omitempty"`

		// Adapters represents the adapters of the image.
		Adapters []*GGUFFile `json:"Adapters,omitempty"`

		// Cmd defines the arguments to launch.
		Cmd []string `json:"Cmd,omitempty"`

		// Labels contains arbitrary metadata for the image.
		Labels map[string]string `json:"Labels,omitempty"`
	}

	RootFS = ocispec.RootFS

	History = ocispec.History

	GGUFFile struct {
		ggufparser.GGUFFile `json:"GGUF"`

		// Architecture represents what architecture the model implements.
		Architecture string `json:"Architecture,omitempty"`

		// Parameters represents the parameters of the model.
		Parameters ggufparser.GGUFParametersScalar `json:"Parameters"`

		// BitsPerWeight represents the bits per weight of the model.
		BitsPerWeight ggufparser.GGUFBitsPerWeightScalar `json:"BitsPerWeight"`

		// FileType represents the file type of the model.
		FileType ggufparser.GGUFFileType `json:"FileType,omitempty"`

		// CmdParameterValue indicates the parameter value of the Cmd.
		CmdParameterValue string `json:"CmdParameterValue,omitempty"`

		// CmdParameterIndex indicates the index of the Cmd.
		CmdParameterIndex int `json:"CmdParameterIndex,omitempty"`
	}
)
