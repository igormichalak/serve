package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"text/template"
	"time"
)

const DefaultPort = "8080"
const HTMLContentType = "text/html"

var reloadBroadcaster = newBroadcaster()

const InjectionTmplString = `<script>
    const sse = new EventSource('http://localhost:{{.Port}}/sse');
	sse.onerror = e => console.error('EventSource failed:', e);
    sse.addEventListener('sourcechange', () => {
        sse.close();
		if (document.hidden) {
			document.addEventListener('visibilitychange', () => {
				if (!document.hidden) window.location.reload();
		    });
		} else {
        	window.location.reload();
		}
    });
</script>`

var InjectionTmpl = template.Must(template.New("sse").Parse(InjectionTmplString))

type InjectionParams struct {
	Port string
}

func serverError(w http.ResponseWriter, err error) {
	fmt.Printf("server error: %v\n", err)
	var body string

	if os.Getenv("DEBUG") == "1" || os.Getenv("DEBUG") == "true" {
		trace := string(debug.Stack())
		body = fmt.Sprintf("%s\n%s", err, trace)
	} else {
		body = http.StatusText(http.StatusInternalServerError)
	}

	http.Error(w, body, http.StatusInternalServerError)
}

func noCacheMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func injectReloadMiddleware(next http.Handler, injection string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bw := newBufferedResponseWriter(w)
		defer func() {
			if err := bw.customFlush(); err != nil {
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

		htmlStr := strings.ReplaceAll(bw.buf.String(), "</body>", injection+"</body>")

		if contentLength := bw.Header().Get("Content-Length"); contentLength != "" {
			n, err := strconv.Atoi(contentLength)
			if err != nil {
				panic(err)
			}
			bw.Header().Set("Content-Length", strconv.Itoa(n+len(injection)))
		}

		if _, err := fmt.Fprint(bw, htmlStr); err != nil {
			serverError(w, err)
		}
	})
}

func formatSSE(event, data string) string {
	return fmt.Sprintf("event: %s\ndata: %s\n\n", event, data)
}

func liveReloadHandler(w http.ResponseWriter, r *http.Request) {
	flusher := w.(http.Flusher)

	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	ch := reloadBroadcaster.subscribe()
	defer reloadBroadcaster.unsubscribe(ch)

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			msg := formatSSE("sourcechange", "{}")
			if _, err := fmt.Fprint(w, msg); err != nil {
				return
			}
			flusher.Flush()
		}
	}
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

	var injectionSB strings.Builder
	if err := InjectionTmpl.Execute(&injectionSB, InjectionParams{Port: port}); err != nil {
		panic(err)
	}
	injection := injectionSB.String()

	dir := flag.Arg(0)
	if dir == "" {
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

	mux := http.NewServeMux()
	fileServer := http.FileServerFS(os.DirFS(dir))
	var handler http.Handler

	if injectReload {
		mux.Handle("GET /", injectReloadMiddleware(fileServer, injection))
		mux.HandleFunc("GET /sse", liveReloadHandler)
		handler = noCacheMiddleware(mux)
	} else {
		mux.Handle("GET /", fileServer)
		handler = mux
	}

	var addr string
	if expose {
		addr = fmt.Sprintf(":%s", port)
	} else {
		addr = fmt.Sprintf("localhost:%s", port)
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
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
