package parser

import (
	"github.com/moby/buildkit/frontend/dockerfile/parser"
)

type (
	// ErrorLocation gives a location in source code that caused the error
	ErrorLocation = parser.ErrorLocation

	// Range is a code section between two positions
	Range = parser.Range

	// Position is a point in source code
	Position = parser.Position
)

// WithLocation extends an error with a source code location
func WithLocation(err error, location []Range) error {
	return parser.WithLocation(err, location)
}

func withLocation(err error, start, end int) error {
	return WithLocation(err, toRanges(start, end))
}

func toRanges(start, end int) (r []Range) {
	if end <= start {
		end = start
	}
	for i := start; i <= end; i++ {
		r = append(r, Range{Start: Position{Line: i}, End: Position{Line: i}})
	}
	return
}
