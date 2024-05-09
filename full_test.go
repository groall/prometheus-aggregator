package main

import (
	"bytes"
	"compress/gzip"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestThreeTypes(t *testing.T) {
	u, _ := newUniverse()
	loadObservations(t, u, makeObservations(t, []string{
		`{"name":"foo_total","type":"counter","help":"Total number of foos."}`,
		`{"name":"foo_total","labels":{"code":"200"},"value": 1}`,
		`{"name":"foo_total","labels":{"code":"404"},"value": 2}`,
		`foo_total{code="200"} 4`,
		`foo_total{code="404"} 8`,

		`{"name":"bar_seconds","type":"histogram","help":"Bar duration in seconds.","buckets":[0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10]}`,
		`{"name":"bar_seconds","value":0.123}`,
		`{"name":"bar_seconds","value":0.234}`,
		`{"name":"bar_seconds","value":0.501}`,
		`{"name":"bar_seconds","value":8.000}`,

		`{"name":"baz_size","type":"gauge","help":"Current size of baz widget."}`,
		`{"name":"baz_size","value": 1}`,
		`{"name":"baz_size","value": 2}`,
		`baz_size{} 4`,
	}))
	if want, have := normalizeResponse(`
		# HELP bar_seconds Bar duration in seconds.
		# TYPE bar_seconds histogram
		bar_seconds_bucket{le="0.01"} 0
		bar_seconds_bucket{le="0.05"} 0
		bar_seconds_bucket{le="0.1"} 0
		bar_seconds_bucket{le="0.5"} 2
		bar_seconds_bucket{le="1"} 3
		bar_seconds_bucket{le="2"} 3
		bar_seconds_bucket{le="5"} 3
		bar_seconds_bucket{le="10"} 4
		bar_seconds_bucket{le="+Inf"} 4
		bar_seconds_sum{} 8.858000
		bar_seconds_count{} 4
		
		# HELP baz_size Current size of baz widget.
		# TYPE baz_size gauge
		baz_size{} 4.000000
		
		# HELP foo_total Total number of foos.
		# TYPE foo_total counter
		foo_total{code="200"} 5.000000
		foo_total{code="404"} 10.000000
	`), normalizeResponse(scrape(t, u)); want != have {
		t.Fatalf("\n---WANT---\n%s\n\n---HAVE---\n%s\n", want, have)
	}
}

func TestInitialDeclarations(t *testing.T) {
	u, _ := newUniverse(makeObservations(t, []string{
		`{"name":"foo_total","type":"counter","help":"Total number of foos."}`,
		`{"name":"bar_seconds","type":"histogram","help":"Bar duration in seconds.","buckets":[0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10]}`,
		`{"name":"baz_size","type":"gauge","help":"Current size of baz widget."}`,
		`{"name":"qux_count","type":"counter","help":"Count of qux events."}`,
	})...)
	loadObservations(t, u, makeObservations(t, []string{
		`foo_total{label="value"} 1`,
		`bar_seconds{} 0.234`,
		`baz_size{} 5`,
	}))
	if want, have := normalizeResponse(`
		# HELP bar_seconds Bar duration in seconds.
		# TYPE bar_seconds histogram
		bar_seconds_bucket{le="0.01"} 0
		bar_seconds_bucket{le="0.05"} 0
		bar_seconds_bucket{le="0.1"} 0
		bar_seconds_bucket{le="0.5"} 1
		bar_seconds_bucket{le="1"} 1
		bar_seconds_bucket{le="2"} 1
		bar_seconds_bucket{le="5"} 1
		bar_seconds_bucket{le="10"} 1
		bar_seconds_bucket{le="+Inf"} 1
		bar_seconds_sum{} 0.234000
		bar_seconds_count{} 1
		
		# HELP baz_size Current size of baz widget.
		# TYPE baz_size gauge
		baz_size{} 5.000000
		
		# HELP foo_total Total number of foos.
		# TYPE foo_total counter
		foo_total{label="value"} 1.000000
	`), normalizeResponse(scrape(t, u)); want != have {
		t.Fatalf("\n---WANT---\n%s\n\n---HAVE---\n%s\n", want, have)
	}
}

// TestParseLine is a regression test for a bug in the line parser.
func TestParseLine(t *testing.T) {
	// Test that we can parse a line with JSON.
	msg := []byte(`{"name":"foo_total","type":"counter","help":"Total number of foos."}`)
	o, err := parseLine(msg)
	if err != nil {
		t.Fatal(err)
	}
	if want, have := "foo_total", o.Name; want != have {
		t.Fatalf("\n---WANT---\n%s\n\n---HAVE---\n%s\n", want, have)
	}
}

func makeObservations(t *testing.T, lines []string) []observation {
	t.Helper()
	observations := make([]observation, len(lines))
	for i, s := range lines {
		o, err := parseLine([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		observations[i] = o
	}
	return observations
}

func loadObservations(t *testing.T, obs observer, observations []observation) {
	t.Helper()
	for _, o := range observations {
		if err := obs.observe(o); err != nil {
			t.Fatalf("%+v: %v", o, err)
		}
	}
}

func scrape(t *testing.T, h http.Handler) string {
	rec := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/", nil)
	h.ServeHTTP(rec, req)
	return rec.Body.String()
}

func normalizeResponse(s string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

type mockPacketConn struct {
	data []byte
	err  error
}

func (c *mockPacketConn) WriteTo(p []byte, _ net.Addr) (int, error) { return len(p), nil }
func (c *mockPacketConn) Close() error                              { return nil }
func (c *mockPacketConn) LocalAddr() net.Addr                       { return nil }
func (c *mockPacketConn) RemoteAddr() net.Addr                      { return nil }
func (c *mockPacketConn) SetDeadline(time.Time) error               { return nil }
func (c *mockPacketConn) SetReadDeadline(time.Time) error           { return nil }
func (c *mockPacketConn) SetWriteDeadline(time.Time) error          { return nil }

func (m *mockPacketConn) ReadFrom(buf []byte) (int, net.Addr, error) {
	copy(buf, m.data)
	return len(m.data), nil, m.err
}

func TestReadFromPacketConn(t *testing.T) {
	expectedOutput := []byte("Hello, World!")
	compressedData := compressData(expectedOutput)
	mockConn := &mockPacketConn{data: compressedData, err: nil}

	// check that we can read gzipped data from the packet conn
	output, err := readFromPacketConn(mockConn, make([]byte, len(compressedData)))
	if err != nil {
		t.Errorf("readFromPacketConn returned an error: %v", err)
	}

	if !reflect.DeepEqual(output, expectedOutput) {
		t.Errorf("readFromPacketConn did not return the expected output. Got: %s, Expected: %s", output, expectedOutput)
	}

	// test that we can read uncompressed data
	mockConn = &mockPacketConn{data: expectedOutput, err: nil}
	output, err = readFromPacketConn(mockConn, make([]byte, len(expectedOutput)))
	if err != nil {
		t.Errorf("readFromPacketConn returned an error: %v", err)
	}

	if !reflect.DeepEqual(output, expectedOutput) {
		t.Errorf("readFromPacketConn did not return the expected output. Got: %s, Expected: %s", output, expectedOutput)
	}
}

func TestUnZipData(t *testing.T) {
	expectedOutput := []byte("Hello, World!")

	compressedData := compressData(expectedOutput)

	output, err := unZipData(compressedData)
	if err != nil {
		t.Errorf("unZipData returned an error: %v", err)
	}

	if !reflect.DeepEqual(output, expectedOutput) {
		t.Errorf("unZipData did not return the expected output. Got: %s, Expected: %s", output, expectedOutput)
	}
}

func compressData(data []byte) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, err := gz.Write(data)
	if err != nil {
		panic(err)
	}
	err = gz.Close()
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func TestIsGzipped(t *testing.T) {
	testCases := []struct {
		input    []byte
		expected bool
	}{
		{[]byte{31, 139, 8, 0, 0, 0, 0, 0, 0, 255}, true},  // Gzipped data
		{[]byte{31, 139}, true},                            // Gzipped data with minimum length
		{[]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, false},      // Not gzipped data
		{[]byte{31, 138, 8, 0, 0, 0, 0, 0, 0, 255}, false}, // Not gzipped data
	}

	for _, tc := range testCases {
		result := isGzipped(tc.input)
		if result != tc.expected {
			t.Errorf("isGzipped have %v for input %v, want %v", result, tc.input, tc.expected)
		}
	}
}

func TestTransparentDecompressGZip(t *testing.T) {
	testCases := []struct {
		input          []byte
		expectedOutput []byte
		expectedError  error
	}{
		{compressData([]byte("Hello, World!")), []byte("Hello, World!"), nil},      // Gzipped data
		{[]byte("Hello, World!"), []byte("Hello, World!"), nil},                    // Gzipped data
		{[]byte{31, 139, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, nil, gzip.ErrHeader}, // Non-gzipped data
	}

	for _, tc := range testCases {
		output, err := decompressIfGzipped(tc.input)

		if !reflect.DeepEqual(output, tc.expectedOutput) {
			t.Errorf("transparentDecompressGZip did not return the expected output. Have: %v, Want: %v", output, tc.expectedOutput)
		}

		if !errors.Is(err, tc.expectedError) {
			t.Errorf("transparentDecompressGZip returned unexpected error. Have: %v, Want: %v", err, tc.expectedError)
		}
	}
}
