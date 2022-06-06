// Copyright 2022 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// package protoscope ...
package protoscope

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	_ "embed"
)

// The contents of language.text.
//go:embed language.txt
var LanguageText string

// A Position describes a location in the input stream.
//
// The zero-value Position represents the first byte of an anonymous input file.
type Position struct {
	Offset int    // Byte offset.
	Line   int    // Line number (zero-indexed).
	Column int    // Column number (zero-indexed byte, not rune, count).
	File   string // Optional file name for pretty-printing.
}

// String converts a Position to a string.
func (p Position) String() string {
	file := p.File
	if file == "" {
		file = "<input>"
	}
	return fmt.Sprintf("%s:%d:%d", file, p.Line+1, p.Column+1)
}

// A tokenKind is a kind of token.
type tokenKind int

const (
	tokenBytes tokenKind = iota
	tokenLongForm
	tokenLeftCurly
	tokenRightCurly
	tokenEOF
)

// A ParseError may be produced while executing a Protoscope file, wrapping
// another error along with a position.
//
// Errors produced by functions in this package my by type-asserted to
// ParseError to try and obtain the position at which the error occurred.
type ParseError struct {
	Pos Position
	Err error
}

// Error makes this type into an error type.
func (e *ParseError) Error() string {
	return fmt.Sprintf("%s: %s", e.Pos, e.Err)
}

// Unwrap extracts the inner wrapped error.
//
// See errors.Unwrap().
func (e *ParseError) Unwrap() error {
	return e.Err
}

// A token is a token in a Protoscope file.
type token struct {
	// Kind is the kind of the token.
	Kind tokenKind
	// Value, for a tokenBytes token, is the decoded value of the token in
	// bytes.
	Value []byte
	// WireType, for a tokenBytes token, is which wire type an InferredType
	// tag expression that preceded it should become.
	WireType int
	// InferredType indicates that this was a tag expression which wishes to infer
	// its type based on tokens that follow.
	InferredType bool
	// Pos is the position of the first byte of the token.
	Pos Position
	// Length, for a tokenLongForm token, is the number of bytes to use to
	// encode the length, not including the initial one.
	Length int
}

var (
	// The relevant capture groups are:
	// 1: The actual value.
	// 2: The encoding format.
	// 3: The wire type, including the colon, if this is a tag.
	// 4: The wire type expression, which may be empty if it is inferred.
	regexpIntOrTag = regexp.MustCompile(`^-?([0-9]+|0x[0-9a-fA-F]+)(z|i32|i64)?(:(\w*))?$`)
	regexpDecFp    = regexp.MustCompile(`^(-?[0-9]+\.[0-9]+(?:[eE]-?[0-9]+)?)(i32|i64)?$`)
	regexpHexFp    = regexp.MustCompile(`^(-?0x[0-9a-fA-F]+\.[0-9a-fA-F]+(?:[pP]-?[0-9]+)?)(i32|i64)?$`)
	regexpLongForm = regexp.MustCompile(`^long-form:([0-9]+)$`)
)

// A Scanner represents parsing state for a Protoscope file.
//
// A zero-value Scanner is ready to begin parsing (given that Input is set to
// a valid value). However, it is recommended to use NewScanner to create a new
// Scanner, since it can pre-populate fields other than Input with default
// settings.
type Scanner struct {
	// Input is the input text being processed.
	Input string
	// Position is the current position at which parsing should
	// resume. The Offset field is used for indexing into Input; the remaining
	// fields are used for error-reporting.
	pos Position
}

// NewScanner creates a new scanner for parsing the given input.
func NewScanner(input string) *Scanner {
	return &Scanner{Input: input}
}

// SetFile sets the file path shown in this Scanner's error reports.
func (s *Scanner) SetFile(path string) {
	s.pos.File = path
}

// Exec consumes tokens until Input is exhausted, returning the resulting
// encoded maybe-DER.
func (s *Scanner) Exec() ([]byte, error) {
	return s.exec(nil)
}

// isEOF returns whether the cursor is at least n bytes ahead of the end of the
// input.
func (s *Scanner) isEOF(n int) bool {
	return s.pos.Offset+n >= len(s.Input)
}

// advance advances the scanner's cursor n positions.
//
// Unlike just s.pos.Offset += n, this will not proceed beyond the end of the
// string, and will update the line and column information accordingly.
func (s *Scanner) advance(n int) {
	for i := 0; i < n && !s.isEOF(0); i++ {
		if s.Input[s.pos.Offset] == '\n' {
			s.pos.Line++
			s.pos.Column = 0
		} else {
			s.pos.Column++
		}
		s.pos.Offset++
	}
}

// consume advances exactly n times and returns all source bytes between the
// initial cursor position and excluding the final cursor position.
//
// If EOF is reached before all n bytes are consumed, the function returns
// false.
func (s *Scanner) consume(n int) (string, bool) {
	start := s.pos.Offset
	s.advance(n)
	if s.pos.Offset-start != n {
		return "", false
	}

	return s.Input[start:s.pos.Offset], true
}

// consumeUntil advances the cursor until the given byte is seen, returning all
// source bytes between the initial cursor position and excluding the given
// byte. This function will advance past the searched-for byte.
//
// If EOF is reached before the byte is seen, the function returns false.
func (s *Scanner) consumeUntil(b byte) (string, bool) {
	if i := strings.IndexByte(s.Input[s.pos.Offset:], b); i != -1 {
		text, _ := s.consume(i + 1)
		return text[:i], true
	}
	return "", false
}

// parseEscapeSequence parses a Protoscope escape sequence, returning the rune
// it escapes.
//
// Valid escapes are:
// \n \" \\ \xNN
//
// This function assumes that the scanner's cursor is currently on a \ rune.
func (s *Scanner) parseEscapeSequence() (rune, error) {
	s.advance(1) // Skip the \. The caller is assumed to have validated it.
	if s.isEOF(0) {
		return 0, &ParseError{s.pos, errors.New("expected escape character")}
	}

	switch c := s.Input[s.pos.Offset]; c {
	case 'n':
		s.advance(1)
		return '\n', nil
	case '"', '\\':
		s.advance(1)
		return rune(c), nil
	case 'x':
		s.advance(1)

		hexes, ok := s.consume(2)
		if !ok {
			return 0, &ParseError{s.pos, errors.New("unfinished escape sequence")}
		}

		bytes, err := hex.DecodeString(hexes)
		if err != nil {
			return 0, &ParseError{s.pos, err}
		}

		var r rune
		for _, b := range bytes {
			r <<= 8
			r |= rune(b)
		}
		return r, nil
	default:
		return 0, &ParseError{s.pos, fmt.Errorf("unknown escape sequence \\%c", c)}
	}
}

// parseQuotedString parses a UTF-8 string until the next ".
//
// This function assumes that the scanner's cursor is currently on a " rune.
func (s *Scanner) parseQuotedString() (token, error) {
	s.advance(1) // Skip the ". The caller is assumed to have validated it.
	start := s.pos
	var bytes []byte
	for {
		if s.isEOF(0) {
			return token{}, &ParseError{start, errors.New("unmatched \"")}
		}
		switch c := s.Input[s.pos.Offset]; c {
		case '"':
			s.advance(1)
			return token{Kind: tokenBytes, Value: bytes, Pos: start}, nil
		case '\\':
			escapeStart := s.pos
			r, err := s.parseEscapeSequence()
			if err != nil {
				return token{}, err
			}
			if r > 0xff {
				// TODO(davidben): Alternatively, should these encode as UTF-8?
				return token{}, &ParseError{escapeStart, errors.New("illegal escape for quoted string")}
			}
			bytes = append(bytes, byte(r))
		default:
			s.advance(1)
			bytes = append(bytes, c)
		}
	}
}

// next lexes the next token.
func (s *Scanner) next(lengthModifier **token) (token, error) {
again:
	if s.isEOF(0) {
		return token{Kind: tokenEOF, Pos: s.pos}, nil
	}

	switch s.Input[s.pos.Offset] {
	case ' ', '\t', '\n', '\r':
		// Skip whitespace.
		s.advance(1)
		goto again
	case '#':
		// Skip to the end of the comment.
		s.advance(1)
		for !s.isEOF(0) {
			wasNewline := s.Input[s.pos.Offset] == '\n'
			s.advance(1)
			if wasNewline {
				break
			}
		}
		goto again
	case '{':
		s.advance(1)
		return token{Kind: tokenLeftCurly, Pos: s.pos}, nil
	case '}':
		s.advance(1)
		return token{Kind: tokenRightCurly, Pos: s.pos}, nil
	case '"':
		return s.parseQuotedString()
	case '`':
		s.advance(1)
		hexStr, ok := s.consumeUntil('`')
		if !ok {
			return token{}, &ParseError{s.pos, errors.New("unmatched `")}
		}
		bytes, err := hex.DecodeString(hexStr)
		if err != nil {
			return token{}, &ParseError{s.pos, err}
		}
		return token{Kind: tokenBytes, Value: bytes, Pos: s.pos}, nil
	}

	// Normal token. Consume up to the next whitespace character, symbol, or
	// EOF.
	start := s.pos
	s.advance(1)
loop:
	for !s.isEOF(0) {
		switch s.Input[s.pos.Offset] {
		case ' ', '\t', '\n', '\r', '{', '}', '[', ']', '`', '"', '#':
			break loop
		default:
			s.advance(1)
		}
	}

	symbol := s.Input[start.Offset:s.pos.Offset]

	if match := regexpIntOrTag.FindStringSubmatch(symbol); match != nil {
		// Go can detect the base if we set base=0, but it treats a leading 0 as
		// octal.
		base := 10
		isHex := strings.HasPrefix(match[0], "0x") || strings.HasPrefix(match[0], "-0x")
		if isHex {
			base = 16
		}

		value, err := strconv.ParseInt(strings.TrimPrefix(match[1], "0x"), base, 64)
		if err != nil {
			return token{}, &ParseError{start, err}
		}

		if strings.HasPrefix(match[0], "-") {
			value = -value
		}

		inferredType := false
		if match[3] != "" {
			if match[2] == "i32" || match[2] == "i64" {
				return token{}, &ParseError{start, errors.New("cannot use fixed-width encoding on tag expressions")}
			}

			var wireType int64
			switch match[4] {
			case "":
				inferredType = true
			case "VARINT":
				wireType = 0
			case "I64":
				wireType = 1
			case "LEN":
				wireType = 2
			case "SGROUP":
				wireType = 3
			case "EGROUP":
				wireType = 4
			case "I32":
				wireType = 5
			default:
				var err error
				if strings.HasPrefix(match[4], "0x") {
					wireType, err = strconv.ParseInt(match[4], 16, 64)
				} else {
					wireType, err = strconv.ParseInt(match[4], 10, 64)
				}
				if err != nil {
					return token{}, &ParseError{start, err}
				}
			}

			if wireType > 7 {
				return token{}, &ParseError{start, errors.New("a tag's wire type must be between 0 and 7")}
			}

			value <<= 3
			value |= wireType
		}

		var enc []byte
		var wireType int
		switch match[2] {
		case "z":
			value = (value << 1) ^ (value >> 63)
			fallthrough
		case "":
			var len int
			if *lengthModifier != nil {
				len = (*lengthModifier).Length
				*lengthModifier = nil
			}
			enc = encodeVarint(nil, uint64(value), len)
		case "i32":
			wireType = 5
			if value >= (1<<32) || value < -(1<<32) {
				return token{}, &ParseError{start, fmt.Errorf("%s does not fit in 32 bits", symbol)}
			}
			enc = make([]byte, 4)
			binary.LittleEndian.PutUint32(enc, uint32(value))
		case "i64":
			wireType = 1
			enc = make([]byte, 8)
			binary.LittleEndian.PutUint64(enc, uint64(value))
		default:
			panic("unreachable")
		}

		return token{Kind: tokenBytes, InferredType: inferredType, WireType: wireType, Value: enc, Pos: s.pos}, nil
	}

	match := regexpDecFp.FindStringSubmatch(symbol)
	if match == nil {
		match = regexpHexFp.FindStringSubmatch(symbol)
	}

	if match != nil {
		// This works fine regardless of base; ParseFloat will detect the base from
		// the 0x prefix. Go expects an exponent on a hex float, so we need to
		// modify match[1] appropriately.
		fp := match[1]
		if strings.Contains(fp, "0x") && !strings.ContainsAny(fp, "Pp") {
			fp += "p0"
		}

		value, err := strconv.ParseFloat(fp, 64)
		if err != nil {
			return token{}, &ParseError{start, err}
		}

		var enc []byte
		var wireType int
		switch match[2] {
		case "i32":
			wireType = 5
			if float64(float32(value)) != value {
				return token{}, &ParseError{start, fmt.Errorf("%s does not fit in 32 bits", symbol)}
			}
			enc = make([]byte, 4)
			binary.LittleEndian.PutUint32(enc, math.Float32bits(float32(value)))
		case "", "i64":
			wireType = 1
			enc = make([]byte, 8)
			binary.LittleEndian.PutUint64(enc, math.Float64bits(value))
		default:
			panic("unreachable")
		}

		return token{Kind: tokenBytes, WireType: wireType, Value: enc, Pos: s.pos}, nil
	}

	if match := regexpLongForm.FindStringSubmatch(symbol); match != nil {
		l, err := strconv.ParseInt(match[1], 10, 32)
		if err != nil {
			return token{}, &ParseError{start, err}
		}
		return token{Kind: tokenLongForm, Length: int(l)}, nil
	}

	switch symbol {
	case "true":
		return token{Kind: tokenBytes, Value: []byte{1}, Pos: s.pos}, nil
	case "false":
		return token{Kind: tokenBytes, Value: []byte{0}, Pos: s.pos}, nil
	case "inf32":
		return token{Kind: tokenBytes, WireType: 5, Value: []byte{0x00, 0x00, 0x80, 0x7f}, Pos: s.pos}, nil
	case "-inf32":
		return token{Kind: tokenBytes, WireType: 5, Value: []byte{0x00, 0x00, 0x80, 0x7f}, Pos: s.pos}, nil
	case "inf64":
		return token{Kind: tokenBytes, WireType: 1, Value: []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xf0, 0x7f}, Pos: s.pos}, nil
	case "-inf64":
		return token{Kind: tokenBytes, WireType: 1, Value: []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xf0, 0xff}, Pos: s.pos}, nil
	}

	return token{}, fmt.Errorf("unrecognized symbol %q", symbol)
}

// exec is the main parser loop.
//
// The leftCurly argument, it not nil, represents the { that began the
// length-prefixed block we're currently executing. Because we need to encode
// the full extent of the contents of a {} before emitting the length prefix,
// this function calls itself with a non-nil leftCurly to encode it.
func (s *Scanner) exec(leftCurly *token) ([]byte, error) {
	var out []byte
	var lengthModifier *token
	inferredTypeIndex := -1
	for {
		token, err := s.next(&lengthModifier)
		if err != nil {
			return nil, err
		}
		if lengthModifier != nil && token.Kind != tokenLeftCurly {
			return nil, &ParseError{lengthModifier.Pos, errors.New("length modifier was not followed by '{' or varint")}
		}
		switch token.Kind {
		case tokenBytes:
			if inferredTypeIndex != -1 {
				out[inferredTypeIndex] |= byte(token.WireType)
				inferredTypeIndex = -1
			}

			if token.InferredType {
				inferredTypeIndex = len(out)
			}
			out = append(out, token.Value...)
		case tokenLongForm:
			lengthModifier = &token
		case tokenLeftCurly:
			if inferredTypeIndex != -1 {
				out[inferredTypeIndex] |= 2
				inferredTypeIndex = -1
			}

			child, err := s.exec(&token)
			if err != nil {
				return nil, err
			}
			var lengthOverride int
			if lengthModifier != nil {
				if lengthModifier.Kind == tokenLongForm {
					lengthOverride = lengthModifier.Length
				}
			}
			out = encodeVarint(out, uint64(len(child)), lengthOverride)
			out = append(out, child...)
			lengthModifier = nil
		case tokenRightCurly:
			if inferredTypeIndex != -1 {
				inferredTypeIndex = -1
			}

			if leftCurly != nil {
				return out, nil
			}
			return nil, &ParseError{token.Pos, errors.New("unmatched '}'")}
		case tokenEOF:
			if inferredTypeIndex != -1 {
				inferredTypeIndex = -1
			}

			if leftCurly == nil {
				return out, nil
			}
			return nil, &ParseError{leftCurly.Pos, errors.New("unmatched '{'")}
		default:
			panic(token)
		}
	}
}

// encodeVarint encodes a varint to dest.
//
// Unlike binary.PutUvarint, this function allows encoding non-minimal varints.
func encodeVarint(dest []byte, value uint64, longForm int) []byte {
	for value > 0x7f {
		dest = append(dest, byte(value&0x7f)|0x80)
		value >>= 7
	}
	dest = append(dest, byte(value))

	if longForm > 0 {
		dest[len(dest)-1] |= 0x80
		for longForm > 1 {
			dest = append(dest, 0x80)
			longForm--
		}
		dest = append(dest, 0x00)
	}

	return dest
}
