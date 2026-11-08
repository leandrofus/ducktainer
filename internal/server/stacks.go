package server

import (
	"context"
	"ducktainer/internal/i18n"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"ducktainer/internal/compose"
	"ducktainer/internal/docker"
)

const (
	lblProject = "com.docker.compose.project"
	lblService = "com.docker.compose.service"
	lblWorkDir = "com.docker.compose.project.working_dir"
	lblFiles   = "com.docker.compose.project.config_files"
	lblEnvFile = "com.docker.compose.project.environment_file"
)

type stackContainer struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Service string `json:"service"`
	State   string `json:"state"`
	Status  string `json:"status"`
	Image   string `json:"image"`
	Tty     bool   `json:"-"`
}

type stackInfo struct {
	Name        string           `json:"name"`
	Managed     bool             `json:"managed"`
	WorkingDir  string           `json:"working_dir,omitempty"`
	ConfigFiles []string         `json:"config_files,omitempty"`
	EnvFile     string           `json:"env_file,omitempty"`
	Services    []string         `json:"services"`
	Containers  []stackContainer `json:"containers"`
	Running     int              `json:"running"`
	Total       int              `json:"total"`
	Status      string           `json:"status"` // running | partial | stopped | empty
	Editable    bool             `json:"editable"`
	// Writable: files can be saved back (Editable only means readable).
	Writable bool `json:"writable"`
	// Reason explains why the compose files can't be edited from here.
	Reason string `json:"reason,omitempty"`
	// Fix is set when Reason is a missing write permission the user can grant.
	Fix *permFix `json:"fix,omitempty"`
	// Location: a ducktainer stack kept in its own directory (not the store).
	Location *compose.Location `json:"location,omitempty"`
	// Node: the files live on the remote node (compose runs there).
	Node bool `json:"node,omitempty"`
}

// stored: the compose file is kept in ducktainer's store.
func (st *stackInfo) stored() bool { return st.Managed && st.Location == nil }

type rawContainer struct {
	ID     string `json:"Id"`
	Names  []string
	Image  string
	State  string
	Status string
	Labels map[string]string
}

func (s *Server) stacks(ctx context.Context, c *docker.Client, epName, lang string) (map[string]*stackInfo, error) {
	var cts []rawContainer
	if err := c.Get(ctx, "/containers/json", url.Values{"all": {"1"}, "filters": {`{"label":["` + lblProject + `"]}`}}, &cts); err != nil {
		return nil, err
	}
	local := c.Endpoint.Local()
	out := map[string]*stackInfo{}
	for _, ct := range cts {
		p := ct.Labels[lblProject]
		st := out[p]
		if st == nil {
			st = &stackInfo{Name: p}
			out[p] = st
		}
		if st.WorkingDir == "" {
			st.WorkingDir = ct.Labels[lblWorkDir]
			if f := ct.Labels[lblFiles]; f != "" {
				st.ConfigFiles = strings.Split(f, ",")
			}
			st.EnvFile = ct.Labels[lblEnvFile]
		}
		st.Containers = append(st.Containers, stackContainer{ID: ct.ID, Name: name(ct.Names), Service: ct.Labels[lblService], State: ct.State, Status: ct.Status, Image: ct.Image})
	}
	for _, n := range s.Stacks.Managed(epName) {
		if out[n] == nil {
			out[n] = &stackInfo{Name: n}
		}
		out[n].Managed = true
		if loc := s.Stacks.Location(epName, n); loc != nil {
			st := out[n]
			st.Location = loc
			st.WorkingDir, st.ConfigFiles, st.EnvFile = loc.Dir, []string{loc.ComposePath()}, loc.EnvFile
		}
	}
	for _, st := range out {
		svc := map[string]bool{}
		for _, ct := range st.Containers {
			svc[ct.Service] = true
			if ct.State == "running" {
				st.Running++
			}
		}
		for k := range svc {
			st.Services = append(st.Services, k)
		}
		sort.Strings(st.Services)
		sort.Slice(st.Containers, func(i, j int) bool { return st.Containers[i].Name < st.Containers[j].Name })
		st.Total = len(st.Containers)
		switch {
		case st.Total == 0:
			st.Status = "empty"
		case st.Running == st.Total:
			st.Status = "running"
		case st.Running == 0:
			st.Status = "stopped"
		default:
			st.Status = "partial"
		}
		st.Editable, st.Reason = editable(ctx, st, local, lang)
		st.Writable = st.Editable
		st.Node = !local && !st.stored()
		if st.Editable && local && !st.stored() {
			files, _ := compose.ExistingFiles(ctx, compose.LocalFS{}, st.ConfigFiles)
			for _, f := range files {
				if !compose.Writable(f) {
					st.Writable, st.Reason = false, i18n.T(lang, "no write permission on %s for user %s (read-only)", f, runUser())
					st.Fix = newPermFix(f)
					break
				}
			}
		}
		if st.Containers == nil {
			st.Containers = []stackContainer{}
		}
		if st.Services == nil {
			st.Services = []string{}
		}
	}
	return out, nil
}

func editable(ctx context.Context, st *stackInfo, local bool, lang string) (bool, string) {
	switch {
	case st.stored():
		return true, ""
	case len(st.ConfigFiles) == 0:
		return false, i18n.T(lang, "the containers do not record their compose files")
	case !local:
		return true, "" // read on the node when opened
	}
	files, _ := compose.ExistingFiles(ctx, compose.LocalFS{}, st.ConfigFiles)
	for _, f := range files {
		if _, err := os.Stat(f); err != nil {
			if os.IsPermission(err) {
				return false, i18n.T(lang, "user %s cannot access %s (check the directory permissions)", runUser(), f)
			}
			return false, i18n.T(lang, "%s not found (was the project moved or deleted?)", f)
		}
		if !compose.Readable(f) {
			return false, i18n.T(lang, "no read permission on %s for user %s", f, runUser())
		}
	}
	return true, ""
}

// permFix carries what the UI needs to explain, in a dialog, how to give the
// service user write access to a compose file (and its directory, where the
// .bak copy is made on save).
type permFix struct {
	File  string `json:"file"`
	Dir   string `json:"dir"`
	User  string `json:"user"`
	Group string `json:"group"`
}

func newPermFix(file string) *permFix {
	u := runUser()
	g := u
	if cu, err := user.Current(); err == nil {
		if gr, err := user.LookupGroupId(cu.Gid); err == nil {
			g = gr.Name
		}
	}
	return &permFix{File: file, Dir: filepath.Dir(file), User: u, Group: g}
}

func runUser() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return "?"
}

func (s *Server) listStacks(w http.ResponseWriter, r *http.Request) {
	c, err := s.ep(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	m, err := s.stacks(r.Context(), c, r.PathValue("ep"), reqLang(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	list := make([]*stackInfo, 0, len(m))
	for _, st := range m {
		list = append(list, st)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	writeJSON(w, list)
}

func (s *Server) stack(r *http.Request) (*docker.Client, *stackInfo, error) {
	c, err := s.ep(r)
	if err != nil {
		return nil, nil, err
	}
	m, err := s.stacks(r.Context(), c, r.PathValue("ep"), reqLang(r))
	if err != nil {
		return nil, nil, err
	}
	st, ok := m[r.PathValue("name")]
	if !ok {
		return nil, nil, herr(http.StatusNotFound, "stack not found: %s", r.PathValue("name"))
	}
	return c, st, nil
}

// fsFor reaches the files of an endpoint: this machine's for the local
// socket, the node's (through a helper container) otherwise.
func (s *Server) fsFor(c *docker.Client) compose.FS {
	if c.Endpoint.Local() {
		return compose.LocalFS{}
	}
	return compose.ForNode(c, s.registryAuth)
}

// files returns the compose files for a stack, if reachable.
func (s *Server) files(ctx context.Context, c *docker.Client, st *stackInfo) (*compose.Files, error) {
	if st.stored() {
		return s.Stacks.Read(c.Endpoint.Name, st.Name)
	}
	if !st.Editable {
		return nil, errors.New(st.Reason)
	}
	return compose.ReadExternal(ctx, s.fsFor(c), st.WorkingDir, st.ConfigFiles, st.EnvFile)
}

func (s *Server) getStack(w http.ResponseWriter, r *http.Request) {
	c, st, err := s.stack(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	resp := map[string]any{"stack": st}
	if f, err := s.files(r.Context(), c, st); err == nil {
		resp["files"] = f
	} else {
		resp["files_error"] = i18n.Render(err, reqLang(r))
	}
	writeJSON(w, resp)
}

func (s *Server) runner(ep string) (*compose.Runner, error) {
	e, ok := s.Cfg.Endpoint(ep)
	if !ok {
		return nil, herr(http.StatusNotFound, "unknown endpoint")
	}
	c, err := s.Client(ep)
	if err != nil {
		return nil, err
	}
	return &compose.Runner{Endpoint: e, Registries: s.Cfg.Snapshot().Registry, Client: c, Auth: s.registryAuth}, nil
}

// compose runs docker compose for a stack streaming its output.
func (s *Server) compose(ctx context.Context, st *stream, ep, project string, f *compose.Files, args ...string) error {
	rn, err := s.runner(ep)
	if err != nil {
		return err
	}
	lw := &lineWriter{s: st}
	defer lw.Flush()
	if err := rn.Exec(ctx, lw, lw, project, f, args...); err != nil {
		lw.Flush()
		if lw.last != "" && !strings.HasPrefix(lw.last, "$ ") {
			return fmt.Errorf("%s (%w)", lw.last, err)
		}
		return err
	}
	return nil
}

type stackReq struct {
	Name    string `json:"name"`
	Compose string `json:"compose"`
	// Files holds edited contents by path (multi-file external projects).
	Files  map[string]string `json:"files"`
	Env    string            `json:"env"`
	Deploy bool              `json:"deploy"`
	Pull   bool              `json:"pull"`
	// Location keeps the stack in a directory (on the node) instead of the
	// store; Existing uses the compose file already there.
	Location *compose.Location `json:"location"`
	Existing bool              `json:"existing"`
}

func (s *Server) createStack(w http.ResponseWriter, r *http.Request) {
	ep := r.PathValue("ep")
	var req stackReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	req.Name = strings.ToLower(strings.TrimSpace(req.Name))
	if err := compose.ValidName(req.Name); err != nil {
		writeErr(w, badRequest(err.Error()))
		return
	}
	if s.Stacks.Exists(ep, req.Name) {
		writeErr(w, herr(http.StatusConflict, "a stack with that name already exists"))
		return
	}
	if req.Location != nil && strings.TrimSpace(req.Location.Dir) == "" {
		req.Location = nil
	}
	if req.Location == nil {
		if _, err := s.Stacks.Write(ep, req.Name, req.Compose, req.Env); err != nil {
			writeErr(w, err)
			return
		}
	}
	st := newStream(w)
	ctx := context.WithoutCancel(r.Context())
	st.Close(func() error {
		var f *compose.Files
		var undo func()
		var err error
		if req.Location == nil {
			f, err = s.Stacks.Read(ep, req.Name)
			undo = func() { s.Stacks.Delete(ep, req.Name) }
		} else {
			f, undo, err = s.createInPlace(ctx, st, ep, req, s.Stacks)
		}
		if err != nil {
			return err
		}
		if err := s.compose(ctx, st, ep, req.Name, f, "config", "--quiet"); err != nil {
			undo()
			return i18n.Errorf("invalid compose, not saved: %v", err)
		}
		st.Log("Stack saved")
		if !req.Deploy {
			return nil
		}
		args := []string{"up", "-d", "--remove-orphans"}
		if req.Pull {
			args = append(args, "--pull", "always")
		}
		return s.compose(ctx, st, ep, req.Name, f, args...)
	}())
}

// createInPlace writes a new stack into its own directory (or registers
// the compose file already there) and returns how to undo it.
func (s *Server) createInPlace(ctx context.Context, st *stream, ep string, req stackReq, store *compose.Store) (*compose.Files, func(), error) {
	loc := *req.Location
	if err := loc.Validate(); err != nil {
		return nil, nil, err
	}
	c, err := s.Client(ep)
	if err != nil {
		return nil, nil, err
	}
	fsys := s.fsFor(c)
	found, err := compose.FindCompose(ctx, fsys, loc.Dir)
	if err != nil {
		return nil, nil, err
	}
	var created []string
	switch {
	case req.Existing && found == "":
		return nil, nil, i18n.Errorf("there is no compose file in %s", loc.Dir)
	case req.Existing:
		loc.File = found
	case found != "":
		return nil, nil, i18n.Errorf("%s already has a compose file (%s): use the existing files or pick another directory", loc.Dir, found)
	default:
		if err := fsys.WriteFile(ctx, loc.ComposePath(), []byte(req.Compose), 0o644); err != nil {
			return nil, nil, err
		}
		created = append(created, loc.ComposePath())
		if strings.TrimSpace(req.Env) != "" {
			had, _ := fsys.Exists(ctx, loc.EnvPath())
			if err := fsys.WriteFile(ctx, loc.EnvPath(), []byte(req.Env), 0o600); err != nil {
				fsys.Remove(ctx, loc.ComposePath())
				return nil, nil, err
			}
			if !had {
				created = append(created, loc.EnvPath())
			}
		}
		st.Log("Files written to %s", loc.Dir)
	}
	if err := store.SetLocation(ep, req.Name, loc); err != nil {
		return nil, nil, err
	}
	undo := func() {
		for _, p := range created {
			fsys.Remove(ctx, p)
		}
		store.Delete(ep, req.Name)
	}
	f, err := compose.ReadExternal(ctx, fsys, loc.Dir, []string{loc.ComposePath()}, loc.EnvFile)
	if err != nil {
		undo()
		return nil, nil, err
	}
	return f, undo, nil
}

func (s *Server) updateStack(w http.ResponseWriter, r *http.Request) {
	ep := r.PathValue("ep")
	name := r.PathValue("name")
	var req stackReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	var f *compose.Files
	var restore func()
	if s.Stacks.Stored(ep, name) {
		prev, _ := s.Stacks.Read(ep, name)
		content := req.Compose
		if content == "" {
			for _, v := range req.Files {
				content = v
			}
		}
		if _, err := s.Stacks.Write(ep, name, content, req.Env); err != nil {
			writeErr(w, err)
			return
		}
		f, _ = s.Stacks.Read(ep, name)
		restore = func() { s.Stacks.Write(ep, name, prev.Files[0].Content, prev.Env) }
	} else {
		c, info, err := s.stack(r)
		if err != nil {
			writeErr(w, err)
			return
		}
		prev, err := s.files(r.Context(), c, info)
		if err != nil {
			writeErr(w, err)
			return
		}
		if !info.Writable {
			writeErr(w, &httpError{http.StatusForbidden, errors.New(info.Reason)})
			return
		}
		fsys := s.fsFor(c)
		if err := compose.WriteExternal(r.Context(), fsys, prev, req.Files, req.Env); err != nil {
			writeErr(w, err)
			return
		}
		f, err = compose.ReadExternal(r.Context(), fsys, info.WorkingDir, info.ConfigFiles, info.EnvFile)
		if err != nil {
			writeErr(w, err)
			return
		}
		old := map[string]string{}
		for _, x := range prev.Files {
			old[x.Path] = x.Content
		}
		restore = func() { compose.WriteExternal(context.Background(), fsys, f, old, prev.Env) }
	}
	st := newStream(w)
	ctx := context.WithoutCancel(r.Context())
	st.Close(func() error {
		if err := s.compose(ctx, st, ep, name, f, "config", "--quiet"); err != nil {
			restore()
			return i18n.Errorf("invalid compose, the previous version was restored: %v", err)
		}
		st.Log("Changes saved")
		if !req.Deploy {
			return nil
		}
		args := []string{"up", "-d", "--remove-orphans"}
		if req.Pull {
			args = append(args, "--pull", "always")
		}
		return s.compose(ctx, st, ep, name, f, args...)
	}())
}

func (s *Server) stackAction(w http.ResponseWriter, r *http.Request) {
	ep := r.PathValue("ep")
	c, info, err := s.stack(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	f, ferr := s.files(r.Context(), c, info)
	var args []string
	needsFiles := false
	switch r.PathValue("action") {
	case "up":
		args, needsFiles = []string{"up", "-d", "--remove-orphans"}, true
	case "redeploy":
		args, needsFiles = []string{"up", "-d", "--remove-orphans", "--pull", "always", "--force-recreate"}, true
	case "pull":
		args, needsFiles = []string{"pull"}, true
	case "down":
		args = []string{"down", "--remove-orphans"}
	case "stop", "start", "restart", "pause", "unpause":
		args = []string{r.PathValue("action")}
	default:
		writeErr(w, badRequest("unknown action"))
		return
	}
	if needsFiles && ferr != nil {
		writeErr(w, badRequest(ferr.Error()))
		return
	}
	if ferr != nil {
		f = nil
	}
	st := newStream(w)
	st.Close(s.compose(context.WithoutCancel(r.Context()), st, ep, info.Name, f, args...))
}

func (s *Server) deleteStack(w http.ResponseWriter, r *http.Request) {
	ep := r.PathValue("ep")
	name := r.PathValue("name")
	c, info, err := s.stack(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	st := newStream(w)
	st.Close(func() error {
		ctx := context.WithoutCancel(r.Context())
		if info.Total > 0 {
			args := []string{"down", "--remove-orphans"}
			if r.URL.Query().Get("volumes") == "1" {
				args = append(args, "--volumes")
			}
			f, ferr := s.files(ctx, c, info)
			if ferr != nil {
				f = nil
			}
			if err := s.compose(ctx, st, ep, name, f, args...); err != nil {
				return err
			}
		}
		if info.Managed {
			if err := s.Stacks.Delete(ep, name); err != nil {
				return err
			}
			if info.Location != nil {
				st.Log("ducktainer no longer tracks the stack; its files in %s were kept", info.Location.Dir)
			} else {
				st.Log("Stack files removed")
			}
		}
		return nil
	}())
}

// adoptStack copies an external project's files into ducktainer's store so it
// can be managed (and edited) from here.
func (s *Server) adoptStack(w http.ResponseWriter, r *http.Request) {
	ep := r.PathValue("ep")
	c, info, err := s.stack(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if info.Managed {
		writeErr(w, badRequest("the stack is already managed by ducktainer"))
		return
	}
	var body struct {
		Compose string `json:"compose"`
		Env     string `json:"env"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	if body.Compose == "" {
		f, err := s.files(r.Context(), c, info)
		if err != nil {
			writeErr(w, badRequest("%v — paste the compose content to adopt it", err))
			return
		}
		if len(f.Files) == 1 { // track it in place: paths keep resolving where they did
			loc := compose.Location{Dir: f.Dir, File: f.Files[0].Path, EnvFile: info.EnvFile}
			if path.Dir(loc.File) == loc.Dir {
				loc.File = path.Base(loc.File)
			}
			if err := loc.Validate(); err != nil {
				writeErr(w, badRequest(err.Error()))
				return
			}
			if err := s.Stacks.SetLocation(ep, info.Name, loc); err != nil {
				writeErr(w, err)
				return
			}
			writeJSON(w, map[string]bool{"ok": true})
			return
		}
		body.Env = f.Env
		body.Compose = f.Files[0].Content
		if len(f.Files) > 1 { // merge base + overrides into one file, keeping ${VARS}
			merged, err := s.mergedConfig(r.Context(), ep, info.Name, f)
			if err != nil {
				writeErr(w, err)
				return
			}
			body.Compose = merged
		}
	}
	if _, err := s.Stacks.Write(ep, info.Name, body.Compose, body.Env); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// stackLogs merges the log streams of every container in a project.
func (s *Server) stackLogs(w http.ResponseWriter, r *http.Request) {
	c, info, err := s.stack(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	st := newStream(w)
	q := logQuery(r)
	svc := r.URL.Query().Get("service")
	var wg sync.WaitGroup
	for _, ct := range info.Containers {
		if svc != "" && ct.Service != svc {
			continue
		}
		var in struct {
			Config struct{ Tty bool }
		}
		if err := c.Get(r.Context(), "/containers/"+ct.ID+"/json", nil, &in); err != nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			label := ct.Service
			if label == "" {
				label = ct.Name
			}
			if err := streamLogs(r.Context(), c, ct.ID, in.Config.Tty, q, label, st); err != nil && r.Context().Err() == nil {
				b, _ := json.Marshal(logLine{S: "err", M: "[ducktainer] " + err.Error(), C: label})
				st.Raw(b)
			}
		}()
	}
	wg.Wait()
	st.Close(nil)
}

// mergedConfig returns `docker compose config --no-interpolate` for a
// multi-file project: one equivalent compose file with variables intact.
func (s *Server) mergedConfig(ctx context.Context, ep, project string, f *compose.Files) (string, error) {
	rn, err := s.runner(ep)
	if err != nil {
		return "", err
	}
	out, err := rn.Output(ctx, project, f, "config", "--no-interpolate")
	if err != nil {
		return "", fmt.Errorf("compose config: %w", err)
	}
	return out, nil
}

func (s *Server) stackMerged(w http.ResponseWriter, r *http.Request) {
	c, info, err := s.stack(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	f, err := s.files(r.Context(), c, info)
	if err != nil {
		writeErr(w, badRequest(err.Error()))
		return
	}
	merged, err := s.mergedConfig(r.Context(), r.PathValue("ep"), info.Name, f)
	if err != nil {
		writeErr(w, badRequest(err.Error()))
		return
	}
	writeJSON(w, map[string]string{"compose": merged})
}

// stackMerge folds base + override files into the base file in place.
func (s *Server) stackMerge(w http.ResponseWriter, r *http.Request) {
	ep := r.PathValue("ep")
	c, info, err := s.stack(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if info.Managed {
		writeErr(w, badRequest("ducktainer stacks already use a single file"))
		return
	}
	if !info.Writable {
		writeErr(w, &httpError{http.StatusForbidden, errors.New(info.Reason)})
		return
	}
	f, err := s.files(r.Context(), c, info)
	if err != nil {
		writeErr(w, badRequest(err.Error()))
		return
	}
	merged, err := s.mergedConfig(r.Context(), ep, info.Name, f)
	if err != nil {
		writeErr(w, badRequest(err.Error()))
		return
	}
	moved, err := compose.Merge(r.Context(), s.fsFor(c), f, merged)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "file": f.Files[0].Path, "backups": append([]string{f.Files[0].Path + compose.BackupSuffix}, moved...)})
}

// stackLocation moves a stack kept in the store to its own directory, or
// points a stack at another directory that already holds its compose file.
func (s *Server) stackLocation(w http.ResponseWriter, r *http.Request) {
	ep, name := r.PathValue("ep"), r.PathValue("name")
	var loc compose.Location
	if err := readJSON(r, &loc); err != nil {
		writeErr(w, err)
		return
	}
	if err := loc.Validate(); err != nil {
		writeErr(w, badRequest(err.Error()))
		return
	}
	c, info, err := s.stack(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !info.Managed {
		writeErr(w, badRequest("adopt the stack first"))
		return
	}
	ctx := r.Context()
	fsys := s.fsFor(c)
	found, err := compose.FindCompose(ctx, fsys, loc.Dir)
	if err != nil {
		writeErr(w, err)
		return
	}
	if info.stored() {
		if found != "" {
			writeErr(w, herr(http.StatusConflict, "%s already has a compose file (%s): use the existing files or pick another directory", loc.Dir, found))
			return
		}
		f, err := s.Stacks.Read(ep, name)
		if err != nil {
			writeErr(w, err)
			return
		}
		if err := fsys.WriteFile(ctx, loc.ComposePath(), []byte(f.Files[0].Content), 0o644); err != nil {
			writeErr(w, err)
			return
		}
		if strings.TrimSpace(f.Env) != "" {
			if err := fsys.WriteFile(ctx, loc.EnvPath(), []byte(f.Env), 0o600); err != nil {
				writeErr(w, err)
				return
			}
		}
	} else {
		if found == "" {
			writeErr(w, badRequest("there is no compose file in %s", loc.Dir))
			return
		}
		loc.File = found
	}
	if err := s.Stacks.SetLocation(ep, name, loc); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "location": loc})
}
