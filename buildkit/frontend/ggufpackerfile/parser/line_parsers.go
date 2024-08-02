package parser

import (
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/pkg/errors"
)

var (
	errGGUFPackerfileNotStringArray = errors.New("when using JSON array syntax, arrays must be comprised of strings only")
	errGGUFPackerfileNotJSONArray   = errors.New("not a JSON array")
)

// ignore the current argument. This will still leave a command parsed, but
// will not incorporate the arguments into the ast.
func parseIgnore(rest string, d *directives) (*Node, map[string]bool, error) {
	return &Node{}, nil, nil
}

// helper to parse words (i.e space delimited or quoted strings) in a statement.
// The quotes are preserved as part of this function and they are stripped later
// as part of processWords().
func parseWords(rest string, d *directives) []string {
	const (
		inSpaces = iota // looking for start of a word
		inWord
		inQuote
	)

	var (
		words    []string
		phase    = inSpaces
		quote    = '\000'
		blankOK  bool
		ch       rune
		chWidth  int
		sbuilder strings.Builder
	)

	for pos := 0; pos <= len(rest); pos += chWidth {
		if pos != len(rest) {
			ch, chWidth = utf8.DecodeRuneInString(rest[pos:])
		}

		if phase == inSpaces { // Looking for start of word
			if pos == len(rest) { // end of input
				break
			}
			if unicode.IsSpace(ch) { // skip spaces
				continue
			}
			phase = inWord // found it, fall through
		}

		if (phase == inWord) && (pos == len(rest)) {
			if blankOK || sbuilder.Len() > 0 {
				words = append(words, sbuilder.String())
			}
			break
		}

		if phase == inWord {
			if unicode.IsSpace(ch) {
				phase = inSpaces
				if blankOK || sbuilder.Len() > 0 {
					words = append(words, sbuilder.String())
				}
				sbuilder.Reset()
				blankOK = false
				continue
			}
			if ch == '\'' || ch == '"' {
				quote = ch
				blankOK = true
				phase = inQuote
			}
			if ch == d.escapeToken {
				if pos+chWidth == len(rest) {
					continue // just skip an escape token at end of line
				}
				// If we're not quoted and we see an escape token, then always just
				// add the escape token plus the char to the word, even if the char
				// is a quote.
				sbuilder.WriteRune(ch)
				pos += chWidth
				ch, chWidth = utf8.DecodeRuneInString(rest[pos:])
			}
			sbuilder.WriteRune(ch)

			continue
		}

		if ch == quote {
			phase = inWord
		}
		// The escape token is special except for ' quotes - can't escape anything for '
		if ch == d.escapeToken && quote != '\'' {
			if pos+chWidth == len(rest) {
				phase = inWord
				continue // just skip the escape token at end
			}
			pos += chWidth
			sbuilder.WriteRune(ch)
			ch, chWidth = utf8.DecodeRuneInString(rest[pos:])
		}
		sbuilder.WriteRune(ch)
	}

	return words
}

// parse environment like statements. Note that this does *not* handle
// variable interpolation, which will be handled in the evaluator.
func parseNameVal(rest string, key string, d *directives) (*Node, error) {
	// This is kind of tricky because we need to support the old
	// variant:   KEY name value
	// as well as the new one:    KEY name=value ...
	// The trigger to know which one is being used will be whether we hit
	// a space or = first.  space ==> old, "=" ==> new

	words := parseWords(rest, d)
	if len(words) == 0 {
		return nil, nil
	}

	// Old format (KEY name value)
	if !strings.Contains(words[0], "=") {
		parts := reWhitespace.Split(rest, 2)
		if len(parts) < 2 {
			return nil, errors.Errorf("%s must have two arguments", key)
		}
		return newKeyValueNode(parts[0], parts[1], ""), nil
	}

	var rootNode *Node
	var prevNode *Node
	for _, word := range words {
		if !strings.Contains(word, "=") {
			return nil, errors.Errorf("Syntax error - can't find = in %q. Must be of the form: name=value", word)
		}

		parts := strings.SplitN(word, "=", 2)
		node := newKeyValueNode(parts[0], parts[1], "=")
		rootNode, prevNode = appendKeyValueNode(node, rootNode, prevNode)
	}

	return rootNode, nil
}

func newKeyValueNode(key, value, sep string) *Node {
	return &Node{
		Value: key,
		Next: &Node{
			Value: value,
			Next:  &Node{Value: sep},
		},
	}
}

func appendKeyValueNode(node, rootNode, prevNode *Node) (*Node, *Node) {
	if rootNode == nil {
		rootNode = node
	}
	if prevNode != nil {
		prevNode.Next = node
	}

	for prevNode = node.Next; prevNode.Next != nil; {
		prevNode = prevNode.Next
	}
	return rootNode, prevNode
}

func parseLabel(rest string, d *directives) (*Node, map[string]bool, error) {
	node, err := parseNameVal(rest, "LABEL", d)
	return node, nil, err
}

// parses a statement containing one or more keyword definition(s) and/or
// value assignments, like `name1 name2= name3="" name4=value`.
// Note that this is a stricter format than the old format of assignment,
// allowed by parseNameVal(), in a way that this only allows assignment of the
// form `keyword=[<value>]` like  `name2=`, `name3=""`, and `name4=value` above.
// In addition, a keyword definition alone is of the form `keyword` like `name1`
// above. And the assignments `name2=` and `name3=""` are equivalent and
// assign an empty value to the respective keywords.
func parseNameOrNameVal(rest string, d *directives) (*Node, map[string]bool, error) {
	words := parseWords(rest, d)
	if len(words) == 0 {
		return nil, nil, nil
	}

	var (
		rootnode *Node
		prevNode *Node
	)
	for i, word := range words {
		node := &Node{}
		node.Value = word
		if i == 0 {
			rootnode = node
		} else {
			prevNode.Next = node
		}
		prevNode = node
	}

	return rootnode, nil, nil
}

// parses a whitespace-delimited set of arguments. The result is effectively a
// linked list of string arguments.
func parseStringsWhitespaceDelimited(rest string, d *directives) (*Node, map[string]bool, error) {
	if rest == "" {
		return nil, nil, nil
	}

	node := &Node{}
	rootnode := node
	prevnode := node
	for _, str := range reWhitespace.Split(rest, -1) { // use regexp
		prevnode = node
		node.Value = str
		node.Next = &Node{}
		node = node.Next
	}

	// XXX to get around regexp.Split *always* providing an empty string at the
	// end due to how our loop is constructed, nil out the last node in the
	// chain.
	prevnode.Next = nil

	return rootnode, nil, nil
}

// parseJSON converts JSON arrays to an AST.
func parseJSON(rest string) (*Node, map[string]bool, error) {
	rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
	if !strings.HasPrefix(rest, "[") {
		return nil, nil, errGGUFPackerfileNotJSONArray
	}

	var myJSON []interface{}
	if err := json.Unmarshal([]byte(rest), &myJSON); err != nil {
		return nil, nil, err
	}

	var top, prev *Node
	for _, str := range myJSON {
		s, ok := str.(string)
		if !ok {
			return nil, nil, errGGUFPackerfileNotStringArray
		}

		node := &Node{Value: s}
		if prev == nil {
			top = node
		} else {
			prev.Next = node
		}
		prev = node
	}

	return top, map[string]bool{"json": true}, nil
}

// parseMaybeJSON determines if the argument appears to be a JSON array. If
// so, passes to parseJSON; if not, quotes the result and returns a single
// node.
func parseMaybeJSON(rest string, d *directives) (*Node, map[string]bool, error) {
	if rest == "" {
		return nil, nil, nil
	}

	node, attrs, err := parseJSON(rest)

	if err == nil {
		return node, attrs, nil
	}
	if errors.Is(err, errGGUFPackerfileNotStringArray) {
		return nil, nil, err
	}

	node = &Node{}
	node.Value = rest
	return node, nil, nil
}

// parseMaybeJSONToList determines if the argument appears to be a JSON array. If
// so, passes to parseJSON; if not, attempts to parse it as a whitespace
// delimited string.
func parseMaybeJSONToList(rest string, d *directives) (*Node, map[string]bool, error) {
	node, attrs, err := parseJSON(rest)

	if err == nil {
		return node, attrs, nil
	}
	if errors.Is(err, errGGUFPackerfileNotStringArray) {
		return nil, nil, err
	}

	return parseStringsWhitespaceDelimited(rest, d)
}
