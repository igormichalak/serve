package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const DefaultPort = "8080"

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

func noCacheMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func injectReloadMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bw := newBufferedResponseWriter(w)
		next.ServeHTTP(bw, r)
		_ = bw.flush()
	})
}

func main() {
	var port string
	var expose bool
	var injectReload bool

	flag.StringVar(&port, "port", DefaultPort, "HTTP server port")
	flag.BoolVar(&expose, "expose", false, "expose the server to all interfaces")
	flag.BoolVar(&injectReload, "reload", false, "inject auto reload into HTML files")
	flag.Parse()

	for _, c := range port {
		if '0' <= c && c <= '9' {
			continue
		}
		fmt.Printf("port contains a character that is not a digit: %q.\n", string(c))
		os.Exit(1)
	}

	dir := flag.Arg(0)

	if len(dir) == 0 {
		fmt.Println("please specify the root directory.")
		os.Exit(1)
	}

	fi, err := os.Stat(dir)
	if err != nil {
		var pathErr *fs.PathError
		if errors.As(err, &pathErr) {
			fmt.Printf("can't open %q; error: %v\n", pathErr.Path, pathErr.Err)
		} else {
			fmt.Printf("error: %v\n", err)
		}
		os.Exit(1)
	}

	if !fi.IsDir() {
		fmt.Printf("%q is not a directory.\n", dir)
		os.Exit(1)
	}

	var addr string
	if expose {
		addr = fmt.Sprintf(":%s", port)
	} else {
		addr = fmt.Sprintf("localhost:%s", port)
	}

	fileServer := http.FileServerFS(os.DirFS(dir))

	var topHandler http.Handler
	if injectReload {
		topHandler = noCacheMiddleware(injectReloadMiddleware(fileServer))
	} else {
		topHandler = fileServer
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           topHandler,
		ReadTimeout:       6 * time.Second,
		ReadHeaderTimeout: 2 * time.Second,
		WriteTimeout:      12 * time.Second,
		IdleTimeout:       time.Minute,
		MaxHeaderBytes:    8_192,
	}

	stopC := make(chan os.Signal, 1)
	signal.Notify(stopC, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)

	go func() {
		fmt.Printf("starting server on %q...\n", srv.Addr)

		err := srv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Printf("server failed: %v\n", err)
			os.Exit(1)
		}
	}()

	<-stopC
	fmt.Println("shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		fmt.Printf("server shutdown failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("server gracefully stopped.")
}
