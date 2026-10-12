package gui

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"path/filepath"
	"sync"

	autowork "github.com/yetone/magpie/internal/auto"
	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/settings"
)

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
		gw := served.Load()
		if gw == nil {
			gw = gateway.New()
		}
		m, err := autowork.New(filepath.Join(settings.Dir(), "auto"), gw.AutoChat, gateway.AutoModelError)
		if err != nil {
			return nil, err
		}
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

// Auto shares the console's authentication and owns its requests and tool runs.
func autoRoutes(mux *http.ServeMux) {
	decode := func(w http.ResponseWriter, r *http.Request, value any) error {
		return json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(value)
	}
	handle := func(pattern string, f func(http.ResponseWriter, *http.Request, *autowork.Manager) (any, error)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			m, err := autoManager()
			var result any
			if err == nil {
				result, err = f(w, r, m)
			}
			if err != nil {
				fail(w, err)
				return
			}
			writeJSON(w, result)
		})
	}
	handle("GET /api/auto", func(_ http.ResponseWriter, _ *http.Request, m *autowork.Manager) (any, error) {
		return m.Snapshot(), nil
	})
	handle("GET /api/auto/tasks/{id}", func(_ http.ResponseWriter, r *http.Request, m *autowork.Manager) (any, error) {
		return m.Task(r.PathValue("id"))
	})
	mux.HandleFunc("GET /api/auto/tasks/{id}/artifacts/{artifact}", func(w http.ResponseWriter, r *http.Request) {
		m, err := autoManager()
		if err != nil {
			fail(w, err)
			return
		}
		f, err := m.OpenArtifact(r.PathValue("id"), r.PathValue("artifact"))
		if err != nil {
			fail(w, err)
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			fail(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": info.Name()}))
		http.ServeContent(w, r, info.Name(), info.ModTime(), f)
	})
	handle("POST /api/auto/config", func(w http.ResponseWriter, r *http.Request, m *autowork.Manager) (any, error) {
		var c autowork.Config
		if err := decode(w, r, &c); err != nil {
			return nil, err
		}
		if err := m.SetConfig(c); err != nil {
			return nil, err
		}
		return m.Snapshot(), nil
	})
	handle("POST /api/auto/tasks", func(w http.ResponseWriter, r *http.Request, m *autowork.Manager) (any, error) {
		var in autowork.CreateInput
		if err := decode(w, r, &in); err != nil {
			return nil, err
		}
		return m.Create(in)
	})
	for _, action := range []string{"start", "continue"} {
		handle("POST /api/auto/tasks/{id}/"+action, func(w http.ResponseWriter, r *http.Request, m *autowork.Manager) (any, error) {
			var in autowork.RunRequest
			if err := decode(w, r, &in); err != nil {
				return nil, err
			}
			return m.Start(r.PathValue("id"), in)
		})
	}
	handle("POST /api/auto/tasks/{id}/cancel", func(_ http.ResponseWriter, r *http.Request, m *autowork.Manager) (any, error) {
		if err := m.Cancel(r.PathValue("id")); err != nil {
			return nil, err
		}
		return m.Task(r.PathValue("id"))
	})
	handle("POST /api/auto/tasks/{id}/feedback", func(w http.ResponseWriter, r *http.Request, m *autowork.Manager) (any, error) {
		var in autowork.FeedbackInput
		if err := decode(w, r, &in); err != nil {
			return nil, err
		}
		if err := m.Feedback(r.PathValue("id"), in); err != nil {
			return nil, err
		}
		return m.Task(r.PathValue("id"))
	})
}
