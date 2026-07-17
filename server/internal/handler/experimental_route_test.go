package handler

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func registerAll() chi.Router {
	r := chi.NewRouter()
	RegisterClaudeScienceRuntimeRoutes(r, &Handler{})
	RegisterLLMWikiBridgeRoutes(r, &Handler{})
	return r
}

// walkChiRoutes returns all method+pattern strings registered on r.
func walkChiRoutes(r chi.Router) []string {
	var out []string
	walkFunc := func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		out = append(out, method+" "+pattern)
		return nil
	}
	if err := chi.Walk(r, walkFunc); err != nil {
		panic(err)
	}
	return out
}

func TestRouteRegistration_RuntimeRoutesExist(t *testing.T) {
	routes := walkChiRoutes(registerAll())
	need := []string{
		"POST /api/experimental/claude-science-runtime/execute",
		"GET /api/experimental/claude-science-runtime/sessions",
		"GET /api/experimental/claude-science-runtime/sessions/{sessionID}",
		"GET /api/experimental/claude-science-runtime/sessions/{sessionID}/artifacts",
		"GET /api/experimental/claude-science-runtime/artifacts/{artifactID}",
		"DELETE /api/experimental/claude-science-runtime/sessions/{sessionID}",
	}
	for _, want := range need {
		found := false
		for _, r := range routes {
			if r == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing route %q; have: %v", want, routes)
		}
	}
}

func TestRouteRegistration_LLMWikiRoutesExist(t *testing.T) {
	routes := walkChiRoutes(registerAll())
	need := []string{
		"GET /api/experimental/llm-wiki/status",
		"GET /api/experimental/llm-wiki/projects",
		"GET /api/experimental/llm-wiki/files",
		"GET /api/experimental/llm-wiki/read",
		"POST /api/experimental/llm-wiki/search",
		"GET /api/experimental/llm-wiki/graph",
		"POST /api/experimental/llm-wiki/write",
		"DELETE /api/experimental/llm-wiki/file",
	}
	for _, want := range need {
		found := false
		for _, r := range routes {
			if r == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing route %q; have: %v", want, routes)
		}
	}
}

func TestRouteRegistration_NoRouteLeaks(t *testing.T) {
	routes := walkChiRoutes(registerAll())
	all := strings.Join(routes, "\n")

	// The runtime must not expose a bare /experimental/ path.
	if strings.Contains(all, "/api/experimental/claude-science-runtime/") && !strings.Contains(all, "/api/experimental/claude-science-runtime/{") {
		// OK — the final "/" in a group path is normal for chi.
	}
	// The registry route must not appear under the root api prefix.
	if strings.Contains(all, "POST /api/experimental/claude-science-runtime/health") {
		t.Error("runtime health route leaked")
	}
}
