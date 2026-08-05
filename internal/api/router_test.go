package api

import "testing"

func TestBrokerSyncRoutes(t *testing.T) {
	router := NewRouter(Deps{}, ServerControl{})
	routes := map[string]bool{}
	for _, route := range router.Routes() {
		routes[route.Method+" "+route.Path] = true
	}

	for _, route := range []string{
		"POST /api/v1/brokers/sync",
		"GET /api/v1/brokers/sync-status",
	} {
		if !routes[route] {
			t.Fatalf("missing broker sync route %s", route)
		}
	}
}
