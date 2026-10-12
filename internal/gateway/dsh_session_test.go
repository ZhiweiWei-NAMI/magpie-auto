package gateway

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

func TestDshSessionReachesExistingWorkBuddyConnection(t *testing.T) {
	for _, id := range []string{"dsh-workbuddy", "other-local"} {
		t.Run(id, func(t *testing.T) {
			f := &fake{t: t, ctype: "application/json", reply: `{"id":"c1","model":"m1","choices":[]}`}
			up := setup(t, provider.Chat, f)
			if err := provider.Save(provider.Provider{ID: id, Name: id, Key: "k", Chat: up.URL + "/v1", Models: []string{"m1"}}); err != nil {
				t.Fatal(err)
			}
			server := New()
			for _, turn := range []string{"first turn", "second turn"} {
				request := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"`+id+`/m1","messages":[{"role":"user","content":"`+turn+`"}]}`))
				request.Header.Set("x-deepseek-harness-session-id", "native-dsh-session")
				response := httptest.NewRecorder()
				server.Handler().ServeHTTP(response, request)
				if response.Code != 200 {
					t.Fatalf("request: %d %s", response.Code, response.Body.String())
				}
				want := ""
				if id == "dsh-workbuddy" {
					want = "native-dsh-session"
				}
				if got := f.head.Get("x-wb-session-id"); got != want {
					t.Fatalf("upstream session = %q, want %q", got, want)
				}
				if got := sessionOf(request.Header); got != "native-dsh-session" {
					t.Fatalf("gateway session = %q", got)
				}
			}
		})
	}
}
