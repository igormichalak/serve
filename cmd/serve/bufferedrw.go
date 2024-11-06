package main

import (
	"bytes"
	"io"
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

func (bw *bufferedResponseWriter) Flush() {
	flusher := bw.ResponseWriter.(http.Flusher)
	flusher.Flush()
}

func (bw *bufferedResponseWriter) bufferFlush() (written int64, err error) {
	bw.ResponseWriter.WriteHeader(bw.status)
	return io.Copy(bw.ResponseWriter, &bw.buf)
}
