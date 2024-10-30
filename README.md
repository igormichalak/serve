# serve

A tiny CLI utility for static file serving

## Installation

Prerequisites:
- [Go 1.23.2](https://go.dev/doc/install)

Install **serve** from source:

```bash
go install github.com/igormichalak/serve/cmd/serve@latest
```

If the `serve` command can't be found, make sure that the `$GOBIN` (or `$GOPATH/bin`) directory is added to your system PATH.

Here's how to find the location of the binary:
```bash
go env GOBIN
```
```bash
echo "$(go env GOPATH)/bin"
```

More info: https://go.dev/wiki/GOPATH

## Usage

```bash
serve path/to/directory
```

### Flags

```bash
Usage of serve:
  -expose
    	expose the server to all interfaces
  -port string
    	HTTP server port (default "8080")
  -reload
    	inject auto reload into HTML files
```
