package broker

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"strconv"
	"sync"
	"time"
)

var clientOrderIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func ValidateClientOrderID(value string) error {
	if !clientOrderIDPattern.MatchString(value) {
		return NewOrderError("invalid_order", "client_order_id must use 1–64 letters, numbers, hyphens or underscores")
	}
	return nil
}

// OrderSessionState keeps contract selections and submission attempts scoped to
// one connection. An uncertain submission is never sent a second time by the
// same session, even if the browser repeats its request.
type OrderSessionState struct {
	mu          sync.Mutex
	instruments map[int64]Instrument
	attempts    map[string]OrderAttemptState
	previews    map[string]struct {
		request OrderRequest
		at      time.Time
	}
}

func (s *OrderSessionState) RememberInstrument(instrument Instrument) Instrument {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(instrument.Symbol))
	// The Web client carries ConID as a JavaScript number.
	id := int64(hash.Sum64() & ((1 << 53) - 1))
	if id == 0 {
		id = 1
	}
	instrument.ConID = id
	s.mu.Lock()
	if s.instruments == nil {
		s.instruments = make(map[int64]Instrument)
	}
	s.instruments[id] = instrument
	s.mu.Unlock()
	return instrument
}

func (s *OrderSessionState) Instrument(id string) (Instrument, error) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n <= 0 {
		return Instrument{}, NewOrderError("invalid_order", "select a broker instrument before submitting")
	}
	s.mu.Lock()
	instrument, ok := s.instruments[n]
	s.mu.Unlock()
	if !ok {
		return Instrument{}, NewOrderError("invalid_order", "instrument selection expired; search again")
	}
	return instrument, nil
}

func (s *OrderSessionState) Begin(req OrderRequest) (OrderAttemptState, bool, error) {
	if req.ClientOrderID == "" {
		return OrderAttemptState{}, false, NewOrderError("invalid_order", "client_order_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attempts == nil {
		s.attempts = make(map[string]OrderAttemptState)
	}
	if old, ok := s.attempts[req.ClientOrderID]; ok {
		if old.Request != req {
			return OrderAttemptState{}, false, NewOrderError("duplicate_order", "client_order_id belongs to another order")
		}
		return old, true, nil
	}
	preview, ok := s.previews[req.ClientOrderID]
	if !ok || preview.request != req || time.Since(preview.at) > 10*time.Minute {
		return OrderAttemptState{}, false, NewOrderError("invalid_order", "order preview expired or changed; preview again before submitting")
	}
	s.attempts[req.ClientOrderID] = OrderAttemptState{Request: req, Code: "order_outcome_unknown", Error: "submission is in progress; check broker status before trying again"}
	delete(s.previews, req.ClientOrderID)
	return OrderAttemptState{}, false, nil
}

func (s *OrderSessionState) RememberPreview(req OrderRequest) {
	if req.ClientOrderID == "" {
		return
	}
	s.mu.Lock()
	if s.previews == nil {
		s.previews = make(map[string]struct {
			request OrderRequest
			at      time.Time
		})
	}
	s.previews[req.ClientOrderID] = struct {
		request OrderRequest
		at      time.Time
	}{request: req, at: time.Now()}
	s.mu.Unlock()
}

func (s *OrderSessionState) Finish(req OrderRequest, order Order, err error) {
	state := OrderAttemptState{Request: req, Order: order}
	if err != nil {
		state.Error, state.Code = err.Error(), "order_outcome_unknown"
		if classified, ok := err.(*OrderError); ok {
			state.Code = classified.Code
		}
	}
	s.mu.Lock()
	if s.attempts == nil {
		s.attempts = make(map[string]OrderAttemptState)
	}
	s.attempts[req.ClientOrderID] = state
	s.mu.Unlock()
}

func (s *OrderSessionState) Attempt(account, clientID string) (OrderAttemptState, error) {
	s.mu.Lock()
	state, ok := s.attempts[clientID]
	s.mu.Unlock()
	if !ok || state.Request.AccountID != account {
		return OrderAttemptState{}, fmt.Errorf("order attempt not found; reconcile with broker before resubmitting")
	}
	return state, nil
}
