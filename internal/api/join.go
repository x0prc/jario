// Cluster membership over HTTP: POST /internal/join (no SigV4).
// A new node starts Raft un-bootstrapped and asks an existing node to
// admit it; the leader's AddVoter replicates the new configuration and
// the joiner picks it up automatically. Firewall this in production.
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/c0ldheat/jario/internal/store"
)

// Joiner admits a voter to the Raft cluster. Implemented by *raft.RaftNode.
type Joiner interface {
	AddVoter(id, addr string) error
}

// SetJoiner wires cluster membership handling. Nil disables /internal/join.
func (h *Handler) SetJoiner(j Joiner) {
	h.joiner = j
}

// joinPayload is the POST /internal/join body.
type joinPayload struct {
	ID   string `json:"id"`
	Addr string `json:"addr"`
}

// routeInternal serves operator endpoints without SigV4 auth.
func (h *Handler) routeInternal(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/internal/join" || r.Method != http.MethodPost {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var p joinPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if p.ID == "" || p.Addr == "" {
		http.Error(w, "id and addr are required", http.StatusBadRequest)
		return
	}
	if h.joiner == nil {
		http.Error(w, "clustering disabled", http.StatusServiceUnavailable)
		return
	}
	if err := h.joiner.AddVoter(p.ID, p.Addr); err != nil {
		if errors.Is(err, store.ErrNotLeader) {
			http.Error(w, "not leader", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "join failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// RequestJoin asks the node at leaderHTTPAddr to admit (id, addr) as a
// voter. leaderHTTPAddr may be "host:port" or a full URL.
func RequestJoin(leaderHTTPAddr, id, addr string) error {
	base := leaderHTTPAddr
	if !strings.Contains(base, "://") {
		base = "http://" + base
	}
	body, err := json.Marshal(joinPayload{ID: id, Addr: addr})
	if err != nil {
		return fmt.Errorf("marshal join request: %w", err)
	}
	resp, err := http.Post(strings.TrimSuffix(base, "/")+"/internal/join", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("post join: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("join rejected: %s (%s)", resp.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}
