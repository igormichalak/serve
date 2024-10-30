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
	"slices"
	"strings"
	"syscall"
	"text/template"
	"time"

	"github.com/fsnotify/fsnotify"
)

const DefaultPort = "8080"
const HTMLContentType = "text/html"

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

var reloadBroadcaster = newBroadcaster()
var ignoredDirs = []string{".git", ".idea", "node_modules"}
var trackedOp = fsnotify.Create | fsnotify.Write | fsnotify.Remove | fsnotify.Rename

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
		fmt.Printf("failed to execute injection template: %v\n", err)
		os.Exit(1)
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

	rootFS := os.DirFS(dir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mux := http.NewServeMux()
	fileServer := http.FileServerFS(rootFS)
	var handler http.Handler

	if injectReload {
		mux.Handle("GET /", withInjectReload(fileServer, injection))
		mux.HandleFunc("GET /sse", liveReloadHandler)
		handler = withRecoverPanic(withRequestCancel(withNoCache(mux), ctx))
	} else {
		mux.Handle("GET /", fileServer)
		handler = withRecoverPanic(withRequestCancel(mux, ctx))
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

	go func() {
		if !injectReload {
			return
		}
		debounce := newDebouncer(100 * time.Millisecond)
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			fmt.Printf("failed to create a watcher: %v\n", err)
			os.Exit(1)
		}
		defer func() {
			if err := watcher.Close(); err != nil {
				fmt.Printf("failed to stop a watcher: %v\n", err)
				os.Exit(1)
			}
		}()
		err = fs.WalkDir(rootFS, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				return nil
			}
			if d.IsDir() && slices.Contains(ignoredDirs, d.Name()) {
				return fs.SkipDir
			}
			return watcher.Add(path)
		})
		if err != nil {
			fmt.Printf("error occured while trying to register the fs tree: %v\n", err)
			os.Exit(1)
		}
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-watcher.Events:
				if !ok {
					return
				}
				if ev.Op&trackedOp == 0 {
					continue
				}
				debounce.Call("reload", func() {
					reloadBroadcaster.notify()
				})
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				fmt.Printf("watcher error: %v\n", err)
			}
		}
	}()

	<-stopC
	cancel()

	fmt.Println("shutting down server...")

	shutdownCtx, shutdownRelease := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownRelease()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		fmt.Printf("server shutdown failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("server gracefully stopped.")
}
