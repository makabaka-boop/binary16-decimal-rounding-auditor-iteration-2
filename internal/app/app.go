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
	// Target is present only in calibration mode and records the
	// requested binary16 pattern for this entry.
	Target *string `json:"target,omitempty"`
}

// CalibrationJSON is the exact common-bias solution. Infeasible requests
// carry only {"feasible": false}.
type CalibrationJSON struct {
	Feasible bool `json:"feasible"`
	// Interval is the exact set of acceptable biases.
	Interval *IntervalJSON `json:"interval,omitempty"`
	// Bias is the signed chosen minimum-absolute-value bias; it is
	// absent together with Results when that minimum is an excluded
	// endpoint.
	Bias json.RawMessage `json:"bias,omitempty"`
	// MinimumAbsoluteBias is the minimum when attained, otherwise the
	// exact unattained infimum.
	MinimumAbsoluteBias json.RawMessage `json:"minimum_absolute_bias,omitempty"`
	MinimumAttainable   *bool           `json:"minimum_attainable,omitempty"`
	// Results is the recalculation using the chosen attainable bias.
	Results []Item `json:"results,omitempty"`
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
type Request struct {
	Values []string `json:"values"`
	// Targets selects calibration mode when present. It must either be
	// absent/null or have exactly one legal finite/infinity binary16 hex
	// pattern per value.
	Targets       *[]string `json:"targets,omitempty"`
	WithIntervals bool      `json:"with_intervals,omitempty"`
}

var (
	// ErrEmptyBatch is returned when no decimal strings were supplied.
	ErrEmptyBatch = errors.New("batch must contain between 1 and 1000 decimal strings")
	// ErrBatchTooLarge is returned for more than 1000 entries.
	ErrBatchTooLarge = errors.New("batch must contain between 1 and 1000 decimal strings")
	// ErrTargetLength is returned when targets is not the same length
	// as values.
	ErrTargetLength = errors.New("targets must contain exactly one hexadecimal binary16 pattern per value")
	// ErrInvalidTarget is returned for a target that is not a legal
	// finite or infinity binary16 pattern.
	ErrInvalidTarget = errors.New("target must be a four-hex-digit finite or infinity binary16 pattern")
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

func ratJSON(r *big.Rat) json.RawMessage {
	raw, _ := json.Marshal(fmt.Sprintf("%d/%d", r.Num(), r.Denom()))
	return raw
}

func intervalJSON(iv half.Interval) *IntervalJSON {
	return &IntervalJSON{
		Low:           ratBound(iv.Low, iv.LowInfinite),
		High:          ratBound(iv.High, iv.HighInfinite),
		LowInclusive:  iv.LowInclusive,
		HighInclusive: iv.HighInclusive,
	}
}

// parseTarget accepts exactly four hexadecimal digits and rejects NaN
// encodings. Both upper- and lower-case digits are accepted.
func parseTarget(s string) (half.Bits, bool) {
	if len(s) != 4 {
		return half.Bits{}, false
	}
	var p uint16
	for _, c := range []byte(s) {
		var d uint16
		switch {
		case c >= '0' && c <= '9':
			d = uint16(c - '0')
		case c >= 'a' && c <= 'f':
			d = uint16(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = uint16(c-'A') + 10
		default:
			return half.Bits{}, false
		}
		p = (p << 4) | d
	}
	return half.DecodePattern(p)
}

type parsedCalibrationValue struct {
	input     string
	negative  bool
	value     *big.Rat
	target    half.Bits
	targetHex string
}

// prepareCalibration validates the entire calibration request before any
// result is produced.
func prepareCalibration(req Request) ([]parsedCalibrationValue, error) {
	if req.Targets == nil {
		return nil, nil
	}
	targets := *req.Targets
	if len(targets) != len(req.Values) {
		return nil, ErrTargetLength
	}
	parsed := make([]parsedCalibrationValue, len(req.Values))
	for i, input := range req.Values {
		neg, r, err := decimal.Parse(input)
		if err != nil {
			return nil, fmt.Errorf("values[%d]: %w", i, err)
		}
		target, ok := parseTarget(targets[i])
		if !ok {
			return nil, fmt.Errorf("targets[%d] %q: %w", i, targets[i], ErrInvalidTarget)
		}
		parsed[i] = parsedCalibrationValue{
			input:     input,
			negative:  neg,
			value:     r,
			target:    target,
			targetHex: fmt.Sprintf("%04X", target.Pattern()),
		}
	}
	return parsed, nil
}

func runCalibration(parsed []parsedCalibrationValue) Response {
	n := len(parsed)
	intersection := half.AdditiveBiasInterval(parsed[0].target, parsed[0].negative, parsed[0].value)
	for i := 1; i < n; i++ {
		iv := half.AdditiveBiasInterval(parsed[i].target, parsed[i].negative, parsed[i].value)
		var ok bool
		intersection, ok = half.Intersect(intersection, iv)
		if !ok {
			return Response{Count: n, Calibration: &CalibrationJSON{Feasible: false}}
		}
	}

	cal := &CalibrationJSON{
		Feasible: true,
		Interval: intervalJSON(intersection),
	}
	bias, attainable := half.MinimumBias(intersection)
	absBias := new(big.Rat).Abs(bias)
	cal.MinimumAbsoluteBias = ratJSON(absBias)
	cal.MinimumAttainable = &attainable
	if attainable {
		cal.Bias = ratJSON(bias)
		cal.Results = make([]Item, n)
		for i, p := range parsed {
			b := half.AddBias(p.negative, p.value, bias)
			hex := fmt.Sprintf("%04X", b.Pattern())
			class := string(b.Class)
			target := p.targetHex
			item := Item{
				Index:  i,
				Input:  p.input,
				Target: &target,
				OK:     true,
				Hex:    &hex,
				Class:  &class,
			}
			if b.IsInf() {
				item.RoundingError = json.RawMessage("null")
			} else {
				e := half.Error(b, new(big.Rat).Add(p.value, bias))
				item.RoundingError = ratJSON(e)
			}
			cal.Results[i] = item
		}
	}
	return Response{Count: n, Calibration: cal}
}

// Run validates the batch and converts every entry.
func Run(req Request) (Response, error) {
	return RunWith(req, false)
}

// RunWith is Run honoring an externally forced certificate mode (the
// command line -with-intervals flag). When neither the request nor the
// force bit enables certificates the output is unchanged.
func RunWith(req Request, forceIntervals bool) (Response, error) {
	n := len(req.Values)
	if n < MinBatch {
		return Response{}, ErrEmptyBatch
	}
	if n > MaxBatch {
		return Response{}, ErrBatchTooLarge
	}
	parsedCal, err := prepareCalibration(req)
	if err != nil {
		return Response{}, err
	}
	if req.Targets != nil {
		return runCalibration(parsedCal), nil
	}

	with := req.WithIntervals || forceIntervals
	resp := Response{Count: n, Result: make([]Item, n)}
	for i, v := range req.Values {
		resp.Result[i] = ConvertOneWith(i, v, with)
	}
	return resp, nil
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
