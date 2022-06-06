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


package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	_ "embed"

	"github.com/google/protoscope"
)

var (
	outPath = flag.String("o", "", "output file to use (defaults to stdout)")
	assemble = flag.Bool("s", false, "whether to treat the input as a Protoscope source file")

	noQuotedStrings = flag.Bool("no-quoted-strings", false, "assume no fields in the input proto are strings")
	allFieldsAreMessages = flag.Bool("all-fields-are-messages", false, "try really hard to disassemble all fields as messages")
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [OPTION...] [INPUT]\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Assemble a Protoscope file to binary, or inspect binary data as Protoscope text.\n\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\n%s\n", protoscope.LanguageText)
	}

	flag.Parse()

	if flag.NArg() > 1 {
		flag.Usage()
		os.Exit(1)
	}

	inPath := ""
	inFile := os.Stdin
	if flag.NArg() == 1 {
		inPath = flag.Arg(0)
		var err error
		inFile, err = os.Open(inPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening %s: %s\n", inPath, err)
			os.Exit(1)
		}
		defer inFile.Close()
	}

	inBytes, err := io.ReadAll(inFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading input: %s\n", err)
		os.Exit(1)
	}

	var outBytes []byte
	if *assemble {
		scanner := protoscope.NewScanner(string(inBytes))
		scanner.SetFile(inPath)

		outBytes, err = scanner.Exec()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Syntax error: %s\n", err)
			os.Exit(1)
		}
	} else {
		outBytes = []byte(protoscope.Write(inBytes, protoscope.WriterOptions{
			NoQuotedStrings: *noQuotedStrings,
			AllFieldsAreMessages: *allFieldsAreMessages,
		}))
	}

	outFile := os.Stdout
	if *outPath != "" {
		var err error
		outFile, err = os.Create(*outPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening %s: %s\n", *outPath, err)
			os.Exit(1)
		}
		defer outFile.Close()
	}
	
	if _, err = outFile.Write(outBytes); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing output: %s\n", err)
		os.Exit(1)
	}
}
