package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/pkg/errors"
)

type observer interface{ observe(observation) error }

// readFromPacketConn reads a packet from the given packet connection and returns the data as a byte slice. The data is transparently decompressed if it is gzipped.
func readFromPacketConn(conn net.PacketConn, buf []byte) ([]byte, error) {
	n, _, err := conn.ReadFrom(buf)
	if err != nil {
		return nil, err
	}

	return transparentDecompressGZip(buf[:n])
}

func forwardPacketConn(conn net.PacketConn, o observer, logger log.Logger) error {
	buf := make([]byte, bufio.MaxScanTokenSize)
	for {
		packet, err := readFromPacketConn(conn, buf)
		if err != nil {
			return err
		}
		name, err := handleLine(packet, o)
		if err != nil {
			level.Error(logger).Log("line", "rejected", "err", err)
			continue
		}
		level.Debug(logger).Log("line", "accepted", "name", name)
	}
}

func forwardListener(ln net.Listener, o observer, strict bool, logger log.Logger) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go handleConn(conn, o, strict, log.With(logger, "remote_addr", conn.RemoteAddr()))
	}
}

func handleConn(rc io.ReadCloser, o observer, strict bool, logger log.Logger) {
	defer rc.Close()
	s := bufio.NewScanner(rc)
	for s.Scan() {
		data, err := transparentDecompressGZip(s.Bytes())
		if err != nil {
			level.Error(logger).Log("line", "rejected", "err", err)
			continue
		}
		name, err := handleLine(data, o)
		if err != nil {
			level.Error(logger).Log("line", "rejected", "err", err)
			if strict {
				return
			}
			continue
		}
		level.Debug(logger).Log("line", "accepted", "name", name)
	}
}

func handleLine(line []byte, o observer) (string, error) {
	obs, err := parseLine(line)
	if err != nil {
		return "", errors.Wrap(err, "parse error")
	}
	if err := o.observe(obs); err != nil {
		return obs.Name, errors.Wrap(err, "observation error")
	}
	return obs.Name, nil
}

func parseLine(p []byte) (o observation, err error) {
	if len(p) <= 0 {
		err = errors.New("invalid (empty) line")
	} else if p[0] == '{' {
		err = json.Unmarshal(p, &o)
	} else {
		err = prometheusUnmarshal(p, &o)
	}
	return o, err
}

func prometheusUnmarshal(p []byte, o *observation) error {
	p = bytes.TrimSpace(p)
	x := bytes.LastIndexByte(p, ' ')
	if x < 1 {
		return fmt.Errorf("bad format: couldn't find space")
	}

	id, val := bytes.TrimSpace(p[:x]), bytes.TrimSpace(p[x+1:])

	value, err := strconv.ParseFloat(string(val), 64)
	if err != nil {
		return errors.Wrapf(err, "bad value (%s)", string(val))
	}

	y := bytes.IndexByte(id, '{')
	if y < 0 {
		return fmt.Errorf("bad format: couldn't find opening brace")
	}
	if id[len(id)-1] != '}' {
		return fmt.Errorf("bad format: couldn't find terminating brace")
	}

	name, labels := id[:y], id[y+1:len(id)-1]
	if bytes.ContainsRune(labels, ' ') {
		return fmt.Errorf("bad format: labels section may not contain spaces")
	}

	labelmap := map[string]string{}
	for _, pair := range bytes.Split(labels, []byte(",")) {
		z := bytes.IndexByte(pair, '=')
		if z < 0 {
			continue
		}
		k, v := pair[:z], pair[z+1:]
		if v[0] != '"' || v[len(v)-1] != '"' {
			return fmt.Errorf("bad format: label value must be wrapped in quotes")
		}
		v = v[1 : len(v)-1]
		labelmap[string(k)] = string(v)
	}

	o.Name = string(name)
	o.Labels = labelmap
	o.Value = new(float64)
	(*o.Value) = value

	return nil
}

// unZipData is a Go function that takes a byte slice as input and returns a byte slice and an error.
//
// It reads the input data as a gzip-compressed stream and decompresses it. The decompressed data is then returned as a byte slice.
// If any error occurs during the decompression process, the function returns the error.
func unZipData(data []byte) ([]byte, error) {
	reader := bytes.NewReader(data)
	gzreader, e1 := gzip.NewReader(reader)
	if e1 != nil {
		return nil, e1
	}

	output, e2 := io.ReadAll(gzreader)
	if e2 != nil {
		return nil, e2
	}

	return output, nil
}

// transparentDecompressGZip decompresses data if it is gzipped.
func transparentDecompressGZip(data []byte) ([]byte, error) {
	if isGzipped(data) { // gzip
		return unZipData(data)
	}

	return data, nil
}

// isGzipped checks if the given byte slice represents a gzip-compressed stream.
func isGzipped(packet []byte) bool {
	return len(packet) >= 2 && packet[0] == 31 && packet[1] == 139
}
