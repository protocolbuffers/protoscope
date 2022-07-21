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
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func num2le(x any) (b []byte) {
	switch i := x.(type) {
	case float32:
		b = make([]byte, 4)
		binary.LittleEndian.PutUint32(b, math.Float32bits(i))
	case float64:
		b = make([]byte, 8)
		binary.LittleEndian.PutUint64(b, math.Float64bits(i))
	default:
		panic(fmt.Sprintf("int2le: unsupported: '%s'", reflect.TypeOf(x)))
	}

	return
}

func concat(chunks ...any) (out []byte) {
	for _, c := range chunks {
		switch x := c.(type) {
		case int:
			out = append(out, byte(x))
		case []byte:
			out = append(out, x...)
		case string:
			out = append(out, []byte(x)...)
		}
	}

	return
}

func TestScan(t *testing.T) {
	tests := []struct {
		name, text string
		// If `output` is `nil`, expects scanning to fail.
		want []byte
	}{
		{
			name: "empty",
			text: "",
			want: []byte{},
		},
		{
			name: "comment",
			text: "#hello",
			want: []byte{},
		},
		{
			name: "comment with content",
			text: "#hello\n`abcd`",
			want: []byte{0xab, 0xcd},
		},
		{
			text: "garbage",
		},

		{
			name: "empty hex",
			text: "``",
			want: []byte{},
		},
		{
			name: "hex",
			text: "`0123456789abcdefABCDEFAbCdEfaBcDeF0a1b3c4d5e6f`",
			want: []byte{
				0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef,
				0xAB, 0xCD, 0xEF, 0xAb, 0xCd, 0xEf, 0xaB, 0xcD, 0xeF,
				0x0a, 0x1b, 0x3c, 0x4d, 0x5e, 0x6f,
			},
		},
		{
			name: "broken hex",
			text: "`abcd",
		},
		{
			name: "single hex",
			text: "`a`",
		},
		{
			name: "odd hex",
			text: "`abc`",
		},
		{
			name: "non-hex in hex",
			text: "`bear`",
		},

		{
			name: "empty quotes",
			text: `""`,
			want: []byte{},
		},
		{
			name: "quotes",
			text: `"hello!"`,
			want: []byte("hello!"),
		},
		{
			name: "quotes concat",
			text: `"hello," " world!"`,
			want: []byte("hello, world!"),
		},
		{
			name: "quotes with non-latin",
			text: `"施氏食獅史🐈‍⬛🖤"`,
			want: []byte("施氏食獅史🐈‍⬛🖤"),
		},
		{
			name: "quotes with escapes",
			text: `"\\\"\ntext\x00\xff"`,
			want: []byte("\\\"\ntext\x00\xff"),
		},
		{
			name: "quotes with whitespace",
			text: `"  
			       "`,
			want: []byte("  \n\t\t\t       "),
		},
		{
			name: "broken quotes",
			text: `"hello!`,
		},
		{
			name: "broken quotes by escape",
			text: `"hello!\"`,
		},
		{
			name: "bad escape",
			text: `"\a"`,
		},

		{
			name: "zero",
			text: "0",
			want: []byte{0x00},
		},
		{
			name: "minus zero",
			text: "-0",
			want: []byte{0x00},
		},
		{
			name: "long-form:0 zero",
			text: "long-form:0 0",
			want: []byte{0x00},
		},
		{
			name: "long zero",
			text: "long-form:5 0",
			want: []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x00},
		},

		{
			name: "one byte",
			text: "42",
			want: []byte{42},
		},
		{
			name: "three byte",
			text: "100000",
			want: []byte{0xa0, 0x8d, 0x06},
		},
		{
			name: "ten byte",
			text: "-1",
			want: []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01},
		},
		{
			name: "one hex byte",
			text: "0x5a",
			want: []byte{0x5a},
		},
		{
			name: "two hex byte",
			text: "0xa5",
			want: []byte{0xa5, 0x01},
		},
		{
			name: "one zig",
			text: "-1z",
			want: []byte{0x01},
		},
		{
			name: "zig 42",
			text: "42z",
			want: []byte{42 * 2},
		},
		{
			name: "long answer",
			text: "long-form:5 -42z",
			want: []byte{
				0xd3, 0x80, 0x80, 0x80, 0x80, 0x00,
			},
		},

		{
			name: "long eof",
			text: "long-form:5",
		},
		{
			name: "double long",
			text: "long-form:3 long-form:4 5",
		},
		{
			name: "negative long",
			text: "long-form:-3 5",
		},
		{
			name: "hex long",
			text: "long-form:0x3 5",
		},

		{
			name: "int too big",
			text: "18446744073709551616",
		},
		{
			name: "negative int too big",
			text: "-9223372036854775809",
		},

		{
			name: "fixed32",
			text: "0xaaai32",
			want: []byte{
				0xaa, 0x0a, 0x00, 0x00,
			},
		},
		{
			name: "-fixed32",
			text: "-0xaaai32",
			want: []byte{
				0x56, 0xf5, 0xff, 0xff,
			},
		},
		{
			name: "fixed64",
			text: "0xaaai64",
			want: []byte{
				0xaa, 0x0a, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			},
		},
		{
			name: "-fixed64",
			text: "-0xaaai64",
			want: []byte{
				0x56, 0xf5, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
			},
		},
		{
			name: "biggest fixed",
			text: `
				18446744073709551615i64
				-9223372036854775808i64
				4294967295i32
				-2147483648i32
			`,
			want: []byte{
				0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
				0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x80,
				0xff, 0xff, 0xff, 0xff,
				0x00, 0x00, 0x00, 0x80,
			},
		},
		{
			name: "fixed32 too big",
			text: "4294967296i32",
		},
		{
			name: "fixed32 too small",
			text: "-2147483649i32",
		},
		{
			name: "long fixed",
			text: "long-form:1 1i32",
		},

		{
			name: "bools",
			text: "true false",
			want: []byte{1, 0},
		},

		{
			name: "fp zero",
			text: "0.0 -0.0 0.0i32 -0.0i32",
			want: concat(
				num2le(0.0),
				// Fun fact! -0.0 as a Go constant is *not* IEEE -0.0!
				num2le(math.Copysign(0, -1)),
				num2le(float32(0.0)),
				num2le(float32(math.Copysign(0, -1))),
			),
		},
		{
			name: "infinity",
			text: "inf64 -inf64 inf32 -inf32",
			want: concat(
				num2le(math.Inf(1)),
				num2le(math.Inf(-1)),
				num2le(float32(math.Inf(1))),
				num2le(float32(math.Inf(-1))),
			),
		},

		{
			name: "plank",
			text: "6.62607015e-34",
			want: num2le(6.62607015e-34),
		},
		{
			name: "speed of light",
			text: "-3.0e9i32",
			want: num2le(float32(-3e9)),
		},

		{
			name: "hex floats",
			text: `
				-0xf.0
				0xabcd.efp-10
				0x1.8p5i32
			`,
			want: concat(
				num2le(-0xf.0p0),
				num2le(0xabcd.efp-10),
				num2le(float32(0x1.8p5)),
			),
		},

		{
			name: "oct null",
			text: "\"\\0\"",
			want: []byte{
				0x00,
			},
		},
		{
			name: "oct null 8",
			text: "\"\\08\"",
			want: []byte{
				0x00, 0x38,
			},
		},
		{
			name: "oct double",
			text: "\"\\13\"",
			want: []byte{
				0x0b,
			},
		},
		{
			name: "oct double X",
			text: "\"\\13" + "X\"",
			want: []byte{
				0x0b, 0x58,
			},
		},
		{
			name: "oct Y double",
			text: "\"Y" + "\\13\"",
			want: []byte{
				0x59, 0x0b,
			},
		},
		{
			name: "oct triple",
			text: "\"\\007\"",
			want: []byte{
				0x07,
			},
		},
		{
			name: "oct WoW",
			text: "\"\\127o\\127\"",
			want: []byte{
				0x57, 0x6f, 0x57,
			},
		},

		{
			name: "oct hex oct",
			text: "\"\\127\\x40\\127\"",
			want: []byte{
				0x57, 0x40, 0x57,
			},
		},

		{
			name: "no fraction float",
			text: "1.",
		},
		{
			name: "no fraction float w/ exponent",
			text: "1e1",
		},
		{
			name: "plus exponent",
			text: "1.0e+1",
		},
		{
			name: "long float",
			text: "long-form:1 1.0",
		},
		{
			name: "float64 too big",
			text: "1.7976931348623157e309",
		},
		{
			name: "float32 too big",
			text: "3.40282347e39i32",
		},

		{
			name: "tags",
			text: `
				1:VARINT  # A varint.
				2:I64     # A fixed-width, 64-bit blob.
				3:LEN     # A length-prefixed blob.
				4:SGROUP  # A start-group marker.
				5:EGROUP  # An end-group marker.
				6:I32     # A fixed-width, 32-bit blob.
			`,
			want: []byte{
				1<<3 | 0,
				2<<3 | 1,
				3<<3 | 2,
				4<<3 | 3,
				5<<3 | 4,
				6<<3 | 5,
			},
		},
		{
			name: "unusual field numbers",
			text: "-5:6 9z:7 0x22:1 0:0",
			want: []byte{
				0xde, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01,
				0x9e, 0x01,
				0x91, 0x02,
				0x00,
			},
		},

		{
			name: "max field number",
			text: "0x1fffffffffffffff:0",
			want: []byte{
				0xf8, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01,
			},
		},
		{
			name: "bad named wire type",
			text: "1:LMAO",
		},
		{
			name: "wire type not a u3",
			text: "1:8",
		},
		{
			name: "field number too big",
			text: "0x2000000000000000:0",
		},

		{
			name: "wire type inference",
			text: `
				1: 42z
				22: {}
				333: 42i32
				4444: -42i64
				55555: 42.0i32
				666666: 0x42.0
				7777777: inf64
			`,
			want: []byte{
				0x08, 0x54,
				0xb2, 0x01, 0x00,
				0xed, 0x14, 0x2a, 0x00, 0x00, 0x00,
				0xe1, 0x95, 0x02, 0xd6, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
				0x9d, 0x90, 0x1b, 0x00, 0x00, 0x28, 0x42,
				0xd1, 0xc2, 0xc5, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0x80, 0x50, 0x40,
				0x89, 0xdf, 0xd5, 0x1d, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xf0, 0x7f,
			},
		},
		{
			name: "long-form inference",
			text: `
				1: long-form:2 5
				2: long-form:3 {}
			`,
			want: []byte{
				0x08, 0x85, 0x80, 0x00,
				0x12, 0x80, 0x80, 0x80, 0x00,
			},
		},
		{
			name: "eof inference",
			text: "1:",
			want: []byte{0x08},
		},

		{
			name: "string field",
			text: `23: {"my cool string"}`,
			want: concat(
				0xba, 0x01, 14,
				"my cool string",
			),
		},
		{
			name: "message field",
			text: `24: {
				1: 5
				2: {"nested string"}
			}`,
			want: concat(
				0xc2, 0x01, 0x11,
				0x08, 0x05,
				0x12, 0x0d,
				"nested string",
			),
		},
		{
			name: "repeated varints",
			text: `25: { 1 2 3 4 5 6 7 }`,
			want: []byte{
				0xca, 0x01, 0x07,
				1, 2, 3, 4, 5, 6, 7,
			},
		},
		{
			name: "long prefix",
			text: `23: long-form:2 {"non-minimally-prefixed"}`,
			want: concat(
				0xba, 0x01, 0x96, 0x80, 0x00,
				"non-minimally-prefixed",
			),
		},

		{
			name: "unclosed prefix",
			text: "{",
		},
		{
			name: "unclosed group",
			text: "1: !{",
		},
		{
			name: "unopened prefix",
			text: "}",
		},
		{
			name: "long end-of-prefix",
			text: "{long-form:2}",
		},

		{
			name: "empty group",
			text: "1: !{}",
			want: []byte{0x0b, 0x0c},
		},
		{
			name: "group with stuff",
			text: `5: !{1: 5 "foo"}`,
			want: concat(
				0x2b,
				0x08, 0x05,
				"foo",
				0x2c,
			),
		},
		{
			name: "nested groups",
			text: `1:!{2:!{3:!{"lmao"}}}`,
			want: concat(
				0x0b,
				0x13,
				0x1b,
				"lmao",
				0x1c,
				0x14,
				0x0c,
			),
		},

		{
			name: "nested groups and length prefixes",
			text: `1:!{2:{3:!{{"lmao"}}}}`,
			want: concat(
				0x0b,
				0x12, 0x07,
				0x1b,
				0x04, "lmao",
				0x1c,
				0x0c,
			),
		},

		{
			name: "bare group",
			text: "!{}",
		},
		{
			name: "typed group",
			text: "1:SGROUP !{}",
		},

		{
			name: "language.txt",
			text: LanguageTxt,
			want: concat(
				"Quoted strings are delimited by double quotes. Backslash denotes escape\n",
				"sequences. Legal escape sequences are: \\ \" \x00 \000 \n. \x00 consumes two\n",
				"hex digits and emits a byte. \000 consumes one to three octal digits and emits\n",
				"a byte. Otherwise, any byte before the closing quote, including a newline, is\n",
				"emitted as-is.",

				"hello world",
				"hello world",

				0x00, 0xab, 0xcd, 0xef, 0xab, 0xcd, 0xef,
				0xc8, 0x03,
				0x81, 0x80, 0xfc, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01,
				0x03, 0x03,

				0x00, 0x00, 0x00, 0x00,
				0xe9, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,

				0x83, 0x80, 0x80, 0x00,
				0x83, 0x80, 0x80, 0x00,

				0x01,
				0x00,

				num2le(1.0),
				num2le(9.423e-2),
				num2le(-0x1.ffp52),
				num2le(float32(1.5)),
				num2le(0xf.fp0),
				num2le(float32(math.Inf(1))),
				num2le(math.Inf(-1)),

				0x08, 0x11, 0x1a, 0x23, 0x2c, 0x35,
				0x80, 0x01,
				0x46,

				0x08, 55*2,
				0x11, num2le(1.23),
				0x1a, 0x04, "text",
				0x35, 0xff, 0xff, 0xff, 0xff,
				0x43, 42, 0x44,

				0xba, 0x01, 14, "my cool string",

				0xc2, 0x01, 0x11,
				0x08, 0x05,
				0x12, 0x0d, "nested string",

				0xca, 0x01, 0x07, 1, 2, 3, 4, 5, 6, 7,

				0xba, 0x01, 0x96, 0x80, 0x00, "non-minimally-prefixed",

				0xd3, 0x01,
				0x08, 0x6e,
				0x11, 0x66, 0x66, 0x66, 0x66, 0x66, 0x66, 0xf6, 0x3f,
				0x1a, 0x04, "abcd",
				0xd4, 0x01,

				0xdb, 0x01,
				0xdc, 0x81, 0x80, 0x80, 0x00,

				0x12, 0x04, "abcd",
				0x12, 0x05, "abcd",
				0x29, "stuff",
			),
		},
	}

	for _, tt := range tests {
		if tt.name == "" {
			tt.name = fmt.Sprintf("%q", tt.text)
		}
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewScanner(tt.text).Exec()
			if got == nil {
				got = []byte{}
			}

			if tt.want == nil {
				if err == nil {
					t.Fatal("expected an error but didn't get one")
				}
			} else if err != nil {
				t.Fatal("unexpected error", err)
			} else if d := cmp.Diff(tt.want, got); d != "" {
				t.Fatal("output mismatch (-want, +got):", d)
			}
		})
	}
}
