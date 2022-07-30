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

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/reflect/protoreflect"

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

	// Schema is a Descriptor that describes the message type we're expecting to
	// disassemble, if any.
	Schema protoreflect.MessageDescriptor
	// Prints field names, using Schema as the source of names.
	PrintFieldNames bool
	// Prints enum value names, using Schema as the source of names.
	PrintEnumNames bool
}

func Write(src []byte, opts WriterOptions) string {
	w := writer{WriterOptions: opts}
	w.Indent = 2
	w.MaxFolds = 3

	if opts.Schema != nil {
		w.descs.Push(opts.Schema)
	}

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
	text     *strings.Builder
	comments []string

	// indent is how much the *next* line should be indented compared to this
	// one.
	indent int
}

type group struct {
	number  uint64
	hasDesc bool
}

type writer struct {
	WriterOptions
	print.Printer

	groups print.Stack[group]
	descs  print.Stack[protoreflect.MessageDescriptor]
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

func (w *writer) decodeVarint(src []byte, fd protoreflect.FieldDescriptor) ([]byte, bool) {
	rest, value, extra, ok := decodeVarint(src)
	if !ok {
		return nil, false
	}
	src = rest

	if extra > 0 {
		w.Writef("long-form:%d ", extra)
	}

	ftype := protoreflect.Int64Kind
	if fd != nil {
		ftype = fd.Kind()
	}

	// Pick a deserialization based on the type. If the type doesn't really
	// make sense (like a double), we fall back on int64. We ignore 32-bit-ness:
	// everything is 64 bit here.
	switch ftype {
	case protoreflect.BoolKind:
		switch value {
		case 0:
			w.Write("false")
			return src, true
		case 1:
			w.Write("true")
			return src, true
		}
		fallthrough
	case protoreflect.Uint32Kind, protoreflect.Uint64Kind,
		protoreflect.Fixed32Kind, protoreflect.Fixed64Kind:
		w.Write(value)
	case protoreflect.Sint32Kind, protoreflect.Sint64Kind:
		// Undo ZigZag encoding, then print as signed.
		value = (value >> 1) ^ -(value & 1)
		w.Writef("%dz", int64(value))
	case protoreflect.EnumKind:
		if w.PrintEnumNames && value < math.MaxInt32 {
			ed := fd.Enum().Values()
			edv := ed.ByNumber(protoreflect.EnumNumber(value))
			if edv != nil {
				w.Remark(string(edv.Name()))
			}
		}
		fallthrough
	default:
		w.Write(int64(value))
	}

	return src, true
}

// decodeFixed prints out a single fixed-length value.
//
// This monster of a generic function exists to reduce keeping the two copies of
// 32-bit and 64-bit logic in sync.
func printFixed[
	U uint32 | uint64,
	I int32 | int64,
	F float32 | float64,
](
	w *writer,
	value U,
	suffix string,
	itof func(U) F,
	src []byte,
	fd protoreflect.FieldDescriptor,
) ([]byte, bool) {

	var ftype protoreflect.Kind
	if fd != nil {
		ftype = fd.Kind()
	}

	switch ftype {
	case protoreflect.Uint32Kind, protoreflect.Uint64Kind,
		protoreflect.Fixed32Kind, protoreflect.Fixed64Kind:
		w.Writef("%di%s", value, suffix)
	case protoreflect.EnumKind:
		if w.PrintEnumNames && value < math.MaxInt32 {
			ed := fd.Enum().Values()
			edv := ed.ByNumber(protoreflect.EnumNumber(value))
			if edv != nil {
				w.Remark(string(edv.Name()))
			}
		}
		fallthrough
	case protoreflect.Int32Kind, protoreflect.Int64Kind,
		protoreflect.Sint32Kind, protoreflect.Sint64Kind,
		protoreflect.Sfixed32Kind, protoreflect.Sfixed64Kind,
		protoreflect.BoolKind:
		w.Writef("%di%s", I(value), suffix)
	default:
		// Assume this is a float by default.
		fvalue := float64(itof(value))
		if math.IsInf(fvalue, 1) {
			w.Writef("inf%s", suffix)
		} else if math.IsInf(fvalue, -1) {
			w.Writef("-inf%s", suffix)
		} else if math.IsNaN(fvalue) {
			// NaNs always print as bits, because there are many NaNs.
			w.Writef("0x%xi%s", value, suffix)
		} else {
			if s := ftoa(value, ftype == protoreflect.DoubleKind || ftype == protoreflect.FloatKind); s != "" {
				// For floats, i64 is actually implied.
				if suffix == "64" {
					w.Write(s)
				} else {
					w.Writef("%si%s", s, suffix)
				}
				w.Remarkf("%#xi%s", U(value), suffix)
			} else {
				w.Writef("%di%s", I(value), suffix)
			}
		}
	}

	return src, true
}

func (w *writer) decodeI32(src []byte, fd protoreflect.FieldDescriptor) ([]byte, bool) {
	if len(src) < 4 {
		return nil, false
	}
	value := binary.LittleEndian.Uint32(src)
	src = src[4:]

	return printFixed[uint32, int32, float32](w, value, "32", math.Float32frombits, src, fd)
}

func (w *writer) decodeI64(src []byte, fd protoreflect.FieldDescriptor) ([]byte, bool) {
	if len(src) < 8 {
		return nil, false
	}
	value := binary.LittleEndian.Uint64(src)
	src = src[8:]

	return printFixed[uint64, int64, float64](w, value, "64", math.Float64frombits, src, fd)
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
	number := value >> 3
	w.Writef("%d:", number)

	var fd protoreflect.FieldDescriptor
	if d := w.descs.Peek(); d != nil && *d != nil {
		fd = (*d).Fields().ByNumber(protowire.Number(number))
	}

	if w.PrintFieldNames && fd != nil {
		w.Remark(fd.Name())
	}

	switch value & 0x7 {
	case 0:
		if w.ExplicitWireTypes {
			w.Write("VARINT")
		}
		w.Write(" ")
		return w.decodeVarint(src, fd)

	case 1:
		if w.ExplicitWireTypes {
			w.Write("I64")
		}
		w.Write(" ")
		return w.decodeI64(src, fd)

	case 5:
		if w.ExplicitWireTypes {
			w.Write("I32")
		}
		w.Write(" ")
		return w.decodeI32(src, fd)

	case 3:
		if fd != nil {
			w.descs.Push(fd.Message())
		}

		if w.ExplicitWireTypes || w.NoGroups {
			w.Write("SGROUP")
			w.StartBlock(print.BlockInfo{
				HasDelimiters:  false,
				HeightToFoldAt: 2,
				UnindentAt:     1,
			})
		} else {
			w.Write(" !{")
			w.StartBlock(print.BlockInfo{
				HasDelimiters:  true,
				HeightToFoldAt: 3,
				UnindentAt:     1,
			})
		}
		w.groups.Push(group{number, fd != nil})

	case 4:
		if len(w.groups) == 0 {
			w.Write("EGROUP")
		} else {
			lastGroup := w.groups.Pop()
			if lastGroup.hasDesc {
				_ = w.descs.Pop()
			}

			if lastGroup.number == number {
				if w.ExplicitWireTypes || w.NoGroups {
					w.Write("EGROUP")
				} else {
					w.Current().Reset()
					/*if w.PrintFieldNames && fd != nil {
						// Drop the field comment for this line.
						w.line(-1).comments = w.line(-1).comments[1:]
					}*/

					if extra > 0 {
						w.Writef("long-form:%d", extra)
						w.NewLine()
					}
					w.Write("}")
				}
				w.EndBlock()
			} else {
				w.resetGroup()
				w.Write("EGROUP")
			}
		}

	case 2:
		if w.ExplicitWireTypes || w.ExplicitLengthPrefixes {
			w.Write("LEN")
		}
		w.Write(" ")

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
			w.Writef("long-form:%d ", extra)
		}
		if w.ExplicitLengthPrefixes {
			w.Write(int64(value))
			w.StartBlock(print.BlockInfo{
				HasDelimiters:  false,
				HeightToFoldAt: 2,
				UnindentAt:     0,
			})
		} else {
			w.Write("{")
			w.StartBlock(print.BlockInfo{
				HasDelimiters:  true,
				HeightToFoldAt: 3,
				UnindentAt:     1,
			})
		}

		ftype := protoreflect.MessageKind
		if fd != nil {
			ftype = fd.Kind()
		}

		decodePacked := func(decode func([]byte, protoreflect.FieldDescriptor) ([]byte, bool)) {
			count := 0
			for ; ; count++ {
				w.NewLine()
				s, ok := decode(delimited, fd)
				if !ok {
					w.DiscardLine()
					break
				}
				delimited = s
			}

			w.FoldIntoColumns(8, count)
		}

		switch ftype {
		case protoreflect.BoolKind, protoreflect.EnumKind,
			protoreflect.Int32Kind, protoreflect.Int64Kind,
			protoreflect.Uint32Kind, protoreflect.Uint64Kind,
			protoreflect.Sint32Kind, protoreflect.Sint64Kind:
			decodePacked(w.decodeVarint)
			goto decodeBytes

		case protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind,
			protoreflect.FloatKind:
			decodePacked(w.decodeI32)
			goto decodeBytes

		case protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind,
			protoreflect.DoubleKind:
			decodePacked(w.decodeI64)
			goto decodeBytes

		case protoreflect.StringKind, protoreflect.BytesKind:
			goto decodeUtf8
		}

		// This is in a block so that the gotos can jump over the declarations
		// safely.
		{
			startLine := w.Mark()
			src2 := delimited
			outerGroups := w.groups
			w.groups = nil
			if fd != nil {
				w.descs.Push(fd.Message())
			}
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
			if fd != nil {
				w.descs.Pop()
			}

			// Order does not matter for fixing up unclosed groups
			for range w.groups {
				w.resetGroup()
			}
			w.groups = outerGroups

			// If we consumed all the bytes, we're done and can wrap up. However, if we
			// consumed *some* bytes, and the user requested unconditional message
			// parsing, we'll continue regardless. We don't bother in the case where we
			// failed at the start because the `...` case below will do a cleaner job.
			if len(src2) == 0 || (w.AllFieldsAreMessages && len(src2) < len(delimited)) {
				delimited = src2
				goto decodeBytes
			} else {
				w.Reset(startLine)
			}
		}

		// Otherwise, maybe it's a UTF-8 string.
	decodeUtf8:
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
				goto decodeBytes
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
			goto decodeBytes
		}

		// Who knows what it is? Bytes or something.
	decodeBytes:
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

func ftoa[I uint32 | uint64](bits I, floatForSure bool) string {
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

	if absExp >= bigExp && !floatForSure {
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
