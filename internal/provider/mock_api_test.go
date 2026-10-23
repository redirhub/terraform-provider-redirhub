package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// mockAPI is an in-memory stand-in for the RedirHub v1 API, shaped like the real responses:
// single items unwrapped, created links and lists under `data`, URLs normalised, unset fields
// null. It lets the acceptance tests run on every push without credentials.
type mockAPI struct {
	mu      sync.Mutex
	links   map[string]map[string]any
	domains map[string]map[string]any
	// replies holds the stored response per Idempotency-Key, as the API keeps it.
	replies map[string][]byte
	nextID  int
	// failNextCreate drops the response of the next POST /links after the link is created,
	// as a timeout between the API and the client would.
	failNextCreate bool
	// refreshesUntilCorrect is how many DNS checks a domain takes before it points to RedirHub.
	refreshesUntilCorrect int
	creates               int
}

func newMockAPI(t *testing.T) (*mockAPI, *httptest.Server) {
	m := &mockAPI{links: map[string]map[string]any{}, domains: map[string]map[string]any{}, replies: map[string][]byte{}, refreshesUntilCorrect: 2}
	server := httptest.NewServer(m)
	t.Cleanup(server.Close)
	return m, server
}

func (m *mockAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if r.Header.Get("Authorization") != "Bearer test-token" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"message": "Unauthenticated."})
		return
	}

	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1"), "/"), "/")

	key := r.Header.Get("Idempotency-Key")
	if r.Method == http.MethodPost && key != "" {
		if reply, ok := m.replies[key]; ok {
			w.Header().Set("Idempotent-Replayed", "true")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(reply)
			return
		}
	}

	switch {
	case parts[0] == "links" && len(parts) == 1 && r.Method == http.MethodPost:
		m.creates++
		link := m.createLink(body)
		reply, _ := json.Marshal(map[string]any{"data": []any{link}})
		if key != "" {
			m.replies[key] = reply
		}
		if m.failNextCreate {
			m.failNextCreate = false
			w.Header().Set("Retry-After", "0")
			writeJSON(w, http.StatusBadGateway, map[string]any{"message": "Bad gateway"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(reply)
	case parts[0] == "links" && len(parts) == 1 && r.Method == http.MethodGet:
		list := []any{}
		for i := m.nextID; i > 0; i-- {
			link, ok := m.links[fmt.Sprintf("link_%d", i)]
			if !ok {
				continue
			}
			if h := r.URL.Query().Get("filter[handler]"); h != "" && !strings.Contains(","+h+",", ","+link["handler"].(string)+",") {
				continue
			}
			if h := r.URL.Query().Get("filter[host]"); h != "" && link["host"] != h {
				continue
			}
			list = append(list, link)
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": list, "meta": map[string]any{"cursor": map[string]any{"next": nil}}})
	case parts[0] == "links" && len(parts) == 2:
		link, ok := m.links[parts[1]]
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"message": "No query results."})
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, link)
		case http.MethodPut:
			if utm, ok := body["utm"]; ok && utm == nil {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"message": "The utm must be an array.", "errors": map[string]any{"utm": []string{"The utm must be an array."}}})
				return
			}
			m.applyLink(link, body)
			writeJSON(w, http.StatusOK, link)
		case http.MethodDelete:
			delete(m.links, parts[1])
			w.WriteHeader(http.StatusNoContent)
		}
	case parts[0] == "host" && len(parts) == 1 && r.Method == http.MethodPost:
		host := body["host"].(string)
		domain := map[string]any{"host": host, "correct": false, "default_require": "cname", "https_requested": body["https_requested"], "force_https": false, "short_links_enabled": body["short_links_enabled"], "refreshes": 0}
		m.domains[host] = domain
		writeJSON(w, http.StatusOK, m.domainJSON(domain))
	case parts[0] == "host" && len(parts) >= 2:
		domain, ok := m.domains[parts[1]]
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not found."})
			return
		}
		switch {
		case len(parts) == 3 && parts[2] == "refresh":
			domain["refreshes"] = domain["refreshes"].(int) + 1
			domain["correct"] = domain["refreshes"].(int) >= m.refreshesUntilCorrect
			writeJSON(w, http.StatusOK, m.domainJSON(domain))
		case r.Method == http.MethodGet:
			writeJSON(w, http.StatusOK, m.domainJSON(domain))
		case r.Method == http.MethodPut:
			for k, v := range body {
				domain[k] = v
			}
			writeJSON(w, http.StatusOK, m.domainJSON(domain))
		case r.Method == http.MethodDelete:
			delete(m.domains, parts[1])
			w.WriteHeader(http.StatusNoContent)
		}
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"message": "Method not allowed."})
	}
}

func (m *mockAPI) createLink(body map[string]any) map[string]any {
	m.nextID++
	handler := body["handler"].(string)
	link := map[string]any{
		"id": fmt.Sprintf("link_%d", m.nextID), "handler": handler, "https": false,
		"destination_routing": "random", "type": nil, "title": nil, "description": nil,
		"status": "active", "paused_by": nil, "forward_path": nil, "forward_query": nil,
		"utm": nil, "qr": nil, "tags": []string{}, "has_password": false,
	}
	if isShortLink(handler) {
		alias, _ := body["alias"].(string)
		if alias == "" {
			alias = fmt.Sprintf("a%d", m.nextID)
		}
		link["host"], link["alias"] = body["host"], alias
		link["url"] = fmt.Sprintf("%s/%s", body["host"], alias)
		if handler == handlerQR {
			link["qr"] = map[string]any{"theme": "light", "color": "#181D27", "logo": false, "show_url": true, "margin": "m"}
		}
	} else {
		u := strings.TrimPrefix(strings.TrimPrefix(body["url"].(string), "https://"), "http://")
		host, rest, _ := strings.Cut(u, "/")
		link["host"], link["alias"] = strings.ToLower(host), nil
		link["url"] = strings.ToLower(host) + "/" + strings.TrimRight(rest, "/")
	}
	m.applyLink(link, body)
	m.links[link["id"].(string)] = link
	return link
}

func (m *mockAPI) applyLink(link, body map[string]any) {
	for _, field := range []string{"destinations", "destination_routing", "type", "title", "description", "forward_path", "forward_query", "tags"} {
		if v, ok := body[field]; ok {
			link[field] = v
		}
	}
	if utm, ok := body["utm"].(map[string]any); ok {
		if len(utm) == 0 {
			link["utm"] = nil
		} else {
			link["utm"] = utm
		}
	}
	if qr, ok := body["qr"].(map[string]any); ok {
		style, _ := link["qr"].(map[string]any)
		if style == nil {
			style = map[string]any{"theme": "light", "color": "#181D27", "logo": false, "show_url": true, "margin": "m"}
		}
		for k, v := range qr {
			style[k] = v
		}
		link["qr"] = style
	}
	if status, ok := body["status"].(float64); ok {
		if status == 0 {
			link["status"], link["paused_by"] = "paused", "user"
		} else {
			link["status"], link["paused_by"] = "active", nil
		}
	}
	if password, ok := body["password"]; ok {
		link["has_password"] = password != nil
	}
}

func (m *mockAPI) domainJSON(d map[string]any) map[string]any {
	name := strings.SplitN(d["host"].(string), ".", 2)[0]
	out := map[string]any{"require": []any{
		map[string]any{"scope": "cname", "name": name, "type": "CNAME", "value": "edge.example.net."},
		map[string]any{"scope": "ip", "name": name, "type": "A", "value": "192.0.2.10"},
		map[string]any{"scope": "verfication", "name": name, "type": "TXT", "value": "reh-verify=abc"},
		map[string]any{"scope": "ssl", "status": "pending"},
	}}
	for k, v := range d {
		if k != "refreshes" {
			out[k] = v
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
