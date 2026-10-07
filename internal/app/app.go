// Package app wires decimal parsing and binary16 rounding into the
// JSON batch command line used by the "half" service.
package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"

	"halfconv/internal/decimal"
	"halfconv/internal/half"
)

// MinBatch / MaxBatch bound the number of decimals per request.
const (
	MinBatch = 1
	MaxBatch = 1000
)

// IntervalJSON is the optional exact rounding preimage certificate.
// Bounds are reduced "p/q" fractions; a null bound means the
// corresponding infinite tail (only used by overflow infinities).
type IntervalJSON struct {
	Low           json.RawMessage `json:"low"`
	High          json.RawMessage `json:"high"`
	LowInclusive  bool            `json:"low_inclusive"`
	HighInclusive bool            `json:"high_inclusive"`
}

// CalibResultJSON is one target pattern recomputed at the chosen bias.
type CalibResultJSON struct {
	Index int    `json:"index"`
	Hex   string `json:"hex"`
	Class string `json:"class"`
}

// CalibrationJSON is the verdict of one calibration batch. In
// calibration mode the response carries this instead of per-item
// results. Bounds and biases are reduced "p/q" fractions (null marks an
// infinity tail); nothing is ever rendered as an approximate decimal.
type CalibrationJSON struct {
	Feasible          bool              `json:"feasible"`
	Attained          *bool             `json:"attained,omitempty"`
	Bias              json.RawMessage   `json:"bias,omitempty"`
	Infimum           json.RawMessage   `json:"infimum,omitempty"`
	Witness           json.RawMessage   `json:"witness,omitempty"`
	Interval          *IntervalJSON     `json:"interval,omitempty"`
	Recomputed        []CalibResultJSON `json:"recomputed,omitempty"`
	WitnessRecomputed []CalibResultJSON `json:"witness_recomputed,omitempty"`
}

// Item is one element of the response.
type Item struct {
	Index int     `json:"index"`
	Input string  `json:"input"`
	OK    bool    `json:"ok"`
	Error *string `json:"error,omitempty"`
	Hex   *string `json:"hex,omitempty"`
	Class *string `json:"class,omitempty"`
	// RoundingError is the reduced fraction value(result)-value(input);
	// it is null for infinities and absent for rejected inputs.
	RoundingError json.RawMessage `json:"rounding_error,omitempty"`
	// Interval is present only when the request opts into exact
	// interval certificates ("with_intervals": true).
	Interval *IntervalJSON `json:"interval,omitempty"`
}

// Response is the batch result envelope.
type Response struct {
	Count       int              `json:"count"`
	Result      []Item           `json:"results,omitempty"`
	Calibration *CalibrationJSON `json:"calibration,omitempty"`
}

// Request is the accepted JSON envelope: {"values": ["1", "2"]}.
// WithIntervals is optional and defaults to false; when it is false the
// response is byte-for-byte compatible with the original output shape.
//
// Targets optionally carries one four-hex-digit binary16 bit pattern per
// value. Its presence switches the whole batch into calibration mode:
// the request is solved as one exact common-bias problem, and any
// invalid original, target or mismatched length rejects the entire
// request (no per-item results are emitted).
type Request struct {
	Values        []string  `json:"values"`
	WithIntervals bool      `json:"with_intervals,omitempty"`
	Targets       *[]string `json:"targets,omitempty"`
}

var (
	// ErrEmptyBatch is returned when no decimal strings were supplied.
	ErrEmptyBatch = errors.New("batch must contain between 1 and 1000 decimal strings")
	// ErrBatchTooLarge is returned for more than 1000 entries.
	ErrBatchTooLarge = errors.New("batch must contain between 1 and 1000 decimal strings")
)

// ConvertOne parses and rounds a single decimal string. A rejected
// input is reported with ok=false and is not an error at the Go level:
// batch processing keeps going.
func ConvertOne(index int, input string) Item {
	return ConvertOneWith(index, input, false)
}

// ratBound marshals a reduced rational bound as a "p/q" JSON string, or
// emits null for an infinite tail.
func ratBound(r *big.Rat, infinite bool) json.RawMessage {
	if infinite {
		return json.RawMessage("null")
	}
	raw, _ := json.Marshal(fmt.Sprintf("%d/%d", r.Num(), r.Denom()))
	return raw
}

// ConvertOneWith is ConvertOne with the optional interval certificate.
// When withIntervals is false the produced item is identical to
// ConvertOne's.
func ConvertOneWith(index int, input string, withIntervals bool) Item {
	item := Item{Index: index, Input: input, OK: false}
	neg, r, err := decimal.Parse(input)
	if err != nil {
		msg := err.Error()
		item.Error = &msg
		return item
	}

	b := half.Convert(neg, r)
	hex := fmt.Sprintf("%04X", b.Pattern())
	class := string(b.Class)
	item.OK = true
	item.Hex = &hex
	item.Class = &class

	if b.IsInf() {
		item.RoundingError = json.RawMessage("null")
	} else {
		e := half.Error(b, r)
		frac := fmt.Sprintf("%d/%d", e.Num(), e.Denom())
		raw, _ := json.Marshal(frac)
		item.RoundingError = raw
	}

	if withIntervals {
		iv := half.IntervalOf(b)
		item.Interval = &IntervalJSON{
			Low:           ratBound(iv.Low, iv.LowInfinite),
			High:          ratBound(iv.High, iv.HighInfinite),
			LowInclusive:  iv.LowInclusive,
			HighInclusive: iv.HighInclusive,
		}
	}
	return item
}

// Run validates the batch and converts every entry.
func Run(req Request) (Response, error) {
	return RunWith(req, false)
}

// RunWith is Run honoring an externally forced certificate mode (the
// command line -with-intervals flag). When neither the request nor the
// force bit enables certificates the output is unchanged. When the
// request supplies targets, the whole batch is solved as one exact
// common-bias calibration problem; any invalid original, target or
// mismatched length rejects the entire request.
func RunWith(req Request, forceIntervals bool) (Response, error) {
	n := len(req.Values)
	if n < MinBatch {
		return Response{}, ErrEmptyBatch
	}
	if n > MaxBatch {
		return Response{}, ErrBatchTooLarge
	}
	if req.Targets != nil {
		return runCalibration(n, req.Values, *req.Targets)
	}
	with := req.WithIntervals || forceIntervals
	resp := Response{Count: n, Result: make([]Item, n)}
	for i, v := range req.Values {
		resp.Result[i] = ConvertOneWith(i, v, with)
	}
	return resp, nil
}

// ErrTargetLength is returned when targets and values have different
// lengths in calibration mode.
var ErrTargetLength = errors.New("targets must have the same length as values")

// parseTarget decodes a four-hex-digit binary16 pattern, accepting
// finite values and infinities but rejecting NaN encodings and malformed
// or non-finite-looking input.
func parseTarget(s string) (half.Bits, error) {
	if len(s) != 4 {
		return half.Bits{}, fmt.Errorf("target %q: expected exactly four hex digits", s)
	}
	var p uint16
	for _, c := range s {
		var d byte
		switch {
		case c >= '0' && c <= '9':
			d = byte(c - '0')
		case c >= 'a' && c <= 'f':
			d = byte(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = byte(c-'A') + 10
		default:
			return half.Bits{}, fmt.Errorf("target %q: not a hex digit", s)
		}
		p = p<<4 | uint16(d)
	}
	b, ok := half.PatternBits(p)
	if !ok {
		return half.Bits{}, fmt.Errorf("target %q: NaN bit pattern is not accepted", s)
	}
	return b, nil
}

func ratJSON(r *big.Rat) json.RawMessage {
	raw, _ := json.Marshal(fmt.Sprintf("%d/%d", r.Num(), r.Denom()))
	return raw
}

func recomputeAll(originals []*big.Rat, negativeLiteral []bool, targets []half.Bits, bias *big.Rat) []CalibResultJSON {
	out := make([]CalibResultJSON, len(originals))
	for i := range originals {
		b := half.CalibratedConvert(negativeLiteral[i], originals[i], bias)
		out[i] = CalibResultJSON{
			Index: i,
			Hex:   fmt.Sprintf("%04X", b.Pattern()),
			Class: string(b.Class),
		}
	}
	return out
}

// runCalibration validates and solves one calibration batch. Unlike the
// ordinary mode it is all-or-nothing: one invalid original or target
// aborts the whole request, so no half suggestion can be produced.
func runCalibration(n int, values []string, targets []string) (Response, error) {
	if len(targets) != n {
		return Response{}, ErrTargetLength
	}

	originals := make([]*big.Rat, n)
	negativeLiteral := make([]bool, n)
	want := make([]half.Bits, n)
	for i, v := range values {
		neg, r, err := decimal.Parse(v)
		if err != nil {
			return Response{}, fmt.Errorf("values[%d]: %w", i, err)
		}
		b, err := parseTarget(targets[i])
		if err != nil {
			return Response{}, fmt.Errorf("targets[%d]: %w", i, err)
		}
		originals[i] = r
		negativeLiteral[i] = neg
		want[i] = b
	}

	cal := half.Calibrate(originals, negativeLiteral, want)
	out := CalibrationJSON{Feasible: cal.Feasible}
	if !cal.Feasible {
		return Response{Count: n, Calibration: &out}, nil
	}

	iv := cal.Interval
	out.Interval = &IntervalJSON{
		Low:           ratBound(iv.Low, iv.LowInfinite),
		High:          ratBound(iv.High, iv.HighInfinite),
		LowInclusive:  iv.LowInclusive,
		HighInclusive: iv.HighInclusive,
	}
	attained := cal.Attained
	out.Attained = &attained
	if attained {
		out.Bias = ratJSON(cal.Bias)
		out.Recomputed = recomputeAll(originals, negativeLiteral, want, cal.Bias)
	} else {
		// The infimum is an excluded endpoint, never a feasible bias;
		// report it as an exact fraction together with a strictly
		// interior feasible witness that does recompute every target.
		out.Infimum = ratJSON(cal.Infimum)
		out.Witness = ratJSON(cal.WitnessBias)
		out.WitnessRecomputed = recomputeAll(originals, negativeLiteral, want, cal.WitnessBias)
	}
	return Response{Count: n, Calibration: &out}, nil
}

// DecodeRequest accepts either {"values": [...]} or a bare JSON array.
// Non-string elements and malformed JSON produce an error.
func DecodeRequest(data []byte) (Request, error) {
	var req Request
	trimmed := leftTrimSpace(data)
	if len(trimmed) == 0 {
		return req, errors.New("empty JSON input")
	}
	if trimmed[0] == '[' {
		var raw []json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			return req, fmt.Errorf("invalid JSON array: %w", err)
		}
		req.Values = make([]string, len(raw))
		for i, el := range raw {
			if err := json.Unmarshal(el, &req.Values[i]); err != nil {
				return req, fmt.Errorf("values[%d] must be a JSON string: %w", i, err)
			}
		}
		return req, nil
	}
	if err := json.Unmarshal(data, &req); err != nil {
		return req, fmt.Errorf("invalid JSON request: %w", err)
	}
	return req, nil
}

func leftTrimSpace(b []byte) []byte {
	for len(b) > 0 && isSpace(b[0]) {
		b = b[1:]
	}
	return b
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// Execute reads a request from in, converts it and writes the indented
// JSON response to out. The returned int is the process exit code.
func Execute(in io.Reader, out io.Writer) int {
	return ExecuteWith(in, out, false)
}

// ExecuteWith is Execute with the interval-certificate force bit used
// by the command line flag.
func ExecuteWith(in io.Reader, out io.Writer, withIntervals bool) int {
	data, err := io.ReadAll(in)
	if err != nil {
		fmt.Fprintf(out, `{"ok":false,"error":%q}`+"\n", "cannot read input: "+err.Error())
		return 1
	}
	req, err := DecodeRequest(data)
	if err != nil {
		fmt.Fprintf(out, `{"ok":false,"error":%q}`+"\n", err.Error())
		return 1
	}
	resp, err := RunWith(req, withIntervals)
	if err != nil {
		fmt.Fprintf(out, `{"ok":false,"error":%q}`+"\n", err.Error())
		return 1
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	_ = enc.Encode(resp)
	return 0
}
