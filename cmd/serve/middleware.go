package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
)

const HTMLContentType = "text/html"

func withRecoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				w.Header().Set("Connection", "close")
				serverError(w, fmt.Errorf("%s", err))
			}
		}()

		next.ServeHTTP(w, r)
	})
}

func withRequestCancel(next http.Handler, ctx context.Context) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCtx, reqCancel := context.WithCancel(r.Context())
		defer reqCancel()

		go func() {
			select {
			case <-ctx.Done():
				reqCancel()
			case <-reqCtx.Done():
			}
		}()

		next.ServeHTTP(w, r.WithContext(reqCtx))
	})
}

func withNoCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func withInjectReload(next http.Handler, injection string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bw := newBufferedResponseWriter(w)
		defer func() {
			if _, err := bw.bufferFlush(); err != nil {
				serverError(w, err)
			}
		}()

		next.ServeHTTP(bw, r)

		if r.Header.Get("Range") != "" {
			return
		}
		if !strings.Contains(bw.Header().Get("Content-Type"), HTMLContentType) {
			return
		}

		htmlStr := strings.Replace(bw.buf.String(), "</body>", injection+"</body>", 1)

		if contentLength := bw.Header().Get("Content-Length"); contentLength != "" {
			n, err := strconv.Atoi(contentLength)
			if err != nil {
				fmt.Printf("could not parse Content-Length value: %v\n", err)
				os.Exit(1)
			}
			bw.Header().Set("Content-Length", strconv.Itoa(n+len(injection)))
		}

		if _, err := fmt.Fprint(bw, htmlStr); err != nil {
			serverError(w, err)
		}
	})
}
