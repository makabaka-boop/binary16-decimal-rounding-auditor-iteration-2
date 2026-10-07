// Command halfconv converts a JSON batch of decimal strings to exact
// IEEE binary16 bit patterns.
//
// Usage:
//
//	halfconv [-with-intervals] < request.json
//	halfconv [-with-intervals] request.json
//	halfconv [-with-intervals] -    # read standard input explicitly
//
// The request is either {"values": ["0.1", "-2e3", ...]} or a bare
// JSON array of strings. The response lists the four-hex-digit bit
// pattern, the IEEE class (zero/subnormal/normal/infinity) and the
// exact reduced rounding fraction (null for infinities). Rejected
// entries appear with "ok": false and a reason; malformed requests
// exit non-zero.
//
// With -with-intervals (or "with_intervals": true in the request body)
// every successful entry additionally carries the exact reduced
// rational interval of input values that encode to that bit pattern,
// including endpoint inclusion under roundTiesToEven.
//
// A same-length "targets" array switches the request to exact common
// additive-bias calibration instead of per-item conversion.
package main

import (
	"flag"
	"fmt"
	"os"

	"halfconv/internal/app"
)

func main() {
	withIntervals := flag.Bool("with-intervals", false,
		"attach exact rounding-preimage interval certificates")
	flag.Parse()

	var in *os.File = os.Stdin
	switch flag.NArg() {
	case 0:
		// default: standard input
	case 1:
		if flag.Arg(0) != "-" {
			f, err := os.Open(flag.Arg(0))
			if err != nil {
				fmt.Fprintf(os.Stderr, "halfconv: %v\n", err)
				os.Exit(1)
			}
			defer f.Close()
			in = f
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: halfconv [-with-intervals] [batch.json | -]")
		os.Exit(2)
	}
	os.Exit(app.ExecuteWith(in, os.Stdout, *withIntervals))
}
