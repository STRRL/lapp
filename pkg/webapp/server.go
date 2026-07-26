package webapp

import (
	"context"
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-errors/errors"
	"github.com/strrl/lapp/gen/go/lapp/web/v1/webv1connect"
	"github.com/strrl/lapp/pkg/gcplog"
	"github.com/strrl/lapp/pkg/workspace"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

//go:embed static/*
var fallbackStatic embed.FS

type ServerConfig struct {
	Addr        string
	Root        string
	StaticDir   string
	APIKey      string
	Model       string
	OpenBrowser bool
}

func NewHandler(config ServerConfig) (http.Handler, error) {
	service, err := NewWorkspaceService(ServiceConfig{
		Root:   config.Root,
		APIKey: config.APIKey,
		Model:  config.Model,
		ImportFetcher: func(ctx context.Context, req workspace.ImportRequest) (workspace.ImportFetchResult, error) {
			fetched, err := gcplog.FetchLines(ctx, gcplog.FetchRequest{
				Project: req.Project,
				Filter:  req.Filter,
				From:    req.From,
				To:      req.To,
				Limit:   req.Limit,
			})
			if err != nil {
				return workspace.ImportFetchResult{}, err
			}
			return workspace.ImportFetchResult{
				Lines:     fetched.Lines,
				Truncated: fetched.Truncated,
			}, nil
		},
	})
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	path, handler := webv1connect.NewWorkspaceServiceHandler(service)
	mux.Handle(path, otelhttp.NewHandler(handler, "connect.WorkspaceService"))
	staticHandler, err := staticFileHandler(config.StaticDir)
	if err != nil {
		return nil, err
	}
	mux.Handle("/", staticHandler)
	return mux, nil
}

func staticFileHandler(staticDir string) (http.Handler, error) {
	if staticDir != "" {
		if info, err := os.Stat(staticDir); err == nil && info.IsDir() {
			return spaFileServer(os.DirFS(staticDir)), nil
		}
	}
	sub, err := fs.Sub(fallbackStatic, "static")
	if err != nil {
		return nil, errors.Errorf("load fallback static assets: %w", err)
	}
	return spaFileServer(sub), nil
}

func spaFileServer(files fs.FS) http.Handler {
	indexPath := "index.html"
	if _, err := fs.Stat(files, "app/index.html"); err == nil {
		indexPath = "app/index.html"
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(filepath.Clean(r.URL.Path), "/")
		if path == "." || path == "" {
			path = indexPath
		}
		if _, err := fs.Stat(files, path); err != nil {
			path = indexPath
		}
		data, err := fs.ReadFile(files, path)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if contentType := mime.TypeByExtension(filepath.Ext(path)); contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		_, _ = w.Write(data)
	})
}
