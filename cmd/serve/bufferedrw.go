package main

import (
	"bytes"
	"net/http"
)

type bufferedResponseWriter struct {
	http.ResponseWriter
	buf    bytes.Buffer
	status int
}

func newBufferedResponseWriter(w http.ResponseWriter) *bufferedResponseWriter {
	return &bufferedResponseWriter{ResponseWriter: w, status: http.StatusOK}
}

func (bw *bufferedResponseWriter) Write(b []byte) (int, error) {
	if bw.status == 0 {
		bw.status = http.StatusOK
	}
	bw.buf.Reset()
	return bw.buf.Write(b)
}

func (bw *bufferedResponseWriter) WriteHeader(statusCode int) {
	bw.status = statusCode
}

func (bw *bufferedResponseWriter) flush() error {
	bw.ResponseWriter.WriteHeader(bw.status)
	_, err := bw.ResponseWriter.Write(bw.buf.Bytes())
	return err
}
