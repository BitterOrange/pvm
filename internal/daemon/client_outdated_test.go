package daemon

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Only the mux's own 404 for an unknown route means an older daemon; a
// handler 404 stays an APIError.
func TestDecodeRespOutdatedDaemon(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/vms/{name}", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, 404, errors.New("vm \"dev1\" not found"))
	})
	mux.HandleFunc("POST /v1/plain", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", 500)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	post := func(path string) error {
		resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return decodeResp(resp, nil)
	}

	err := post("/v1/exits/my-exit/probe")
	if !errors.Is(err, ErrDaemonOutdated) || !strings.Contains(err.Error(), "POST /v1/exits/my-exit/probe") {
		t.Fatalf("unknown route: got %v, want ErrDaemonOutdated naming the request", err)
	}
	err = post("/v1/vms/dev1")
	var api *APIError
	if errors.Is(err, ErrDaemonOutdated) || !errors.As(err, &api) || api.HTTPStatus != 404 {
		t.Fatalf("handler 404: got %v, want an APIError", err)
	}
	err = post("/v1/plain")
	if errors.Is(err, ErrDaemonOutdated) || err == nil || !strings.Contains(err.Error(), "daemon http 500") {
		t.Fatalf("plain 500: got %v", err)
	}
}
