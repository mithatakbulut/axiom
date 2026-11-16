package control

import (
	"context"
	"encoding/json"
	"fmt"
	"vigil/internal/advisor"
	"vigil/internal/config"
	"vigil/internal/recorder"
)

type Service struct {
	Config     config.Config
	ConfigPath string // Absolute path when the caller selected an explicit config.
	Store      *recorder.Store
	Advisor    advisor.Runner
}
type Status struct {
	Mode                  string `json:"mode"`
	Paused                bool   `json:"paused"`
	Events                int    `json:"events"`
	Database              string `json:"database"`
	InterventionAvailable bool   `json:"intervention_available"`
}

func (s Service) Status(ctx context.Context) (Status, error) {
	var st Status
	var err error
	st.Mode, st.Paused, err = s.settings(ctx)
	if err != nil {
		return st, err
	}
	st.InterventionAvailable = s.Config.Escalation.Enabled && st.Mode != "observe" && !st.Paused
	st.Events, err = s.Store.Check(ctx)
	st.Database = s.Config.Storage.Path
	return st, err
}
func (s Service) SetMode(ctx context.Context, mode string) error {
	if !config.ValidMode(mode) {
		return fmt.Errorf("invalid mode")
	}
	return s.Store.SetSetting(ctx, "mode", mode)
}
func (s Service) SetPaused(ctx context.Context, paused bool) error {
	return s.Store.SetSetting(ctx, "paused", fmt.Sprint(paused))
}
func (s Service) History(ctx context.Context, limit int) ([]recorder.HistoryEvent, error) {
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("limit must be 1–100")
	}
	return s.Store.History(ctx, limit)
}
func (s Service) settings(ctx context.Context) (string, bool, error) {
	mode, err := s.Store.Setting(ctx, "mode", s.Config.Mode)
	if err != nil {
		return "", false, err
	}
	p, err := s.Store.Setting(ctx, "paused", "false")
	return mode, p == "true", err
}

type Explanation struct {
	Progress json.RawMessage      `json:"latest_progress,omitempty"`
	Advisor  *recorder.Escalation `json:"latest_advisor,omitempty"`
}

func (s Service) Explain(ctx context.Context, session string) (Explanation, error) {
	p, err := s.Store.LastProgress(ctx, session)
	if err != nil {
		return Explanation{}, err
	}
	a, err := s.Store.LastAdvisor(ctx, session)
	return Explanation{Progress: p, Advisor: a}, err
}
