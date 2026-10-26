package webapp

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-errors/errors"
	"github.com/strrl/lapp/pkg/victorialogs"
	"github.com/strrl/lapp/pkg/workspace"
)

const victoriaLogsStreamsTimeout = time.Minute

// victoriaLogsService is the slice of a VictoriaLogs query that lands in one
// service's workspace: every matching stream whose labels name that service.
type victoriaLogsService struct {
	Service string
	Query   string
	Streams []string
	Hits    int
}

// planVictoriaLogsServices groups streams by the service their labels name
// and narrows the query to each group's stream IDs, so each service gets its
// own ImportRun with a re-pullable query. Sorted by service name.
func planVictoriaLogsServices(streams []victorialogs.Stream, query string) []victoriaLogsService {
	groups := map[string]*victoriaLogsService{}
	ids := map[string][]string{}
	for _, stream := range streams {
		svc := cleanServiceName(victorialogs.ServiceLabel(stream.Labels))
		group := groups[svc]
		if group == nil {
			group = &victoriaLogsService{Service: svc}
			groups[svc] = group
		}
		group.Streams = append(group.Streams, stream.Labels)
		group.Hits += stream.Hits
		ids[svc] = append(ids[svc], stream.ID)
	}
	plan := make([]victoriaLogsService, 0, len(groups))
	for svc, group := range groups {
		sort.Strings(ids[svc])
		group.Query = victorialogs.CombinedQuery(query, ids[svc])
		plan = append(plan, *group)
	}
	sort.Slice(plan, func(i, j int) bool { return plan[i].Service < plan[j].Service })
	return plan
}

// victoriaLogsFetcher adapts a VictoriaLogs client to the ImportRun fetcher
// seam. The ImportRun project carries the optional tenant.
func victoriaLogsFetcher(client *victorialogs.Client) workspace.ImportFetcher {
	return func(ctx context.Context, req workspace.ImportRequest, writeLine workspace.ImportLineWriter) (workspace.ImportFetchResult, error) {
		fetched, err := client.Fetch(ctx, victorialogs.FetchRequest{
			URL:    req.URL,
			Tenant: req.Project,
			Query:  req.Filter,
			From:   req.From,
			To:     req.To,
			Limit:  req.Limit,
		}, writeLine)
		if err != nil {
			return workspace.ImportFetchResult{}, err
		}
		return workspace.ImportFetchResult{Truncated: fetched.Truncated}, nil
	}
}

func listVictoriaLogsServices(
	ctx context.Context,
	list func(context.Context, victorialogs.FetchRequest) ([]victorialogs.Stream, error),
	req victorialogs.FetchRequest,
) ([]victoriaLogsService, error) {
	ctx, cancel := context.WithTimeout(ctx, victoriaLogsStreamsTimeout)
	defer cancel()
	streams, err := list(ctx, req)
	if err != nil {
		return nil, errors.Errorf("list victorialogs streams: %w", err)
	}
	return planVictoriaLogsServices(streams, req.Query), nil
}

type victoriaLogsImportRequest struct {
	URL   string    `json:"url"`
	Query string    `json:"query"`
	Since string    `json:"since"` // e.g. "1h"; ignored when From/To are set
	From  time.Time `json:"from"`
	To    time.Time `json:"to"`
}

type victoriaLogsStarted struct {
	ID      string `json:"id"`
	Service string `json:"service"`
}

// handleImportVictoriaLogs splits a VictoriaLogs snapshot by service: one new
// workspace per service, each imported and then searched for patterns in the
// background. Poll /api/demo/status for each returned workspace.
func (d *demoAPI) handleImportVictoriaLogs(w http.ResponseWriter, r *http.Request) {
	if d.fetcher == nil || d.victoriaLogsStreams == nil {
		writeError(w, http.StatusFailedDependency, errors.New("VictoriaLogs import is not available"))
		return
	}
	var req victoriaLogsImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	from, to, err := importTimeRange(req.Since, req.From, req.To)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	fetch := victorialogs.FetchRequest{URL: strings.TrimSpace(req.URL), Query: req.Query, From: from, To: to}
	if err := fetch.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	plan, err := listVictoriaLogsServices(r.Context(), d.victoriaLogsStreams, fetch)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if len(plan) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("no log entries matched this query and time range"))
		return
	}
	started := make([]victoriaLogsStarted, 0, len(plan))
	dirs := make([]string, 0, len(plan))
	for _, svc := range plan {
		id, dir, err := d.newWorkspaceDir(svc.Service)
		if err == nil {
			err = prepareImportWorkspace(dir, svc.Service)
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		started = append(started, victoriaLogsStarted{ID: id, Service: svc.Service})
		dirs = append(dirs, dir)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		for i, svc := range plan {
			dir := dirs[i]
			res, err := workspace.RunImport(ctx, workspace.ImportConfig{
				Dir: dir, Provider: victorialogs.Provider, URL: fetch.URL, Filter: svc.Query,
				From: from, To: to, Limit: providerImportLimit, Fetcher: d.fetcher,
			})
			if err != nil {
				slog.Error("victorialogs import failed", "workspace", started[i].ID, "error", err)
				continue
			}
			if res.EntryCount == 0 {
				continue
			}
			if _, err := workspace.Discover(ctx, workspace.DiscoveryConfig{Dir: dir, APIKey: d.namingKey(svc.Service), Model: d.model, Mask: d.namingMask(svc.Service)}); err != nil {
				slog.Error("discovery failed", "workspace", started[i].ID, "error", err)
			}
		}
	}()
	writeJSON(w, map[string]any{"workspaces": started})
}

// importTimeRange resolves the web app's "last <since>" or explicit range.
func importTimeRange(since string, from, to time.Time) (start, end time.Time, err error) {
	if from.IsZero() || to.IsZero() {
		duration, parseErr := time.ParseDuration(since)
		if parseErr != nil || duration <= 0 {
			return time.Time{}, time.Time{}, errors.New("choose a time range")
		}
		to = time.Now().UTC()
		from = to.Add(-duration)
	}
	if !from.Before(to) {
		return time.Time{}, time.Time{}, errors.New("the start must be before the end")
	}
	return from, to, nil
}

// VictoriaLogsImport describes a CLI import from VictoriaLogs. Each service
// found in the matching streams lands in workspace <Topic>-<service> under
// Root, created when missing.
type VictoriaLogsImport struct {
	Root    string
	Topic   string
	Request victorialogs.FetchRequest
	Client  *victorialogs.Client
	// OnPlan, if set, is called with the services found before any rows are
	// fetched, so callers can say how much is about to be pulled.
	OnPlan func(services []string)
}

// VictoriaLogsServiceImport is the outcome for one service.
type VictoriaLogsServiceImport struct {
	Service     string
	WorkspaceID string
	Streams     []string
	Result      workspace.ImportResult
	Err         error
}

// ImportVictoriaLogs runs one ImportRun per service and never starts
// discovery. A failed service does not stop the others; check each Err.
func ImportVictoriaLogs(ctx context.Context, imp VictoriaLogsImport) ([]VictoriaLogsServiceImport, error) {
	root, err := resolveRoot(imp.Root)
	if err != nil {
		return nil, err
	}
	if err := imp.Request.Validate(); err != nil {
		return nil, err
	}
	plan, err := listVictoriaLogsServices(ctx, imp.Client.Streams, imp.Request)
	if err != nil {
		return nil, err
	}
	if imp.OnPlan != nil {
		names := make([]string, 0, len(plan))
		for _, svc := range plan {
			names = append(names, svc.Service)
		}
		imp.OnPlan(names)
	}
	fetcher := victoriaLogsFetcher(imp.Client)
	out := make([]VictoriaLogsServiceImport, 0, len(plan))
	for _, svc := range plan {
		id := imp.Topic + "-" + svc.Service
		result := VictoriaLogsServiceImport{Service: svc.Service, WorkspaceID: id, Streams: svc.Streams}
		dir := filepath.Join(root, id)
		if result.Err = prepareImportWorkspace(dir, svc.Service); result.Err == nil {
			result.Result, result.Err = workspace.RunImport(ctx, workspace.ImportConfig{
				Dir: dir, Provider: victorialogs.Provider, URL: imp.Request.URL, Project: imp.Request.Tenant,
				Filter: svc.Query, From: imp.Request.From, To: imp.Request.To, Limit: imp.Request.Limit, Fetcher: fetcher,
			})
		}
		out = append(out, result)
	}
	return out, nil
}
