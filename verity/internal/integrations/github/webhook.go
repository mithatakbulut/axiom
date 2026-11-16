package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

var ErrWebhookSignature = errors.New("invalid GitHub webhook signature")
var ErrWebhookDelivery = errors.New("missing GitHub webhook delivery metadata")

type DeliveryStore interface {
	ClaimWebhookDelivery(context.Context, string, string) (bool, error)
}
type retryableDeliveryStore interface {
	ReleaseWebhookDelivery(context.Context, string) error
}
type EventHandler interface {
	HandleGitHubEvent(context.Context, Event) error
}

// AtomicEventHandler commits delivery deduplication and its local effects together.
type AtomicEventHandler interface {
	HandleGitHubEventAtomic(context.Context, Event) error
}

type Event struct {
	DeliveryID    string `json:"delivery_id"`
	Type          string `json:"type"`
	Action        string `json:"action,omitempty"`
	Repository    string `json:"repository"`
	Number        int    `json:"number,omitempty"`
	HeadSHA       string `json:"head_sha,omitempty"`
	BaseSHA       string `json:"base_sha,omitempty"`
	Ref           string `json:"ref,omitempty"`
	WorkflowRunID int64  `json:"workflow_run_id,omitempty"`
	Conclusion    string `json:"conclusion,omitempty"`
}

type Webhook struct {
	Secret  []byte
	Store   DeliveryStore
	Handler EventHandler
}

func (w Webhook) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_, atomic := w.Handler.(AtomicEventHandler)
	if len(w.Secret) == 0 || w.Handler == nil || (w.Store == nil && !atomic) {
		http.Error(rw, "webhook is not configured", http.StatusServiceUnavailable)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(rw, r.Body, 1<<20))
	if err != nil {
		http.Error(rw, "invalid webhook body", http.StatusRequestEntityTooLarge)
		return
	}
	if !validSignature(w.Secret, body, r.Header.Get("X-Hub-Signature-256")) {
		http.Error(rw, ErrWebhookSignature.Error(), http.StatusUnauthorized)
		return
	}
	id, kind := r.Header.Get("X-GitHub-Delivery"), r.Header.Get("X-GitHub-Event")
	if id == "" || kind == "" {
		http.Error(rw, ErrWebhookDelivery.Error(), http.StatusBadRequest)
		return
	}
	event, err := decodeEvent(kind, id, body)
	if err != nil {
		http.Error(rw, "unsupported or invalid webhook event", http.StatusBadRequest)
		return
	}
	if handler, ok := w.Handler.(AtomicEventHandler); ok {
		if err = handler.HandleGitHubEventAtomic(r.Context(), event); err != nil {
			http.Error(rw, "event processing failed", http.StatusServiceUnavailable)
			return
		}
		rw.WriteHeader(http.StatusAccepted)
		return
	}
	claimed, err := w.Store.ClaimWebhookDelivery(r.Context(), id, kind)
	if err != nil {
		http.Error(rw, "webhook storage unavailable", http.StatusServiceUnavailable)
		return
	}
	if !claimed {
		rw.WriteHeader(http.StatusAccepted)
		return
	}
	if err = w.Handler.HandleGitHubEvent(r.Context(), event); err != nil {
		if store, ok := w.Store.(retryableDeliveryStore); ok {
			_ = store.ReleaseWebhookDelivery(r.Context(), id)
		}
		http.Error(rw, "event processing failed", http.StatusServiceUnavailable)
		return
	}
	rw.WriteHeader(http.StatusAccepted)
}

func validSignature(secret, body []byte, header string) bool {
	if !strings.HasPrefix(header, "sha256=") {
		return false
	}
	got, err := hex.DecodeString(strings.TrimPrefix(header, "sha256="))
	if err != nil {
		return false
	}
	h := hmac.New(sha256.New, secret)
	_, _ = h.Write(body)
	return hmac.Equal(got, h.Sum(nil))
}

func decodeEvent(kind, id string, body []byte) (Event, error) {
	e := Event{DeliveryID: id, Type: kind}
	switch kind {
	case "pull_request":
		var p struct {
			Action     string `json:"action"`
			Number     int    `json:"number"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
			PR struct {
				Head struct {
					SHA string `json:"sha"`
				} `json:"head"`
				Base struct {
					SHA string `json:"sha"`
				} `json:"base"`
			} `json:"pull_request"`
		}
		if json.Unmarshal(body, &p) != nil || p.Number <= 0 || !fullSHA(p.PR.Head.SHA) || (p.PR.Base.SHA != "" && !fullSHA(p.PR.Base.SHA)) {
			return e, errors.New("invalid pull request event")
		}
		e.Action, e.Number, e.Repository, e.HeadSHA, e.BaseSHA = p.Action, p.Number, p.Repository.FullName, p.PR.Head.SHA, p.PR.Base.SHA
		if e.Action != "opened" && e.Action != "reopened" && e.Action != "synchronize" && e.Action != "ready_for_review" && e.Action != "closed" {
			return e, errors.New("unsupported pull request action")
		}
	case "push":
		var p struct {
			Ref        string `json:"ref"`
			After      string `json:"after"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
		}
		if json.Unmarshal(body, &p) != nil || !fullSHA(p.After) {
			return e, errors.New("invalid push event")
		}
		e.Ref, e.HeadSHA, e.Repository = p.Ref, p.After, p.Repository.FullName
	case "workflow_run":
		var p struct {
			Action     string `json:"action"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
			Run struct {
				ID         int64  `json:"id"`
				HeadSHA    string `json:"head_sha"`
				Conclusion string `json:"conclusion"`
			} `json:"workflow_run"`
		}
		if json.Unmarshal(body, &p) != nil || p.Run.ID <= 0 || !fullSHA(p.Run.HeadSHA) {
			return e, errors.New("invalid workflow run event")
		}
		e.Action, e.Repository, e.WorkflowRunID, e.HeadSHA, e.Conclusion = p.Action, p.Repository.FullName, p.Run.ID, p.Run.HeadSHA, p.Run.Conclusion
		if p.Action != "completed" && p.Action != "requested" && p.Action != "in_progress" {
			return e, errors.New("unsupported workflow run action")
		}
	default:
		return e, errors.New("unsupported event type")
	}
	return e, nil
}
