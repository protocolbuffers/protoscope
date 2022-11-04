# Protoscope

*Protobuf + Rotoscope*

Protoscope is a simple, human-editable language for representing and emitting
the
[Protobuf wire format](https://developers.google.com/protocol-buffers/docs/encoding).
It is inspired by, and is significantly based on,
[DER ASCII](https://github.com/google/der-ascii), a similar tool for working
with DER and BER, wire formats of ASN.1.

Unlike most Protobuf tools, it is normally ignorant of schemata specified in
`.proto` files; it has just enough knowledge of the wire format to provide
primitives for constructing messages (such as field tags, varints, and length
prefixes). A disassembler is included that uses heuristics to try convert
encoded Protobuf into Protoscope, although the heuristics are necessarily
imperfect.

We provide the Go package `github.com/protocolbuffers/protoscope`, as well as
the `protoscope` tool, which can be installed with the Go tool via

```
go install github.com/protocolbuffers/protoscope/cmd/protoscope...@latest
```

`go install` will place the binary in the `GOBIN` directory, which is
`~/go/bin` by default. See the [docs for `go install`](https://pkg.go.dev/cmd/go#hdr-Compile_and_install_packages_and_dependencies) for more details.

For the language specification and basic examples, see
[language.txt](/language.txt). Example disassembly can be found under
[./testdata](/testdata).

## Cookbook

Protoscope can be used in a number of different ways to inspect or create binary
Protobuf data. This isn't the full breadth of usecases, but they are the ones
Protoscope (and its ancestor, DER ASCII) were designed for.

### Exploring Binary Dumps

Sometimes, while working on a library that emits wire format, it may be
necessary to debug the precise output of a test failure. If your test prints out
a hex string, you can use the `xxd` command to turn it into raw binary data and
pipe it into `protoscope`.

Consider the following example of a message with a `google.protobuf.Any` field:

```sh
$ cat hexdata.txt
0a400a26747970652e676f6f676c65617069732e636f6d2f70726f746f332e546573744d65737361676512161005420e65787065637465645f76616c756500000000
$ xxd -r -ps hexdata.txt | protoscope
1: {
  1: {"type.googleapis.com/proto3.TestMessage"}
  2: {`1005420e65787065637465645f76616c756500000000`}
}
$ xxd -r -ps <<< "1005420e65787065637465645f76616c756500000000" | protoscope
2: 5
8: {"expected_value"}
`00000000`
```

This reveals that four zero bytes sneaked into the output!

If your test failure output is made up of C-style escapes and text, the `printf`
command can be used instead of `xxd`:

```sh
$ printf '\x10\x05B\x0eexpected_value\x00\x00\x00\x00' | protoscope
2: 5
8: {"expected_value"}
`00000000`
```

The `protoscope` command has many flags for refining the heuristic used to
decode the binary.

If an encoded `FileDescriptorSet` proto is available that contains your
message's type, you can use it to get schema-aware decoding:

```sh
$ cat hexdata.txt
086510661867206828d20130d4013d6b000000416c000000000000004d6d000000516e000000000000005d0000de42610000000000005c40680172033131357a0331313683018801758401
$ xxd -r -ps hexdata.txt | protoscope \
  -descriptor-set path/to/fds.pb -message-type unittest.TestAllTypes \
  -print-field-names
1: 101        # optional_int32
2: 102        # optional_int64
3: 103        # optional_uint32
4: 104        # optional_uint64
5: 105z       # optional_sint32
6: 106z       # optional_sint64
7: 107i32     # optional_fixed32
8: 108i64     # optional_fixed64
9: 109i32     # optional_sfixed32
10: 110i64    # optional_sfixed64
11: 111.0i32  # optional_float, 0x42de0000i32
12: 112.0     # optional_double, 0x405c000000000000i64
13: true      # optional_bool
14: {"115"}   # optional_string
15: {"116"}   # optional_bytes
16: !{        # optionalgroup
  17: 117     # a
}
```

You can get an encoded `FileDescriptorSet` by invoking

```sh
protoc -Ipath/to/imported/protos -o my_fds.pb my_proto.proto
```

### Modifying Existing Files

Suppose that we have a proto file `foo.bin` of unknown schema:

```sh
$ protoscope foo.bin
1: 42
2: {
  42: {"my awesome proto"}
}
```

Modifying the embedded string with a hex editor is very painful, because it's
possible that the length prefix needs to be updated, which can lead to the
length prefix on outer messages needing to be changed as well. This is made
worse by length prefixes being varints, which may grow or shrink and feed into
further outer length prefix updates.

But `protoscope` makes this into a simple disassemble, edit, assembly loop:

```sh
$ xxd foo.bin
00000000: 082a 1213 d202 106d 7920 6177 6573 6f6d  .*.....my awesom
00000010: 6520 7072 6f74 6f                        e proto

$ protoscope foo.bin > foo.txt  # Disassemble.
$ cat foo.txt
1: 42
2: {
  42: {"my awesome proto"}
}

$ vim foo.txt  # Make some edits.
$ cat foo.txt
1: 43
2: {
  42: {"my even more awesome awesome proto"}
}

$ protoscope -s foo.txt > foo.bin  # Reassemble.
$ xxd foo.bin
00000000: 082b 1225 d202 226d 7920 6576 656e 206d  .+.%.."my even m
00000010: 6f72 6520 6177 6573 6f6d 6520 6177 6573  ore awesome awes
00000020: 6f6d 6520 7072 6f74 6f                   ome proto
```

The `-message-type` option from above can be used when you know the schema to
make it easier to find specific fields.

### Describing Invalid Binaries

Because Protoscope has a very weak understanding of Protobuf, it can be used to
create invalid encodings to verify that some invariant is actually checked by a
production parser.

For example, the following Protoscope text can be used to create a test that
ensures a too-long length prefix is rejected as invalid.

```
1: {
  2:LEN 5   # Explicit length prefix.
    "oops"  # One byte too short.
}
```

This is more conveinent than typing out bytes by hand, because Protoscope takes
care of tedious details like length prefixes, varint encoding, float encoding,
and other things not relevant to the test. It also permits comments, which can
be used to specify why the Protoscope snippet produces a broken binary.

Protoscope itself generates test data using Protoscope, which is then checked
in. Other projects can either check in binary data directly, or use the build
system to invoke `protoscope`, such as with a Bazel `genrule()`.

## Backwards Compatibility

The Protoscope language itself may be extended over time, but the intention is
for extensions to be backwards-compatible. Specifically:

*   The command-line interface to `protoscope` will remain compatible, though
    new options may be added in the future.

*   Previously valid Protoscope will remain valid and produce the same output.
    In particular, checking in test data as Protoscope text should be
    future-proof.

*   Previously invalid Protoscope may become valid in the future if the language
    is extended.

*   Disassembly is necessarily a heuristic, so its output *may* change over
    time, but it is guaranteed to produce Protoscope output that will reassemble
    to the original byte string. `protoscope | protoscope -s` is always
    equivalent to `cat`.

## Disclaimer

This is not an official Google project.
