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

package protoscope

import (
	"encoding/binary"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/protocolbuffers/protoscope/internal/print"
)

// WriterOptions represents options that can be passed to control the writer's
// decoding heuristics.
type WriterOptions struct {
	// Disables treating any fields as containing UTF-8.
	NoQuotedStrings bool
	// Treats every length-prefixed field as being a message, printing hex if
	// an error is hit.
	AllFieldsAreMessages bool
	// Disables emitting !{}.
	NoGroups bool
	// Always prints the wire type of a field. Also disables !{} group syntax,
	// like NoGroups
	ExplicitWireTypes bool
	// Never prints {}; instead, prints out an explicit length prefix (but still
	// indents the contents of delimited things.
	ExplicitLengthPrefixes bool
}

func Write(src []byte, opts WriterOptions) string {
	w := writer{WriterOptions: opts}
	w.Indent = 2
	w.MaxFolds = 3

	for len(src) > 0 {
		w.NewLine()
		rest, ok := w.decodeField(src)
		if !ok {
			w.DiscardLine()
			break
		}
		src = rest
	}

	// Order does not matter for fixing up unclosed groups
	for _ = range w.groups {
		w.resetGroup()
	}

	w.dumpHexString(src)
	return string(w.Finish())
}

type line struct {
	text, comment *strings.Builder

	// indent is how much the *next* line should be indented compared to this
	// one.
	indent int
}

type writer struct {
	WriterOptions
	print.Printer

	groups print.Stack[uint64]
}

func (w *writer) dumpHexString(src []byte) {
	if len(src) == 0 {
		return
	}

	w.NewLine()
	w.Write("`")
	for i, b := range src {
		if i > 0 && i%40 == 0 {
			w.Write("`")
			w.NewLine()
			w.Write("`")
		}
		w.Writef("%02x", b)
	}
	w.Write("`")
}

func (w *writer) resetGroup() {
	// Do some surgery on the line with the !{ to replace it with an SGROUP.
	start := w.DropBlock()

	if !w.NoGroups {
		// Remove the trailing " !{"
		start.Truncate(start.Len() - 3)
		start.WriteString("SGROUP")
	}
}

func (w *writer) decodeField(src []byte) ([]byte, bool) {
	rest, value, extra, ok := decodeVarint(src)
	if !ok {
		return nil, false
	}
	src = rest

	// 0 is never a valid field number, so this probably isn't a message.
	if value>>3 == 0 && !w.AllFieldsAreMessages {
		return nil, false
	}

	if extra > 0 {
		w.Writef("long-form:%d ", extra)
	}
	w.Writef("%d:", value>>3)

	switch value & 0x7 {
	case 0:
		if w.ExplicitWireTypes {
			w.Writef("VARINT")
		}

		rest, value, extra, ok := decodeVarint(src)
		if !ok {
			return nil, false
		}
		src = rest

		if extra > 0 {
			w.Writef(" long-form:%d", extra)
		}
		w.Writef(" %d", int64(value))

	case 3:
		if w.ExplicitWireTypes || w.NoGroups {
			w.Writef("SGROUP")
			w.StartBlock(print.BlockInfo{
				HasDelimiters:  false,
				HeightToFoldAt: 2,
				UnindentAt:     1,
			})
		} else {
			w.Writef(" !{")
			w.StartBlock(print.BlockInfo{
				HasDelimiters:  true,
				HeightToFoldAt: 3,
				UnindentAt:     1,
			})
		}

		w.groups.Push(value >> 3)

	case 4:
		if len(w.groups) == 0 {
			w.Writef("EGROUP")
		} else {
			lastGroup := w.groups.Pop()
			if lastGroup == value>>3 {
				if w.ExplicitWireTypes || w.NoGroups {
					w.Writef("EGROUP")
				} else {
					w.Current().Reset()
					if extra > 0 {
						w.Writef("long-form:%d", extra)
						w.NewLine()
					}
					w.Writef("}")
				}
				w.EndBlock()
			} else {
				w.resetGroup()
				w.Writef("EGROUP")
			}
		}

	case 1:
		if w.ExplicitWireTypes {
			w.Writef("I64")
		}

		// Assume this is a float by default.
		if len(src) < 8 {
			return nil, false
		}
		bits := binary.LittleEndian.Uint64(src)
		src = src[8:]
		value := math.Float64frombits(bits)

		if math.IsInf(value, 1) {
			w.Write(" inf64")
		} else if math.IsInf(value, -1) {
			w.Write(" -inf64")
		} else if math.IsNaN(value) {
			w.Writef(" 0x%xi64", bits)
		} else {
			if s := ftoa(bits); s != "" {
				w.Writef(" %s", s)
				w.Remarkf("%#xi64", int64(bits))
			} else {
				w.Writef(" %di64", int64(bits))
			}
		}
	case 5:
		if w.ExplicitWireTypes {
			w.Writef("I32")
		}

		// Assume this is a float by default.
		if len(src) < 4 {
			return nil, false
		}
		bits := binary.LittleEndian.Uint32(src)
		src = src[4:]
		value := float64(math.Float32frombits(bits))

		if math.IsInf(value, 1) {
			w.Write(" inf32")
		} else if math.IsInf(value, -1) {
			w.Write(" -inf32")
		} else if math.IsNaN(value) {
			w.Writef(" 0x%xi32", bits)
		} else {
			if s := ftoa(bits); s != "" {
				w.Writef(" %si32", s)
				w.Remarkf("%#xi32", int32(bits))

			} else {
				w.Writef(" %di32", int32(bits))
			}
		}

	case 2:
		if w.ExplicitWireTypes || w.ExplicitLengthPrefixes {
			w.Writef("LEN")
		}

		rest, value, extra, ok := decodeVarint(src)
		if !ok {
			return nil, false
		}
		src = rest

		if uint64(len(src)) < value {
			return nil, false
		}

		delimited := src[:int(value)]
		src = src[int(value):]

		if extra > 0 {
			w.Writef(" long-form:%d", extra)
		}
		if w.ExplicitLengthPrefixes {
			w.Writef(" %d", int64(value))
			w.StartBlock(print.BlockInfo{
				HasDelimiters:  false,
				HeightToFoldAt: 2,
				UnindentAt:     0,
			})
		} else {
			w.Write(" {")
			w.StartBlock(print.BlockInfo{
				HasDelimiters:  true,
				HeightToFoldAt: 3,
				UnindentAt:     1,
			})
		}

		// First, assume this is a message.
		startLine := w.Mark()
		src2 := delimited
		outerGroups := w.groups
		w.groups = nil
		for len(src2) > 0 {
			w.NewLine()
			s, ok := w.decodeField(src2)
			if !ok {
				// Clip off an incompletely printed line.
				w.DiscardLine()
				break
			}
			src2 = s
		}

		// Order does not matter for fixing up unclosed groups
		for _ = range w.groups {
			w.resetGroup()
		}
		w.groups = outerGroups

		// If we consumed all the bytes, we're done and can wrap up. However, if we
		// consumed *some* bytes, and the user requested unconditional message
		// parsing, we'll continue regardless. We don't bother in the case where we
		// failed at the start because the `...` case below will do a cleaner job.
		if len(src2) == 0 || (w.AllFieldsAreMessages && len(src2) < len(delimited)) {
			delimited = src2
			goto justBytes
		} else {
			w.Reset(startLine)
		}

		// Otherwise, maybe it's a UTF-8 string.
		if !w.NoQuotedStrings && utf8.Valid(delimited) {
			runes := utf8.RuneCount(delimited)

			s := string(delimited)
			unprintable := 0
			for _, r := range s {
				if !unicode.IsGraphic(r) {
					unprintable++
				}
			}
			if float64(unprintable)/float64(runes) > 0.3 {
				goto justBytes
			}

			w.NewLine()
			w.Write("\"")
			for i, r := range s {
				if i != 0 && i%80 == 0 {
					w.Write("\"")
					w.NewLine()
					w.Write("\"")
				}

				switch r {
				case '\n':
					w.Write("\\n")
				case '\\':
					w.Write("\\\\")
				case '"':
					w.Write("\\\"")
				default:
					if !unicode.IsGraphic(r) {
						enc := make([]byte, 4)
						enc = enc[:utf8.EncodeRune(enc, r)]
						for _, b := range enc {
							w.Writef("\\x%02x", b)
						}
					} else {
						w.Writef("%c", r)
					}
				}
			}
			w.Write("\"")
			delimited = nil
			goto justBytes
		}

		// Who knows what it is? Bytes or something.
	justBytes:
		w.dumpHexString(delimited)
		if !w.ExplicitLengthPrefixes {
			w.NewLine()
			w.Write("}")
		}
		w.EndBlock()
	case 6, 7:
		return nil, false
	}
	return src, true
}

func ftoa[I uint32 | uint64](bits I) string {
	var mantLen, expLen, bitLen int
	var value float64
	switch b := any(bits).(type) {
	case uint32:
		bitLen = 32
		expLen = 8
		value = float64(math.Float32frombits(b))
	case uint64:
		bitLen = 64
		expLen = 11
		value = math.Float64frombits(b)
	}
	mantLen = bitLen - expLen - 1

	if bits == 0 {
		return "0.0"
	} else if bits == 1<<(bitLen-1) {
		return "-0.0"
	}

	exp := int64((bits >> mantLen) & ((1 << expLen) - 1))
	exp -= (1 << (expLen - 1)) - 1
	absExp := exp
	if absExp < 0 {
		absExp = -absExp
	}
	bigExp := int64(1)<<(expLen-1) - 1

	if absExp >= bigExp {
		// Very large or very small exponents indicate this probably isn't actually
		// a float.
		return ""
	}

	// Only print floats in decimal if it can be round-tripped.
	decimal := strconv.FormatFloat(value, 'g', -1, bitLen)

	roundtrip, _ := strconv.ParseFloat(decimal, bitLen)
	var bits2 I
	switch any(bits).(type) {
	case uint32:
		bits2 = I(math.Float32bits(float32(roundtrip)))
	case uint64:
		bits2 = I(math.Float64bits(roundtrip))
	}

	if bits2 != bits {
		decimal = strconv.FormatFloat(value, 'x', -1, bitLen)
	}

	// Discard a + after the exponent.
	decimal = strings.Replace(decimal, "+", "", -1)

	// Insert a decimal point if necessary.
	if !strings.Contains(decimal, ".") {
		if strings.Contains(decimal, "e") {
			decimal = strings.Replace(decimal, "e", ".0e", -1)
		} else {
			decimal += ".0"
		}
	}

	return decimal
}

func decodeVarint(src []byte) (rest []byte, value uint64, extraBytes int, ok bool) {
	count := 0
	for {
		if len(src) == 0 {
			ok = false
			return
		}

		var b byte
		b, src = src[0], src[1:]
		if count == 9 && b > 1 {
			// The tenth byte has a special upper limit: it may only be 0 or 1.
			ok = false
			return
		}

		value |= uint64(b&0x7f) << (count * 7)
		count++

		if b&0x7f == 0 {
			extraBytes++
		} else {
			extraBytes = 0
		}

		if b&0x80 == 0 {
			break
		}
	}

	if value == 0 {
		extraBytes--
	}
	rest = src
	ok = true
	return
}
