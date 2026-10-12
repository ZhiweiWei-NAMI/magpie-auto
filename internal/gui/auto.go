package gui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	autowork "github.com/yetone/magpie/internal/auto"
	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/settings"
)

// The manager belongs to this backend, not an HTTP request or a webview.
// Leaving a page or closing a connection cannot cancel an owned execution.
var autoBackend struct {
	sync.Mutex
	manager *autowork.Manager
}

func autoManager() (*autowork.Manager, error) {
	autoBackend.Lock()
	defer autoBackend.Unlock()
	if backendCtx.Err() != nil {
		return nil, errors.New("Auto backend has stopped")
	}
	if autoBackend.manager == nil {
		m, err := autowork.New(filepath.Join(settings.Dir(), "auto"), autowork.CodexExecutor{})
		if err != nil {
			return nil, err
		}
		gw := served.Load()
		if gw == nil {
			gw = gateway.New()
		}
		m.UseDecisionTransport(gw.SystemOneDecision)
		autoBackend.manager = m
	}
	return autoBackend.manager, nil
}

func stopAuto() {
	autoBackend.Lock()
	m := autoBackend.manager
	autoBackend.Unlock()
	if m != nil {
		_ = m.Close()
	}
}

func autoRoutes(mux *http.ServeMux) {
	manager := func(rw http.ResponseWriter) *autowork.Manager {
		m, err := autoManager()
		if err != nil {
			fail(rw, err)
			return nil
		}
		rw.Header().Set("Cache-Control", "no-store")
		return m
	}
	decode := func(rw http.ResponseWriter, r *http.Request, out any) bool {
		err := json.NewDecoder(http.MaxBytesReader(rw, r.Body, 1<<20)).Decode(out)
		if err != nil {
			fail(rw, err)
			return false
		}
		return true
	}
	mux.HandleFunc("GET /api/auto", func(rw http.ResponseWriter, r *http.Request) {
		if m := manager(rw); m != nil {
			writeJSON(rw, m.Snapshot())
		}
	})
	mux.HandleFunc("POST /api/auto/config", func(rw http.ResponseWriter, r *http.Request) {
		m := manager(rw)
		if m == nil {
			return
		}
		var in autowork.Config
		if !decode(rw, r, &in) {
			return
		}
		if err := m.SetConfig(in); err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, m.Snapshot())
	})
	mux.HandleFunc("POST /api/auto/tasks", func(rw http.ResponseWriter, r *http.Request) {
		m := manager(rw)
		if m == nil {
			return
		}
		var in autowork.CreateInput
		if !decode(rw, r, &in) {
			return
		}
		t, err := m.Create(in)
		if err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, t)
	})
	mux.HandleFunc("GET /api/auto/tasks/{id}", func(rw http.ResponseWriter, r *http.Request) {
		m := manager(rw)
		if m == nil {
			return
		}
		t, err := m.Task(r.PathValue("id"))
		if err != nil {
			http.Error(rw, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(rw, t)
	})
	for _, action := range []string{"start", "continue"} {
		mux.HandleFunc("POST /api/auto/tasks/{id}/"+action, func(rw http.ResponseWriter, r *http.Request) {
			m := manager(rw)
			if m == nil {
				return
			}
			var in autowork.RunRequest
			if !decode(rw, r, &in) {
				return
			}
			var t autowork.Task
			var err error
			if action == "start" {
				t, err = m.Start(r.PathValue("id"), in)
			} else {
				t, err = m.Continue(r.PathValue("id"), in)
			}
			if err != nil {
				fail(rw, err)
				return
			}
			writeJSON(rw, t)
		})
	}
	mux.HandleFunc("POST /api/auto/tasks/{id}/cancel", func(rw http.ResponseWriter, r *http.Request) {
		m := manager(rw)
		if m == nil {
			return
		}
		if err := m.Cancel(r.PathValue("id")); err != nil {
			fail(rw, err)
			return
		}
		t, err := m.Task(r.PathValue("id"))
		if err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, t)
	})
	mux.HandleFunc("POST /api/auto/tasks/{id}/feedback", func(rw http.ResponseWriter, r *http.Request) {
		m := manager(rw)
		if m == nil {
			return
		}
		var in autowork.FeedbackInput
		if !decode(rw, r, &in) {
			return
		}
		if err := m.Feedback(r.PathValue("id"), in); err != nil {
			fail(rw, err)
			return
		}
		t, err := m.Task(r.PathValue("id"))
		if err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, t)
	})
	mux.HandleFunc("POST /api/auto/tasks/{id}/actions/{action}", func(rw http.ResponseWriter, r *http.Request) {
		m := manager(rw)
		if m == nil {
			return
		}
		var in autowork.ActionResponse
		if !decode(rw, r, &in) {
			return
		}
		if err := m.ReplyAction(r.PathValue("id"), r.PathValue("action"), in); err != nil {
			fail(rw, err)
			return
		}
		t, err := m.Task(r.PathValue("id"))
		if err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, t)
	})
	mux.HandleFunc("GET /api/auto/tasks/{id}/artifacts/{artifact}", func(rw http.ResponseWriter, r *http.Request) {
		m := manager(rw)
		if m == nil {
			return
		}
		t, err := m.Task(r.PathValue("id"))
		if err != nil {
			http.NotFound(rw, r)
			return
		}
		for _, a := range t.Artifacts {
			if a.ID != r.PathValue("artifact") {
				continue
			}
			owner := t
			if a.TaskID != "" && a.TaskID != t.ID {
				owner, err = m.Task(a.TaskID)
				if err != nil {
					http.NotFound(rw, r)
					return
				}
				// Resolve only an exact recorded child artifact reference. The
				// recorded owner's run retains the original workspace snapshot.
				found := false
				for _, actual := range owner.Artifacts {
					if actual.ID == a.ID && actual.RunID == a.RunID && actual.SourceID == a.SourceID && actual.Path == a.Path {
						found = true
						break
					}
				}
				if !found {
					http.NotFound(rw, r)
					return
				}
			}
			for _, run := range owner.Runs {
				if run.ID != a.RunID {
					continue
				}
				file, err := autoArtifactPath(run.Route.CWD, a.Path)
				if err != nil {
					fail(rw, err)
					return
				}
				rw.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(filepath.Base(file)))
				http.ServeFile(rw, r, file)
				return
			}
		}
		http.NotFound(rw, r)
	})
}

// Only recorded regular artifacts within the execution's original workspace
// can be downloaded. Resolve symlinks before checking the containment boundary.
func autoArtifactPath(cwd, path string) (string, error) {
	if cwd == "" || path == "" {
		return "", errors.New("artifact has no recorded workspace")
	}
	root, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	file, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	file, err = filepath.Abs(file)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, file)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", errors.New("artifact is outside its execution workspace")
	}
	info, err := os.Stat(file)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("artifact is not a regular file")
	}
	return file, nil
}
