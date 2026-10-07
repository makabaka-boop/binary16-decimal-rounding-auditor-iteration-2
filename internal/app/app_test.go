package app_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"halfconv/internal/app"
)

func decodeResponse(t *testing.T, out *bytes.Buffer) app.Response {
	t.Helper()
	var resp app.Response
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON (%v): %s", err, out.String())
	}
	return resp
}

func execute(t *testing.T, body string) (app.Response, int) {
	t.Helper()
	var out bytes.Buffer
	code := app.Execute(strings.NewReader(body), &out)
	return decodeResponse(t, &out), code
}

func TestBatchEndToEnd(t *testing.T) {
	body := `{"values": ["0.1", "-0", "65520", "NaN", "1e-50", "2050"]}`
	resp, code := execute(t, body)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if resp.Count != 6 || len(resp.Result) != 6 {
		t.Fatalf("count = %d, want 6", resp.Count)
	}

	want := []struct {
		ok    bool
		hex   string
		class string
		err   json.RawMessage
	}{
		{true, "2E66", "normal", json.RawMessage(`"-1/40960"`)},
		{true, "8000", "zero", json.RawMessage(`"0/1"`)},
		{true, "7C00", "infinity", json.RawMessage(`null`)},
		{false, "", "", nil},
		{true, "0000", "zero", nil}, // error checked below
		{true, "6801", "normal", json.RawMessage(`"0/1"`)},
	}
	for i, w := range want {
		it := resp.Result[i]
		if it.OK != w.ok {
			t.Fatalf("item %d: ok=%v, want %v (%+v)", i, it.OK, w.ok, it)
		}
		if !w.ok {
			if it.Error == nil || *it.Error == "" {
				t.Fatalf("item %d: missing rejection reason", i)
			}
			if it.Hex != nil || it.Class != nil || it.RoundingError != nil {
				t.Fatalf("item %d: rejected item must not carry results", i)
			}
			continue
		}
		if *it.Hex != w.hex {
			t.Fatalf("item %d: hex=%v, want %s", i, *it.Hex, w.hex)
		}
		if *it.Class != w.class {
			t.Fatalf("item %d: class=%v, want %s", i, *it.Class, w.class)
		}
		if w.class == "infinity" {
			if string(it.RoundingError) != "null" {
				t.Fatalf("item %d: rounding_error=%s, want null", i, it.RoundingError)
			}
		}
	}

	// 1e-50: rounds to +0, error = -1/10^50, reduced denominator fully written.
	tinyErr := string(resp.Result[4].RoundingError)
	if !strings.Contains(tinyErr, "1/100000000000000000000000000000000000000000000000000") {
		t.Fatalf("1e-50 error = %s, want -1/10^50", tinyErr)
	}
}

func TestBareArrayEnvelope(t *testing.T) {
	resp, code := execute(t, `["1", "2.5"]`)
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if resp.Count != 2 {
		t.Fatalf("count = %d", resp.Count)
	}
	if got := *resp.Result[0].Hex; got != "3C00" {
		t.Fatalf("item 0 hex = %s, want 3C00", got)
	}
	if got := *resp.Result[1].Hex; got != "4100" {
		t.Fatalf("item 1 hex = %s, want 4100 (2.5)", got)
	}
}

func TestBatchSizeLimits(t *testing.T) {
	run := func(n int) (*bytes.Buffer, int) {
		vals := make([]string, n)
		for i := range vals {
			vals[i] = "1"
		}
		body, _ := json.Marshal(app.Request{Values: vals})
		var out bytes.Buffer
		code := app.Execute(bytes.NewReader(body), &out)
		return &out, code
	}

	if _, code := run(1000); code != 0 {
		t.Fatalf("batch of 1000 should succeed, code=%d", code)
	}
	if _, code := run(1001); code != 1 {
		t.Fatalf("batch of 1001 should fail, code=%d", code)
	}

	var out bytes.Buffer
	if code := app.Execute(strings.NewReader(`{"values": []}`), &out); code != 1 {
		t.Fatalf("empty batch code=%d, want 1", code)
	}
	if !bytes.Contains(out.Bytes(), []byte("1 and 1000")) {
		t.Fatalf("empty batch message: %s", out.String())
	}
}

func TestMalformedRequests(t *testing.T) {
	bodies := []string{
		``, `not json`, `{"values": [1, 2]}`, `{"values": "1"}`,
		`[`, `{"values": [NaN]}`,
	}
	for _, b := range bodies {
		var out bytes.Buffer
		code := app.Execute(strings.NewReader(b), &out)
		if code == 0 {
			t.Errorf("body %q: expected non-zero exit", b)
		}
		if !bytes.Contains(out.Bytes(), []byte(`"ok":false`)) {
			t.Errorf("body %q: expected error envelope, got %s", b, out.String())
		}
	}
}

func TestPerItemRejectionsDoNotAbortBatch(t *testing.T) {
	resp, code := execute(t, `{"values": ["Infinity", "1e51", "x", "1.0"]}`)
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	for i := 0; i < 3; i++ {
		if resp.Result[i].OK {
			t.Fatalf("item %d unexpectedly ok", i)
		}
	}
	if !resp.Result[3].OK || *resp.Result[3].Hex != "3C00" {
		t.Fatalf("item 3: %+v", resp.Result[3])
	}
}
