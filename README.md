# Protoscope

*Protobuf + Rotoscope*

Protoscope is a simple, human-editable language for representing and emitting
the [Protobuf wire format](https://developers.google.com/protocol-buffers/docs/encoding).
It is inspired by, and is significantly based on,
[DER ASCII](https://github.com/google/der-ascii), a similar tool for working with
DER and BER, wire formats of ASN.1.

Unlike most Protobuf tools, it is completely ignorant of schemata specified in `.proto`
files; it has just enough knowledge of the wire format to provide primitives for 
constructing messages (such as field tags, varints, and length prefixes). A disassembler
is included that uses heuristics to try convert encoded Protobuf into Protoscope,
although the heuristics are necessarily imperfect.

We provide the Go package `github.com/protocolbuffers/protoscope`, as well as the `protoscope`
tool, which can be installed with the Go tool via

    go install github.com/protocolbuffers/protoscope/cmd/protoscope...@latest

These tools may be used to create test inputs by taking an existing proto,
dissassembling with `protoscope`, making edits, and then reassembling with
`protoscope -s`. This avoids having to manually fix up all the length prefixes.
They may also be used to inspect proto files (or things that look like them.)

For the language specification and basic examples, see [language.txt](/language.txt).
Example disassembly can be found under [./testdata](/testdata).

## Backwards compatibility

The Protoscope language itself may be extended over time, but the intention is
for extensions to be backwards-compatible. Specifically:

* The command-line interface to `protoscope` will remain compatible, though new
  options may be added in the future.

* Previously valid Protoscope will remain valid and produce the same output.
  In particular, checking in test data as Protoscope text should be future-proof.

* Previously invalid Protoscope may become valid in the future if
  the language is extended.

* Disassembly is necessarily a heuristic, so its output *may* change over time,
  but it is guaranteed to produce Protoscope output that will reassemble to the
  original byte string. `protoscope | protoscope -s` is always equivalent to
  `cat`.

## Disclaimer

This is not an official Google project.
